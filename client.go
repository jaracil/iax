package iax

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

type IAXTrunkState int32

const (
	Uninitialized IAXTrunkState = iota
	Ready
	ShutingDown
	killed
)

func (cs IAXTrunkState) String() string {
	switch cs {
	case Uninitialized:
		return "Uninitialized"
	case Ready:
		return "Ready"
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

type IAXTrunkEventKind int

const (
	IncomingCallIAXTrunkEvent IAXTrunkEventKind = iota
	RegisterIAXTrunkEvent
	StateChangeIAXTrunkEvent
)

func (it IAXTrunkEventKind) String() string {
	switch it {
	case IncomingCallIAXTrunkEvent:
		return "IncomingCall"
	case RegisterIAXTrunkEvent:
		return "Register"
	case StateChangeIAXTrunkEvent:
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

func (e *IncomingCallEvent) Kind() IAXTrunkEventKind {
	return IncomingCallIAXTrunkEvent
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

func (e *RegistrationEvent) Kind() IAXTrunkEventKind {
	return RegisterIAXTrunkEvent
}

type IAXTrunkStateChangeEvent struct {
	State     IAXTrunkState
	PrevState IAXTrunkState
	TimeStamp time.Time
}

func (e *IAXTrunkStateChangeEvent) Timestamp() time.Time {
	return e.TimeStamp
}

func (e *IAXTrunkStateChangeEvent) SetTimestamp(ts time.Time) {
	e.TimeStamp = ts
}

func (e *IAXTrunkStateChangeEvent) Kind() IAXTrunkEventKind {
	return StateChangeIAXTrunkEvent
}

type IAXTrunkEvent interface {
	Kind() IAXTrunkEventKind
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

// IAXTrunkOptions are the options for the IAX trunk
type IAXTrunkOptions struct {
	BindAddr         string
	FrameTimeout     time.Duration
	EvtQueueSize     int
	SendQueueSize    int
	CallEvtQueueSize int
	CallFrmQueueSize int
	Ctx              context.Context
	DebugMiniframes  bool
}

func (it *IAXTrunk) pushEvent(evt IAXTrunkEvent) {
	if evt.Timestamp().IsZero() {
		evt.SetTimestamp(time.Now())
	}
	select {
	case it.evtQueue <- evt:
	default:
		it.log(ErrorLogLevel, "IAX trunk event queue full")
	}
}

func (it *IAXTrunk) WaitEvent(timeout time.Duration) (IAXTrunkEvent, error) {
	ctx, cancel := context.WithTimeout(it.ctx, timeout)
	defer cancel()
	select {
	case evt := <-it.evtQueue:
		return evt, nil
	case <-ctx.Done():
		if ctx.Err() == context.DeadlineExceeded {
			return nil, ErrTimeout
		}
		return nil, ctx.Err()
	}
}

// IAXTrunk is an IAX trunk
type IAXTrunk struct {
	options       *IAXTrunkOptions
	conn          *net.UDPConn
	ctx           context.Context
	cancel        context.CancelFunc
	sendQueue     chan Frame
	evtQueue      chan IAXTrunkEvent
	lock          sync.RWMutex
	state         IAXTrunkState
	callIDCount   uint16
	localCallMap  map[uint16]*Call
	remoteCallMap map[remoteCallKey]*Call
	peers         map[string]*Peer
	logLevel      LogLevel
	localAddr     *net.UDPAddr
}

// SetLogLevel sets the IAX trunk log level
func (it *IAXTrunk) SetLogLevel(level LogLevel) {
	it.logLevel = level
}

// LogLevel returns the IAX trunk log level
func (it *IAXTrunk) LogLevel() LogLevel {
	return it.logLevel
}

// log logs a message
func (it *IAXTrunk) log(level LogLevel, format string, args ...interface{}) {
	if it.logLevel != DisabledLogLevel && level >= it.logLevel {
		format = fmt.Sprintf("[%s] %s", level, format)
		log.Printf(format, args...)
	}
}

func (it *IAXTrunk) AddPeer(peer *Peer) {
	it.peers[peer.User] = peer
}

func (it *IAXTrunk) Peer(user string) *Peer {
	return it.peers[user]
}

func (it *IAXTrunk) Peers() map[string]*Peer {
	return it.peers
}

func (it *IAXTrunk) DelPeer(user string) {
	delete(it.peers, user)
}

func (it *IAXTrunk) PeerAddress(user string) (*net.UDPAddr, error) {
	peer := it.Peer(user)
	if peer == nil {
		return nil, ErrPeerNotFound
	}
	peerAddr := peer.Address()
	if peerAddr == nil {
		return nil, ErrPeerUnreachable
	}
	return peerAddr, nil
}

func (it *IAXTrunk) Poke(peerUsr string) (*FullFrame, error) {
	call := NewCall(it)
	return call.poke(peerUsr)
}

func (it *IAXTrunk) registerLoop() {
	for it.State() == Ready {
		time.Sleep(time.Second)

		peers := it.Peers()
		for _, peer := range peers {
			if peer.RegOutInterval > 0 {
				if time.Now().After(peer.nextRegOutTime) {
					go func(p *Peer) {
						err := it.register(p.User)
						if err != nil {
							p.RegOutOK = false
							p.RegOutErr = err
							it.pushEvent(&RegistrationEvent{
								Peer:       p.User,
								Registered: false,
								Cause:      err.Error(),
							})
						} else {
							p.RegOutOK = true
							p.RegOutErr = nil
							it.pushEvent(&RegistrationEvent{
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

func (it *IAXTrunk) register(peer string) error {
	call := NewCall(it)
	return call.register(peer)
}

// routeFrame routes a frame to the appropriate call
func (it *IAXTrunk) routeFrame(frame Frame) {
	if it.logLevel > DisabledLogLevel {
		if it.logLevel <= UltraDebugLogLevel {
			if frame.IsFullFrame() || it.options.DebugMiniframes {
				it.log(UltraDebugLogLevel, "RX %s", frame)
			}
		}
	}
	it.lock.RLock()
	call, ok := it.remoteCallMap[newRemoteCallKeyFromFrame(frame)]
	it.lock.RUnlock()
	if ok {
		call.pushFrame(frame)
		return
	}
	it.lock.RLock()
	call, ok = it.localCallMap[frame.DstCallNumber()]
	it.lock.RUnlock()
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
			it.SendFrame(oFrm)
		} else {
			if ffrm.FrameType() == FrmIAXCtl {
				switch ffrm.Subclass() {
				case IAXCtlNew:
					if it.State() == Ready {
						call := NewCall(it)
						call.pushFrame(frame)
						it.pushEvent(&IncomingCallEvent{
							Call: call,
						})
					}
				case IAXCtlRegReq:
					if it.State() == Ready {
						call := NewCall(it)
						call.pushFrame(frame)
					}
				case IAXCtlPoke:
					if it.State() == Ready {
						call := NewCall(it)
						call.pushFrame(frame)
					}
				}
			}
		}
	}
}

// senderTask sends frames to the peers
func (it *IAXTrunk) senderTask() {
	for it.State() != Uninitialized {
		select {
		case frm := <-it.sendQueue:
			data := frm.Encode()
			addr := frm.PeerAddr()
			if addr == nil {
				it.log(ErrorLogLevel, "No peer address for frame: %s", frm)
				continue
			}
			n, err := it.conn.WriteToUDP(data, addr)
			if err != nil || n != len(data) {
				it.Kill()
				return
			}
		case <-it.ctx.Done():
			it.Kill()
			return
		}
	}
}

// receiverTask receives frames from the peers
func (it *IAXTrunk) receiverTask() {
	for it.State() != Uninitialized {
		data := make([]byte, FrameMaxSize)
		n, addr, err := it.conn.ReadFromUDP(data)
		if err != nil {
			break
		}
		frm, err := DecodeFrame(data[:n])
		if err != nil {
			it.log(ErrorLogLevel, "Error decoding frame: %s", err)
			continue
		}
		frm.SetPeerAddr(addr)
		it.routeFrame(frm)
	}
	it.Kill()
}

// NewIAXTrunk creates a new IAX trunk
func NewIAXTrunk(options *IAXTrunkOptions) *IAXTrunk {
	it := &IAXTrunk{
		options:       options,
		state:         Uninitialized,
		localCallMap:  make(map[uint16]*Call),
		remoteCallMap: make(map[remoteCallKey]*Call),
		peers:         make(map[string]*Peer),
	}
	evtQueueSize := it.options.EvtQueueSize
	if evtQueueSize == 0 {
		evtQueueSize = 20
	}
	sendQueueSize := it.options.SendQueueSize
	if sendQueueSize == 0 {
		sendQueueSize = 100
	}
	it.sendQueue = make(chan Frame, sendQueueSize)
	it.evtQueue = make(chan IAXTrunkEvent, evtQueueSize)
	return it
}

// State returns the IAX trunk state
func (it *IAXTrunk) State() IAXTrunkState {
	return IAXTrunkState(atomic.LoadInt32((*int32)(&it.state)))
}

// Init initializes the IAX trunk
func (it *IAXTrunk) Init() error {
	if it.State() != Uninitialized {
		return ErrInvalidState
	}

	if it.options.Ctx != nil {
		it.ctx, it.cancel = context.WithCancel(it.options.Ctx)
	} else {
		it.ctx, it.cancel = context.WithCancel(context.Background())
	}

	var err error

	if it.options.BindAddr != "" {
		it.localAddr, err = net.ResolveUDPAddr("udp", it.options.BindAddr)
		if err != nil {
			return err
		}
	} else {
		it.localAddr, err = net.ResolveUDPAddr("udp", "0.0.0.0:4569")
		if err != nil {
			return err
		}
	}
	conn, err := net.ListenUDP("udp", it.localAddr)
	if err != nil {
		return err
	}
	it.conn = conn
	it.setState(Ready)
	go it.senderTask()
	go it.receiverTask()
	go it.registerLoop()
	return nil
}

// ShutDown shuts down the IAX trunk
// It will hangup all calls and disconnect from the peers
func (it *IAXTrunk) ShutDown() {
	it.setState(ShutingDown)
	for retry := 0; retry < 3; retry++ {
		for _, call := range it.localCallMap {
			call.Hangup("shutdown", 0)
		}
		time.Sleep(time.Second)
		if len(it.localCallMap) == 0 {
			break
		}
	}
	// TODO: Unregister if registered
	it.Kill()
}

// Kill kills the IAX trunk without shutting down the calls
func (it *IAXTrunk) Kill() {
	if it.State() != killed {
		it.setState(killed)

		if it.conn != nil {
			it.conn.Close()
		}

		if it.cancel != nil {
			it.cancel()
		}
	}
}

// SendFrame sends a frame to the peer
func (it *IAXTrunk) SendFrame(frame Frame) {
	if it.State() != Uninitialized {
		if it.logLevel > DisabledLogLevel {
			if it.logLevel <= UltraDebugLogLevel {
				if frame.IsFullFrame() || it.options.DebugMiniframes {
					it.log(UltraDebugLogLevel, "TX %s", frame)
				}
			}
		}
		it.sendQueue <- frame
	}
}

// Options returns a copy of the IAX trunk options
func (it *IAXTrunk) Options() *IAXTrunkOptions {
	opts := *it.options
	return &opts
}

func (it *IAXTrunk) setState(state IAXTrunkState) {
	if state != it.State() {
		oldState := it.State()
		atomic.StoreInt32((*int32)(&it.state), int32(state))
		it.pushEvent(&IAXTrunkStateChangeEvent{
			State:     state,
			PrevState: oldState,
		})
		it.log(DebugLogLevel, "IAX trunk state change %v -> %v", oldState, state)
	}
}
