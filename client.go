package iax

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

type ClientState int

const (
	Disconnected ClientState = iota
	Disconnecting
	Connected
	Connecting
)

func (cs ClientState) String() string {
	switch cs {
	case Disconnected:
		return "Disconnected"
	case Disconnecting:
		return "Disconnecting"
	case Connected:
		return "Connected"
	case Connecting:
		return "Connecting"
	default:
		return "Unknown"
	}
}

type LogLevel int

const (
	DisabledLogLevel LogLevel = iota
	UltraDebugLogLevel
	DebugLogLevel
	InfoLogLevel
	WarningLogLevel
	ErrorLogLevel
	CriticalLogLevel
)

// String returns the string representation of the log level
func (ll LogLevel) String() string {
	switch ll {
	case DisabledLogLevel:
		return "Disabled"
	case UltraDebugLogLevel:
		return "UltraDebug"
	case DebugLogLevel:
		return "Debug"
	case InfoLogLevel:
		return "Info"
	case WarningLogLevel:
		return "Warning"
	case ErrorLogLevel:
		return "Error"
	case CriticalLogLevel:
		return "Critical"
	default:
		return "Unknown"
	}
}

type ClientEventKind int

const (
	NewCallEventKind ClientEventKind = iota
)

func (et ClientEventKind) String() string {
	switch et {
	case NewCallEventKind:
		return "NewCall"
	default:
		return "Unknown"
	}
}

type NewCallEvent struct {
	Call      *Call
	TimeStamp time.Time
}

func (e *NewCallEvent) Kind() ClientEventKind {
	return NewCallEventKind
}

type ClientEvent interface {
	Kind() ClientEventKind
}

// ClientOptions are the options for the client
type ClientOptions struct {
	Host             string
	Port             int
	BindPort         int
	Username         string
	Password         string
	RegInterval      time.Duration
	EvtQueueSize     int
	SendQueueSize    int
	CallEvtQueueSize int
	CallFrmQueueSize int
	Ctx              context.Context
}

func (c *Client) pushEvent(evt ClientEvent) {
	select {
	case c.evtQueue <- evt:
	default:
		c.log(ErrorLogLevel, "Client event queue full")
	}
}

func (c *Client) WaitEvent(timeout time.Duration) (ClientEvent, error) {
	ctx, cancel := context.WithTimeout(c.ctx, timeout)
	defer cancel()
	select {
	case evt := <-c.evtQueue:
		return evt, nil
	case <-ctx.Done():
		if ctx.Err() == context.DeadlineExceeded {
			return nil, ErrTimeout
		}
		return nil, ctx.Err()
	}
}

// Client is an IAX2 client connection
type Client struct {
	options       *ClientOptions
	conn          *net.UDPConn
	ctx           context.Context
	cancel        context.CancelFunc
	sendQueue     chan Frame
	evtQueue      chan ClientEvent
	lock          sync.RWMutex
	state         ClientState
	callIDCount   uint16
	localCallMap  map[uint16]*Call
	remoteCallMap map[uint16]*Call
	lastRegTime   time.Time
	regInterval   time.Duration
	logLevel      LogLevel
}

// SetLogLevel sets the client log level
func (c *Client) SetLogLevel(level LogLevel) {
	c.logLevel = level
}

// LogLevel returns the client log level
func (c *Client) LogLevel() LogLevel {
	return c.logLevel
}

// log logs a message
func (c *Client) log(level LogLevel, format string, args ...interface{}) {
	if c.logLevel != DisabledLogLevel && level >= c.logLevel {
		format = fmt.Sprintf("[%s] %s", level, format)
		log.Printf(format, args...)
	}
}

func (c *Client) schedRegister() {
	if c.state == Connected {
		c.Register()
	}
}

func (c *Client) Register() error {

	call := NewCall(c)
	defer call.Destroy()

	oFrm := NewFullFrame(FrmIAXCtl, IAXCtlRegReq)
	oFrm.AddIE(StringIE(IEUsername, c.options.Username))
	oFrm.AddIE(Uint16IE(IERefresh, uint16(c.options.RegInterval.Seconds())))

	rFrm, err := call.sendFullFrame(oFrm)

	if err != nil {
		return err
	}

	if rFrm.FrameType() != FrmIAXCtl {
		return ErrUnexpectedFrameType
	}

	if rFrm.Subclass() == IAXCtlRegAck {
		return nil
	}

	if rFrm.Subclass() == IAXCtlRegAuth {
		ie := rFrm.FindIE(IEAuthMethods)
		if ie == nil {
			return ErrMissingIE
		}
		if ie.AsUint16()&0x0002 == 0 {
			return ErrUnsupportedAuthMethod
		}
		ie = rFrm.FindIE(IEChallenge)
		if ie == nil {
			return ErrMissingIE
		}
		challenge := ie.AsString()
		md5Digest := md5.Sum([]byte(challenge + c.options.Password))
		challengeResponse := hex.EncodeToString(md5Digest[:])

		c.log(DebugLogLevel, "Challenge: %s", challenge)

		oFrm = NewFullFrame(FrmIAXCtl, IAXCtlRegReq)
		oFrm.AddIE(StringIE(IEUsername, c.options.Username))
		oFrm.AddIE(Uint16IE(IERefresh, uint16(c.options.RegInterval.Seconds())))
		oFrm.AddIE(StringIE(IEMD5Result, challengeResponse))
		rFrm, err = call.sendFullFrame(oFrm)
		if err != nil {
			return err
		}

		if rFrm.FrameType() != FrmIAXCtl {
			return ErrUnexpectedFrameType
		}

		if rFrm.Subclass() == IAXCtlRegAck {
			c.lastRegTime = time.Now()
			ie := rFrm.FindIE(IERefresh)
			if ie != nil {
				c.regInterval = time.Duration(ie.AsUint16()) * time.Second
			} else {
				c.regInterval = time.Second * 60
			}
			// Schedule next registration
			time.AfterFunc(c.regInterval-(5*time.Second), c.schedRegister)
			return nil
		}

		if rFrm.Subclass() == IAXCtlRegRej {
			return ErrConnectionRejected
		}
	}
	return errors.New("not implemented")
}

// routeFrame routes a frame to the appropriate call
func (c *Client) routeFrame(frame Frame) {
	if c.logLevel > DisabledLogLevel && c.logLevel <= UltraDebugLogLevel {
		c.log(UltraDebugLogLevel, "RX %s", frame)
	}
	c.lock.RLock()
	call, ok := c.remoteCallMap[frame.SrcCallNumber()]
	c.lock.RUnlock()
	if ok {
		call.pushFrame(frame)
		return
	}
	c.lock.RLock()
	call, ok = c.localCallMap[frame.DstCallNumber()]
	c.lock.RUnlock()
	if ok {
		call.pushFrame(frame)
		return
	}
	if frame.IsFullFrame() {
		ffrm := frame.(*FullFrame)
		if ffrm.DstCallNumber() != 0 { // Respond invalid full frames
			oFrm := NewFullFrame(FrmIAXCtl, IAXCtlInval)
			oFrm.SetTimestamp(ffrm.Timestamp())
			oFrm.SetSrcCallNumber(ffrm.DstCallNumber())
			oFrm.SetDstCallNumber(ffrm.SrcCallNumber())
			oFrm.SetISeqNo(ffrm.OSeqNo())
			oFrm.SetOSeqNo(ffrm.ISeqNo())
			c.SendFrame(oFrm)
		} else {
			if ffrm.FrameType() == FrmIAXCtl {
				if ffrm.Subclass() == IAXCtlNew { // New incoming call
					call := NewCall(c)
					call.pushFrame(frame)
					c.pushEvent(&NewCallEvent{call, time.Now()})
				} else if ffrm.Subclass() == IAXCtlPoke {
					call := NewCall(c)
					call.pushFrame(frame)
					go func() {
						time.Sleep(time.Second)
						call.Destroy()
					}()
				}
			}
		}
	}
}

// sender sends frames to the server
func (c *Client) sender() {
	for c.state != Disconnected {
		frm, ok := <-c.sendQueue
		if !ok {
			break
		}
		data := frm.Encode()
		n, err := c.conn.Write(data)
		if err != nil || n != len(data) {
			break
		}
	}
}

// receiver receives frames from the server
func (c *Client) receiver() {
	for c.state != Disconnected {
		data := make([]byte, FrameMaxSize)
		n, err := c.conn.Read(data)
		if err != nil {
			break
		}

		frm, err := DecodeFrame(data[:n])
		if err != nil {
			c.log(ErrorLogLevel, "Error decoding frame: %s", err)
			continue
		}
		c.routeFrame(frm)
	}

}

// NewClient creates a new IAX2 client
func NewClient(options *ClientOptions) *Client {
	return &Client{
		options: options,
		state:   Disconnected,
	}
}

// State returns the client state
func (c *Client) State() ClientState {
	return c.state
}

// Connect connects to the server
func (c *Client) Connect() error {
	if c.state != Disconnected {
		return ErrInvalidState
	}
	c.state = Connecting
	ctx, cancel := context.WithCancel(context.Background())
	if c.options.Ctx != nil {
		ctx, cancel = context.WithCancel(c.options.Ctx)
	}
	evtQueueSize := c.options.EvtQueueSize
	if evtQueueSize == 0 {
		evtQueueSize = 20
	}
	sendQueueSize := c.options.SendQueueSize
	if sendQueueSize == 0 {
		sendQueueSize = 100
	}
	c.ctx = ctx
	c.cancel = cancel
	c.sendQueue = make(chan Frame, sendQueueSize)
	c.evtQueue = make(chan ClientEvent, evtQueueSize)
	c.localCallMap = make(map[uint16]*Call)
	c.remoteCallMap = make(map[uint16]*Call)

	defer func() {
		if c.state == Connecting {
			c.Disconnect()
		}
	}()

	var err error
	var rAddr *net.UDPAddr = nil
	if c.options.Host != "" {
		if c.options.Port == 0 {
			c.options.Port = 4569 // Default IAX port
		}
		rAddrStr := c.options.Host
		if !strings.Contains(rAddrStr, ":") {
			rAddrStr = rAddrStr + ":" + strconv.Itoa(c.options.Port)
		}

		rAddr, err = net.ResolveUDPAddr("udp", rAddrStr)
		if err != nil {
			return err
		}
	}

	var lAddr *net.UDPAddr = nil
	if c.options.BindPort != 0 {
		lAddrStr := "0.0.0.0:" + strconv.Itoa(c.options.BindPort)
		lAddr, err = net.ResolveUDPAddr("udp", lAddrStr)
		if err != nil {
			return err
		}
	}

	conn, err := net.DialUDP("udp", lAddr, rAddr)
	if err != nil {
		return err
	}

	c.conn = conn
	go c.sender()
	go c.receiver()

	if c.options.RegInterval > 0 {
		err = c.Register()
		if err != nil {
			return err
		}
	}

	c.state = Connected

	return nil
}

// Disconnect disconnects from the server
func (c *Client) Disconnect() {
	if c.state == Connected || c.state == Connecting {
		c.state = Disconnecting
		// TODO: Hangup all calls
		// TODO: send unregistration
		c.state = Disconnected

		if c.sendQueue != nil {
			close(c.sendQueue)
			c.sendQueue = nil
		}

		if c.evtQueue != nil {
			close(c.evtQueue)
			c.evtQueue = nil
		}

		if c.conn != nil {
			c.conn.Close()
			c.conn = nil
		}

		c.cancel()

		c.ctx = nil
		c.cancel = nil
		c.localCallMap = nil
		c.remoteCallMap = nil
	}
}

// SendFrame sends a frame to the server
func (c *Client) SendFrame(frame Frame) {
	if c.state != Disconnected {
		if c.logLevel > DisabledLogLevel && c.logLevel <= UltraDebugLogLevel {
			c.log(UltraDebugLogLevel, "TX %s", frame)
		}
		c.sendQueue <- frame
	}
}

// Options returns a copy of the client options
func (c *Client) Options() *ClientOptions {
	opts := *c.options
	return &opts
}
