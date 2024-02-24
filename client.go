package iax

import (
	"context"
	"crypto/md5"
	"encoding/hex"
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
	Connected
	ShutingDown
	killed
)

func (cs ClientState) String() string {
	switch cs {
	case Disconnected:
		return "Disconnected"
	case Connected:
		return "Connected"
	case ShutingDown:
		return "ShutingDown"
	case killed:
		return "killed"
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
	IncomingCallClientEvent ClientEventKind = iota
	RegisterClientEvent
	StateChangeClientEvent
)

func (et ClientEventKind) String() string {
	switch et {
	case IncomingCallClientEvent:
		return "IncomingCall"
	case RegisterClientEvent:
		return "Register"
	case StateChangeClientEvent:
		return "StateChange"
	default:
		return "Unknown"
	}
}

type IncomingCallEvent struct {
	Call      *Call
	TimeStamp time.Time
}

func (e *IncomingCallEvent) Timestamp() time.Time {
	return e.TimeStamp
}

func (e *IncomingCallEvent) SetTimestamp(ts time.Time) {
	e.TimeStamp = ts
}

func (e *IncomingCallEvent) Kind() ClientEventKind {
	return IncomingCallClientEvent
}

type RegistrationEvent struct {
	Registered bool
	Cause      string
	TimeStamp  time.Time
}

func (e *RegistrationEvent) Timestamp() time.Time {
	return e.TimeStamp
}

func (e *RegistrationEvent) SetTimestamp(ts time.Time) {
	e.TimeStamp = ts
}

func (e *RegistrationEvent) Kind() ClientEventKind {
	return RegisterClientEvent
}

type ClientStateChangeEvent struct {
	State     ClientState
	PrevState ClientState
	TimeStamp time.Time
}

func (e *ClientStateChangeEvent) Timestamp() time.Time {
	return e.TimeStamp
}

func (e *ClientStateChangeEvent) SetTimestamp(ts time.Time) {
	e.TimeStamp = ts
}

func (e *ClientStateChangeEvent) Kind() ClientEventKind {
	return StateChangeClientEvent
}

type ClientEvent interface {
	Kind() ClientEventKind
	Timestamp() time.Time
	SetTimestamp(time.Time)
}

// ClientOptions are the options for the client
type ClientOptions struct {
	Host             string
	Port             int
	BindPort         int
	Username         string
	Password         string
	RegInterval      time.Duration
	FrameTimeout     time.Duration
	EvtQueueSize     int
	SendQueueSize    int
	CallEvtQueueSize int
	CallFrmQueueSize int
	Ctx              context.Context
	DebugMiniframes  bool
}

func (c *Client) pushEvent(evt ClientEvent) {
	if evt.Timestamp().IsZero() {
		evt.SetTimestamp(time.Now())
	}
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
	registered    bool
	logLevel      LogLevel
	raceLock      sync.Mutex
	lAddr         *net.UDPAddr
	rAddr         *net.UDPAddr
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

func (c *Client) Poke() (*FullFrame, error) {
	call := NewCall(c)
	defer call.Hangup("", 0)
	oFrm := NewFullFrame(FrmIAXCtl, IAXCtlPoke)
	rfrm, err := call.sendFullFrame(oFrm)
	if err != nil {
		return nil, err
	}
	if rfrm.FrameType() != FrmIAXCtl || rfrm.Subclass() != IAXCtlPong {
		return nil, ErrUnexpectedFrame
	}
	return rfrm, nil
}

func (c *Client) registerLoop() {
	next := time.Duration(0)
	c.regInterval = c.options.RegInterval
	for c.State() != Disconnected {
		select {
		case <-c.ctx.Done():
			return
		case <-time.After(next):
			err := c.register()
			if err != nil {
				c.registered = false
				c.pushEvent(&RegistrationEvent{
					Registered: false,
					Cause:      err.Error(),
				})
			} else {
				c.registered = true
				c.pushEvent(&RegistrationEvent{
					Registered: true,
				})
			}
			next = c.regInterval - time.Second*2
		}
	}
}

func (c *Client) register() error {
	call := NewCall(c)
	defer call.kill(fmt.Errorf("no longer needed"))

	oFrm := NewFullFrame(FrmIAXCtl, IAXCtlRegReq)
	oFrm.AddIE(StringIE(IEUsername, c.options.Username))
	oFrm.AddIE(Uint16IE(IERefresh, uint16(c.options.RegInterval.Seconds())))

	rFrm, err := call.sendFullFrame(oFrm)

	if err != nil {
		return err
	}

	if rFrm.FrameType() != FrmIAXCtl {
		return ErrUnexpectedFrame
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

		oFrm = NewFullFrame(FrmIAXCtl, IAXCtlRegReq)
		oFrm.AddIE(StringIE(IEUsername, c.options.Username))
		oFrm.AddIE(Uint16IE(IERefresh, uint16(c.options.RegInterval.Seconds())))
		oFrm.AddIE(StringIE(IEMD5Result, challengeResponse))
		rFrm, err = call.sendFullFrame(oFrm)
		if err != nil {
			return err
		}

		if rFrm.FrameType() != FrmIAXCtl {
			return ErrUnexpectedFrame
		}

		if rFrm.Subclass() == IAXCtlRegAck {
			c.lastRegTime = time.Now()
			ie := rFrm.FindIE(IERefresh)
			if ie != nil {
				c.regInterval = time.Duration(ie.AsUint16()) * time.Second
			} else {
				c.regInterval = time.Second * 60
			}
			return nil
		}

		if rFrm.Subclass() == IAXCtlRegRej {
			return ErrRejected
		}
	}
	return ErrUnexpectedFrame
}

// routeFrame routes a frame to the appropriate call
func (c *Client) routeFrame(frame Frame) {
	if c.logLevel > DisabledLogLevel {
		if c.logLevel <= UltraDebugLogLevel {
			if frame.IsFullFrame() || c.options.DebugMiniframes {
				c.log(UltraDebugLogLevel, "RX %s", frame)
			}
		}
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
					if c.State() == Connected {
						call := NewCall(c)
						call.pushFrame(frame)
						c.pushEvent(&IncomingCallEvent{
							Call: call,
						})
					}
				} else if ffrm.Subclass() == IAXCtlPoke {
					if c.State() == Connected {
						call := NewCall(c)
						call.pushFrame(frame)
						go func() {
							time.Sleep(time.Second)
							call.kill(fmt.Errorf("no longer needed"))
						}()
					}
				}
			}
		}
	}
}

// sender sends frames to the server
func (c *Client) sender() {
	for c.State() != Disconnected {
		select {
		case frm := <-c.sendQueue:
			data := frm.Encode()
			n, err := c.conn.WriteToUDP(data, c.rAddr)
			if err != nil || n != len(data) {
				c.Kill()
				return
			}
		case <-c.ctx.Done():
			c.Kill()
			return
		}
	}
}

// receiver receives frames from the server
func (c *Client) receiver() {
	for c.State() != Disconnected {
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
	c.Kill()
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
	c.raceLock.Lock()
	defer c.raceLock.Unlock()
	return c.state
}

// Connect connects to the server
func (c *Client) Connect() error {
	if c.State() != Disconnected {
		return ErrInvalidState
	}

	if c.options.Ctx != nil {
		c.ctx, c.cancel = context.WithCancel(c.options.Ctx)
	} else {
		c.ctx, c.cancel = context.WithCancel(context.Background())
	}

	evtQueueSize := c.options.EvtQueueSize
	if evtQueueSize == 0 {
		evtQueueSize = 20
	}
	sendQueueSize := c.options.SendQueueSize
	if sendQueueSize == 0 {
		sendQueueSize = 100
	}
	c.sendQueue = make(chan Frame, sendQueueSize)
	c.evtQueue = make(chan ClientEvent, evtQueueSize)
	c.localCallMap = make(map[uint16]*Call)
	c.remoteCallMap = make(map[uint16]*Call)

	var err error
	if c.options.Host != "" {
		if c.options.Port == 0 {
			c.options.Port = 4569 // Default IAX port
		}
		rAddrStr := c.options.Host
		if !strings.Contains(rAddrStr, ":") {
			rAddrStr = rAddrStr + ":" + strconv.Itoa(c.options.Port)
		}

		c.rAddr, err = net.ResolveUDPAddr("udp", rAddrStr)
		if err != nil {
			return err
		}
	}

	if c.options.BindPort != 0 {
		lAddrStr := "0.0.0.0:" + strconv.Itoa(c.options.BindPort)
		c.lAddr, err = net.ResolveUDPAddr("udp", lAddrStr)
		if err != nil {
			return err
		}
	}
	conn, err := net.ListenUDP("udp", c.lAddr)
	if err != nil {
		return err
	}
	c.conn = conn
	c.setState(Connected)
	go c.sender()
	go c.receiver()
	if c.options.RegInterval > 0 {
		go c.registerLoop()
	}
	return nil
}

// ShutDown shuts down the client
// It will hangup all calls and disconnect from the server
func (c *Client) ShutDown() {
	c.setState(ShutingDown)
	for retry := 0; retry < 3; retry++ {
		for _, call := range c.localCallMap {
			call.Hangup("shutdown", 0)
		}
		time.Sleep(time.Second)
		if len(c.localCallMap) == 0 {
			break
		}
	}
	// TODO: Unregister if registered
	c.Kill()
}

// Kill kills the client without shutting down the calls
func (c *Client) Kill() {
	if c.State() != killed {
		c.setState(killed)

		if c.conn != nil {
			c.conn.Close()
		}

		if c.cancel != nil {
			c.cancel()
		}
	}
}

// SendFrame sends a frame to the server
func (c *Client) SendFrame(frame Frame) {
	if c.State() != Disconnected {
		if c.logLevel > DisabledLogLevel {
			if c.logLevel <= UltraDebugLogLevel {
				if frame.IsFullFrame() || c.options.DebugMiniframes {
					c.log(UltraDebugLogLevel, "TX %s", frame)
				}
			}
		}
		c.sendQueue <- frame
	}
}

// Options returns a copy of the client options
func (c *Client) Options() *ClientOptions {
	opts := *c.options
	return &opts
}

func (c *Client) setState(state ClientState) {
	c.raceLock.Lock()
	defer c.raceLock.Unlock()
	if state != c.state {
		oldState := c.state
		c.state = state
		c.pushEvent(&ClientStateChangeEvent{
			State:     state,
			PrevState: oldState,
		})
		c.log(DebugLogLevel, "Client state change %v -> %v", oldState, state)
	}
}
