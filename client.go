package iax

import (
	"context"
	"fmt"
	"log"
	"net"
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
	Peer       string
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

type Peer struct {
	User           string
	Password       string
	Host           string
	RegOutInterval time.Duration
	lastRegOutTime time.Time
	nextRegOutTime time.Time
	RegOutOK       bool
	RegOutErr      error
	regInAddr      *net.UDPAddr
	regInInterval  time.Duration
	lastRegInTime  time.Time
	CodecPrefs     []Codec
	CodecCaps      CodecMask
	lock           sync.Mutex
}

func (p *Peer) Address() *net.UDPAddr {
	if p == nil {
		return nil
	}
	p.lock.Lock()
	defer p.lock.Unlock()
	if p.Host != "" {
		addr, err := net.ResolveUDPAddr("udp", p.Host)
		if err != nil {
			return nil
		}
		return addr
	}
	if p.regInAddr != nil {
		return p.regInAddr
	}
	return nil
}

// ClientOptions are the options for the client
type ClientOptions struct {
	BindAddr         string
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
	peers         map[string]*Peer
	logLevel      LogLevel
	raceLock      sync.Mutex
	localAddr     *net.UDPAddr
	defPeerAddr   *net.UDPAddr
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

func (c *Client) AddPeer(peer *Peer) {
	c.peers[peer.User] = peer
}

func (c *Client) Peer(user string) *Peer {
	return c.peers[user]
}

func (c *Client) Peers() map[string]*Peer {
	return c.peers
}

func (c *Client) DelPeer(user string) {
	delete(c.peers, user)
}

func (c *Client) PeerAddress(user string) (*net.UDPAddr, error) {
	peer := c.Peer(user)
	if peer == nil {
		return nil, ErrPeerNotFound
	}
	peerAddr := peer.Address()
	if peerAddr == nil {
		return nil, ErrPeerUnreachable
	}
	return peerAddr, nil
}

func (c *Client) Poke(peerUsr string) (*FullFrame, error) {
	call := NewCall(c)
	return call.poke(peerUsr)
}

func (c *Client) registerLoop() {
	for c.State() == Connected {
		time.Sleep(time.Second)

		peers := c.Peers()
		for _, peer := range peers {
			if peer.RegOutInterval > 0 {
				if time.Now().After(peer.nextRegOutTime) {
					go func(p *Peer) {
						err := c.register(p.User)
						if err != nil {
							p.RegOutOK = false
							p.RegOutErr = err
							c.pushEvent(&RegistrationEvent{
								Peer:       p.User,
								Registered: false,
								Cause:      err.Error(),
							})
						} else {
							p.RegOutOK = true
							p.RegOutErr = nil
							c.pushEvent(&RegistrationEvent{
								Peer:       p.User,
								Registered: true,
							})
						}

					}(peer)
				}
			}
		}
	}
}

func (c *Client) register(peer string) error {
	call := NewCall(c)
	return call.register(peer)
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
			oFrm.SetPeerAddr(ffrm.PeerAddr())
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
							call.kill(ErrLocalHangup)
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
			addr := frm.PeerAddr()
			if addr == nil {
				c.log(ErrorLogLevel, "No peer address for frame: %s", frm)
				continue
			}
			n, err := c.conn.WriteToUDP(data, addr)
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
		n, addr, err := c.conn.ReadFromUDP(data)
		if err != nil {
			break
		}
		frm, err := DecodeFrame(data[:n])
		if err != nil {
			c.log(ErrorLogLevel, "Error decoding frame: %s", err)
			continue
		}
		frm.SetPeerAddr(addr)
		c.routeFrame(frm)
	}
	c.Kill()
}

// NewClient creates a new IAX2 client
func NewClient(options *ClientOptions) *Client {
	c := &Client{
		options:       options,
		state:         Disconnected,
		localCallMap:  make(map[uint16]*Call),
		remoteCallMap: make(map[uint16]*Call),
		peers:         make(map[string]*Peer),
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
	return c
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

	var err error

	if c.options.BindAddr != "" {
		c.localAddr, err = net.ResolveUDPAddr("udp", c.options.BindAddr)
		if err != nil {
			return err
		}
	} else {
		c.localAddr, err = net.ResolveUDPAddr("udp", "0.0.0.0:4569")
		if err != nil {
			return err
		}
	}
	conn, err := net.ListenUDP("udp", c.localAddr)
	if err != nil {
		return err
	}
	c.conn = conn
	c.setState(Connected)
	go c.sender()
	go c.receiver()
	go c.registerLoop()
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
