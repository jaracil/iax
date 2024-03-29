package iax

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

type CallState int32

const (
	IdleCallState CallState = iota
	IncomingCallState
	DialingCallState
	AcceptCallState
	OnlineCallState
	HangupCallState
)

func (cs CallState) String() string {
	switch cs {
	case IdleCallState:
		return "Idle"
	case IncomingCallState:
		return "Incoming"
	case DialingCallState:
		return "Dialing"
	case AcceptCallState:
		return "Accept"
	case OnlineCallState:
		return "Online"
	case HangupCallState:
		return "Hangup"
	default:
		return "Unknown"
	}
}

type CallEventKind int

const (
	StateChangeEventKind CallEventKind = iota
	MediaEventKind
	DTMFEventKind
	PlayEventKind
)

func (ek CallEventKind) String() string {
	switch ek {
	case StateChangeEventKind:
		return "StateChange"
	case MediaEventKind:
		return "Media"
	case DTMFEventKind:
		return "DTMF"
	case PlayEventKind:
		return "Play"
	default:
		return "Unknown"
	}
}

type CallStateChangeEvent struct {
	State     CallState
	PrevState CallState
	ts        time.Time
}

func (e *CallStateChangeEvent) Kind() CallEventKind {
	return StateChangeEventKind
}

func (e *CallStateChangeEvent) Timestamp() time.Time {
	return e.ts
}

func (e *CallStateChangeEvent) SetTimestamp(ts time.Time) {
	e.ts = ts
}

type MediaEvent struct {
	Data  []byte
	Codec Codec
	ts    time.Time
}

func (e *MediaEvent) Kind() CallEventKind {
	return MediaEventKind
}

func (e *MediaEvent) Timestamp() time.Time {
	return e.ts
}

func (e *MediaEvent) SetTimestamp(ts time.Time) {
	e.ts = ts
}

type DTMFEvent struct {
	Digit string
	Start bool
	ts    time.Time
}

func (e *DTMFEvent) Timestamp() time.Time {
	return e.ts
}

func (e *DTMFEvent) SetTimestamp(ts time.Time) {
	e.ts = ts
}

func (e *DTMFEvent) Kind() CallEventKind {
	return DTMFEventKind
}

type CallCtlEvent struct {
	Subclass Subclass
	ts       time.Time
}

func (e *CallCtlEvent) Timestamp() time.Time {
	return e.ts
}

func (e *CallCtlEvent) SetTimestamp(ts time.Time) {
	e.ts = ts
}

func (e *CallCtlEvent) Kind() CallEventKind {
	return DTMFEventKind
}

type PlayEvent struct {
	Playing bool
	ts      time.Time
}

func (e *PlayEvent) Kind() CallEventKind {
	return PlayEventKind
}

func (e *PlayEvent) Timestamp() time.Time {
	return e.ts
}

func (e *PlayEvent) SetTimestamp(ts time.Time) {
	e.ts = ts
}

type CallEvent interface {
	Kind() CallEventKind
	Timestamp() time.Time
	SetTimestamp(time.Time)
}

type ClockSource int

const (
	ClockSourcePeer ClockSource = iota
	ClockSourceLocal
	ClockSourceSoftware
)

type DialOptions struct {
	CalledNumber  string
	CallingNumber string
	DNID          string
	CodecCaps     CodecMask
	CodecPrefs    []Codec
	CodecFormat   Codec
	Language      string
	ADSICPE       uint16
	CallingPres   uint8
	CallingTON    uint8
	CallingTNS    uint16
	CallingANI    string
	PeerTime      time.Time
}
type Call struct {
	iaxTrunk      *IAXTrunk
	respQueue     chan *FullFrame
	evtQueue      chan CallEvent
	frmQueue      chan Frame
	mediaQueue    chan *MediaEvent
	ticker        chan time.Time
	ctx           context.Context
	cancel        context.CancelFunc
	localCallID   uint16
	remoteCallID  uint16
	remoteCallKey remoteCallKey
	oseqno        uint8
	iseqno        uint8
	creationTs    time.Time
	sendLock      sync.Mutex
	raceLock      sync.Mutex
	state         CallState
	peer          *Peer
	dialOptions   *DialOptions
	acceptCodec   Codec
	lastPeerCodec Codec
	outgoing      bool
	mediaPlaying  bool
	mediaStop     bool
	killErr       error
	hangupCause   string
	hangupCode    uint8
	peerAddr      *net.UDPAddr
}

func NewCall(it *IAXTrunk) *Call {
	evtQueueSize := it.options.CallEvtQueueSize
	if evtQueueSize == 0 {
		evtQueueSize = 20
	}
	frmQueueSize := it.options.CallFrmQueueSize
	if frmQueueSize == 0 {
		frmQueueSize = 20
	}

	ctx, cancel := context.WithCancel(it.ctx)
	call := &Call{
		iaxTrunk:   it,
		ctx:        ctx,
		cancel:     cancel,
		respQueue:  make(chan *FullFrame, 3),
		evtQueue:   make(chan CallEvent, evtQueueSize),
		frmQueue:   make(chan Frame, frmQueueSize),
		mediaQueue: make(chan *MediaEvent, 5),
		ticker:     make(chan time.Time),
		creationTs: time.Now(),
	}
	it.lock.Lock()
	for {
		it.callIDCount++
		if it.callIDCount > 0x7fff {
			it.callIDCount = 1
		}
		if _, ok := it.localCallMap[it.callIDCount]; !ok {
			break
		}
	}
	call.localCallID = it.callIDCount
	it.localCallMap[it.callIDCount] = call
	it.lock.Unlock()

	call.log(DebugLogLevel, "Created")
	call.setState(IdleCallState)
	go call.frameLoop()
	return call
}

func (c *Call) frameLoop() {
	for {
		select {
		case frame, ok := <-c.frmQueue:
			if !ok {
				return
			}
			c.processFrame(frame)
		case <-c.ctx.Done():
			return
		}
	}
}

func (c *Call) mediaOutputLoop() {
	needFullFrame := true
	for c.State() != HangupCallState {
		var ev *MediaEvent
		select {
		case ev = <-c.mediaQueue:
		case <-c.ctx.Done():
			return
		}
		var eventTs time.Time
		select {
		case eventTs = <-c.ticker:
		case <-c.ctx.Done():
			return
		}
		if needFullFrame {
			needFullFrame = false
			go func() {
				ffrm := NewFullFrame(FrmVoice, ev.Codec.Subclass())
				ffrm.SetPayload(ev.Data)
				ffrm.SetTimestamp(uint32(eventTs.Sub(c.creationTs).Milliseconds()))
				_, err := c.sendFullFrame(ffrm)
				if err != nil {
					c.log(ErrorLogLevel, "Error sending voice full-frame: %s", err)
					c.kill(err)
					return
				}
			}()
		} else {
			mfrm := NewMiniFrame(ev.Data)
			mfrm.SetTimestamp(uint32(eventTs.Sub(c.creationTs).Milliseconds()))
			c.sendMiniFrame(mfrm)
		}

	}
}

func (c *Call) tickerLoop() {
	nextTick := time.Now()
	for c.State() != HangupCallState {
		if time.Now().Before(nextTick) {
			time.Sleep(time.Until(nextTick))
		}
		select {
		case c.ticker <- nextTick:
		default:
		}
		nextTick = nextTick.Add(time.Millisecond * 20)
	}
}

func (c *Call) PlayMedia(r io.Reader) error {
	c.log(DebugLogLevel, "Playing media with codec %s frame size: %d", c.acceptCodec, c.acceptCodec.FrameSize())
	c.raceLock.Lock()
	if c.mediaPlaying {
		c.raceLock.Unlock()
		return ErrResourceBusy
	}
	c.mediaPlaying = true
	c.mediaStop = false
	c.raceLock.Unlock()
	c.pushEvent(&PlayEvent{
		Playing: true,
	})
	defer func() {
		c.raceLock.Lock()
		c.mediaPlaying = false
		c.mediaStop = false
		c.raceLock.Unlock()
		c.pushEvent(&PlayEvent{
			Playing: false,
		})
	}()
	frameSize := c.acceptCodec.FrameSize()
	for !c.mediaStop && c.State() != HangupCallState {
		buf := make([]byte, frameSize)
		n, err := r.Read(buf)
		if err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
		if n != frameSize {
			break
		}
		select {
		case c.mediaQueue <- &MediaEvent{
			Data:  buf,
			Codec: c.acceptCodec,
		}:
		case <-c.ctx.Done():
			return c.ctx.Err()
		}
	}
	return nil
}

func (c *Call) StopMedia() {
	c.raceLock.Lock()
	if c.mediaPlaying {
		c.mediaStop = true
	}
	c.raceLock.Unlock()
}

func (c *Call) SendMedia(ev *MediaEvent) error {
	if c.State() != OnlineCallState && c.State() != AcceptCallState {
		return ErrInvalidState
	}
	if c.mediaPlaying {
		return nil // Drop sync media when playing async media
	}
	select {
	case c.mediaQueue <- ev:
	case <-c.ctx.Done():
		return c.ctx.Err()
	default:
		return ErrFullBuffer
	}
	return nil
}

// IsOutgoing returns true if the call was initiated by us.
func (c *Call) IsOutgoing() bool {
	return c.outgoing
}

func (c *Call) pushFrame(frame Frame) {
	if c.State() == HangupCallState {
		return
	}
	select {
	case c.frmQueue <- frame:
	default:
		c.log(ErrorLogLevel, "Frame queue full")
	}
}

func (c *Call) processFrame(frame Frame) {
	if c.State() == HangupCallState {
		return
	}
	addr := frame.PeerAddr()
	if c.peerAddr == nil {
		c.peerAddr = addr
		c.log(DebugLogLevel, "Peer address set to %s", addr.String())
	}
	if !addr.IP.Equal(c.peerAddr.IP) {
		c.log(DebugLogLevel, "Frame from different peer address")
		return
	}
	if addr.Port != c.peerAddr.Port {
		c.log(DebugLogLevel, "Frame from different peer port")
		return
	}
	if frame.IsFullFrame() {
		ffrm := frame.(*FullFrame)
		if c.iaxTrunk.logLevel > DisabledLogLevel {
			if c.iaxTrunk.logLevel <= DebugLogLevel {
				c.log(DebugLogLevel, "<< RX frame, type %s, subclass %s, retransmit %t", ffrm.FrameType(), SubclassToString(ffrm.FrameType(), ffrm.Subclass()), ffrm.Retransmit())
			}
		}
		if ffrm.OSeqNo() != c.iseqno {
			if ffrm.Retransmit() && ffrm.OSeqNo() == c.iseqno-1 {
				c.log(DebugLogLevel, "Retransmitted frame received")
				if ffrm.NeedACK() {
					c.sendACK(ffrm)
				}
			} else {
				c.log(DebugLogLevel, "Out of order frame received. Expected %d, got %d", c.iseqno, ffrm.OSeqNo())
			}
			return
		}
		if !(ffrm.FrameType() == FrmIAXCtl && ffrm.Subclass() == IAXCtlAck) {
			c.iseqno++
		}
		if c.remoteCallID == 0 && ffrm.SrcCallNumber() != 0 {
			if !(ffrm.FrameType() == FrmIAXCtl && ffrm.Subclass() == IAXCtlCallToken) {
				c.remoteCallID = ffrm.SrcCallNumber()
				c.remoteCallKey = newRemoteCallKeyFromFrame(ffrm)
				c.iaxTrunk.lock.Lock()
				c.iaxTrunk.remoteCallMap[c.remoteCallKey] = c
				c.iaxTrunk.lock.Unlock()
				c.log(DebugLogLevel, "Remote call ID set to %d", c.remoteCallID)
			}
		}
		if ffrm.NeedACK() {
			c.sendACK(ffrm)
		}
		if ffrm.IsResponse() && ffrm.DstCallNumber() != 0 {
			select {
			case c.respQueue <- ffrm:
			default:
				c.kill(fmt.Errorf("%w: Response queue full", ErrInternal))
			}
			return
		}
		switch ffrm.FrameType() {
		case FrmIAXCtl:
			switch ffrm.Subclass() {
			case IAXCtlHangup:
				c.processHangup(ffrm)
			case IAXCtlNew:
				c.processIncomingCall(ffrm)
			case IAXCtlRegReq:
				go c.processRegReq(ffrm)
			case IAXCtlLagRqst:
				frame := NewFullFrame(FrmIAXCtl, IAXCtlLagRply)
				frame.SetTimestamp(ffrm.Timestamp())
				go c.sendFullFrame(frame)
			case IAXCtlPing, IAXCtlPoke:
				frame := NewFullFrame(FrmIAXCtl, IAXCtlPong)
				go func() {
					c.sendFullFrame(frame)
					if c.State() == IdleCallState {
						c.kill(fmt.Errorf("%w, unneeded temp call", ErrLocalHangup))
					}
				}()
			default:
				c.log(DebugLogLevel, "Unhandled IAXCtl subclass %s", SubclassToString(FrmIAXCtl, ffrm.Subclass()))
			}
		case FrmControl:
			switch ffrm.Subclass() {
			case CtlAnswer:
				if c.State() == AcceptCallState {
					c.setState(OnlineCallState)
				} else {
					if c.State() == OnlineCallState && ffrm.Retransmit() {
						return // Ignore retransmitted answer
					}
					c.kill(ErrInvalidState)
				}
			default:
				c.pushEvent(&CallCtlEvent{
					Subclass: ffrm.Subclass(),
				})
			}
		case FrmDTMFBegin, FrmDTMFEnd:
			c.pushEvent(&DTMFEvent{
				Digit: string(rune(ffrm.Subclass())),
				Start: ffrm.FrameType() == FrmDTMFBegin,
			})
		case FrmVoice:
			codec := CodecFromSubclass(ffrm.Subclass())
			c.lastPeerCodec = codec
			c.pushEvent(&MediaEvent{
				Data:  ffrm.Payload(),
				Codec: codec,
			})
		default:
			c.log(DebugLogLevel, "Unhandled frame type %s", ffrm.FrameType())
		}
	} else {
		c.pushEvent(&MediaEvent{
			Data:  frame.Payload(),
			Codec: c.lastPeerCodec,
		})
	}
}

func (c *Call) makeDialFrame(opts *DialOptions) (*FullFrame, error) {
	frame := NewFullFrame(FrmIAXCtl, IAXCtlNew)
	frame.AddIE(Uint16IE(IEVersion, 2))
	frame.AddIE(StringIE(IECalledNumber, opts.CalledNumber))
	frame.AddIE(Uint8IE(IECallingPres, opts.CallingPres))
	frame.AddIE(Uint8IE(IECallingTON, opts.CallingTON))
	frame.AddIE(Uint16IE(IECallingTNS, opts.CallingTNS))
	frame.AddIE(StringIE(IECallingANI, opts.CallingANI))
	frame.AddIE(Uint16IE(IEADSICPE, opts.ADSICPE))
	if opts.CodecPrefs == nil {
		opts.CodecPrefs = c.peer.CodecPrefs
	}
	if opts.CodecCaps == 0 {
		opts.CodecCaps = c.peer.CodecCaps
	}
	frame.AddIE(StringIE(IECodecPrefs, EncodePreferredCodecs(opts.CodecPrefs)))
	frame.AddIE(Uint32IE(IECapability, uint32(opts.CodecCaps)))
	data := make([]byte, 9)
	binary.BigEndian.PutUint64(data[1:], uint64(opts.CodecCaps))
	frame.AddIE(BytesIE(IECapability2, data))
	frame.AddIE(Uint32IE(IEFormat, uint32(opts.CodecFormat.BitMask())))
	data = make([]byte, 9)
	binary.BigEndian.PutUint64(data[1:], uint64(opts.CodecFormat.BitMask()))
	frame.AddIE(BytesIE(IEFormat2, data))
	frame.AddIE(StringIE(IEUsername, c.peer.User))
	frame.AddIE(Uint32IE(IEDateTime, TimeToIaxTime(time.Now())))
	if opts.DNID != "" {
		frame.AddIE(StringIE(IEDNID, opts.DNID))
	}
	if opts.CallingNumber != "" {
		frame.AddIE(StringIE(IECallingNumber, opts.CallingNumber))
	}
	if opts.Language != "" {
		frame.AddIE(StringIE(IELanguage, opts.Language))
	}
	return frame, nil
}

func (c *Call) processHangup(frame *FullFrame) error {
	cause := frame.FindIE(IECause).AsString()
	code := frame.FindIE(IECauseCode).AsUint8()
	c.hangupCause = cause
	c.hangupCode = code
	var err error
	switch frame.Subclass() {
	case IAXCtlHangup:
		err = ErrRemoteHangup
	case IAXCtlReject:
		err = ErrRemoteReject
	}
	return c.kill(fmt.Errorf("%w: %s (%d)", err, cause, code))
}

func (c *Call) processAccept(frame *FullFrame) error {
	if c.State() != DialingCallState {
		return c.kill(ErrInvalidState)
	}
	ie := frame.FindIE(IEFormat2)
	if ie != nil {
		mask := CodecMask(binary.BigEndian.Uint64(ie.AsBytes()[1:]))
		c.acceptCodec = mask.FirstCodec()
	} else {
		ie := frame.FindIE(IEFormat)
		if ie != nil {
			mask := CodecMask(ie.AsUint32())
			c.acceptCodec = mask.FirstCodec()
		} else {
			return c.kill(fmt.Errorf("%w: %s", ErrMissingIE, "No codec info"))
		}
	}
	c.setState(AcceptCallState)
	return nil
}

func (c *Call) processAuthReq(frame *FullFrame) error {
	if c.State() != DialingCallState {
		return c.kill(ErrInvalidState)
	}
	ie := frame.FindIE(IEChallenge)
	if ie == nil {
		return c.kill(fmt.Errorf("%w: %s", ErrMissingIE, "No challenge"))
	}
	nonce := ie.AsString()
	ie = frame.FindIE(IEUsername)
	if ie == nil {
		return c.kill(fmt.Errorf("%w: %s", ErrMissingIE, "No username"))
	}
	username := ie.AsString()
	if username != c.peer.User {
		return c.kill(fmt.Errorf("%w: %s", ErrAuthFailed, "Invalid username"))
	}
	ie = frame.FindIE(IEAuthMethods)
	if ie == nil {
		return c.kill(fmt.Errorf("%w: %s", ErrMissingIE, "No auth methods"))
	}
	if ie.AsUint16() != 0x0002 {
		return c.kill(fmt.Errorf("%w: %s", ErrAuthFailed, "Unsupported auth method"))
	}
	frame = NewFullFrame(FrmIAXCtl, IAXCtlAuthRep)
	frame.AddIE(StringIE(IEMD5Result, challengeResponse(c.peer.Password, nonce)))
	rfrm, err := c.sendFullFrame(frame)
	if err != nil {
		return c.kill(err)
	}
	if rfrm.FrameType() != FrmIAXCtl {
		return c.kill(ErrUnexpectedFrame)
	}
	switch rfrm.Subclass() {
	case IAXCtlAccept:
		return c.processAccept(rfrm)
	case IAXCtlReject:
		return c.processHangup(rfrm)
	default:
		return c.kill(ErrUnexpectedFrame)
	}
}

func (c *Call) Dial(peerUsr string, opts *DialOptions) error {
	if c.State() != IdleCallState {
		return c.kill(ErrInvalidState)
	}
	c.peer = c.iaxTrunk.Peer(peerUsr)
	if c.peer == nil {
		return ErrPeerNotFound
	}
	c.peerAddr = c.peer.Address()
	if c.peerAddr == nil {
		return ErrPeerUnreachable
	}
	c.outgoing = true
	c.setState(DialingCallState)
	callToken := ""
	for {
		frame, err := c.makeDialFrame(opts)
		if err != nil {
			return c.kill(err)
		}
		if c.peer.EnableCallToken {
			frame.AddIE(StringIE(IECallToken, callToken))
		}
		rfrm, err := c.sendFullFrame(frame)
		if err != nil {
			return c.kill(err)
		}
		if rfrm.FrameType() != FrmIAXCtl {
			return c.kill(ErrUnexpectedFrame)
		}
		switch rfrm.Subclass() {
		case IAXCtlCallToken:
			if !c.peer.EnableCallToken {
				return c.kill(fmt.Errorf("%w: %s", ErrUnexpectedFrame, "Call token not enabled"))
			}
			callToken = rfrm.FindIE(IECallToken).AsString()
			if callToken == "" {
				return c.kill(fmt.Errorf("%w: %s", ErrMissingIE, "No call token"))
			}
			c.iseqno = 0
			c.oseqno = 0
		case IAXCtlAccept:
			return c.processAccept(rfrm)
		case IAXCtlAuthReq:
			return c.processAuthReq(rfrm)
		case IAXCtlReject:
			return c.processHangup(rfrm)
		default:
			return c.kill(ErrUnexpectedFrame)
		}
	}
}

func (c *Call) processRegReq(frame *FullFrame) error {
	challenge := ""
	for {
		ie := frame.FindIE(IEUsername)
		if ie == nil {
			return c.kill(fmt.Errorf("%w: %s", ErrMissingIE, "No username"))
		}
		peer := c.iaxTrunk.Peer(ie.AsString())
		if peer == nil {
			c.Reject("Peer not found", 0)
			return ErrPeerNotFound
		}
		if peer.Host != "" {
			c.Reject("registration not allowed", 0)
			return ErrPeerUnreachable
		}
		refreshInterval := time.Second * 60
		ie = frame.FindIE(IERefresh)
		if ie != nil {
			i := time.Duration(ie.AsUint16())
			if i > 0 {
				refreshInterval = i * time.Second
			}
		}
		if challenge != "" {
			ie = frame.FindIE(IEMD5Result)
			if ie == nil || ie.AsString() != challengeResponse(peer.Password, challenge) {
				frm := NewFullFrame(FrmIAXCtl, IAXCtlRegRej)
				c.sendFullFrame(frm)
				c.log(DebugLogLevel, "Registration auth failed from %s (%s)", peer.User, refreshInterval)
				return c.kill(ErrAuthFailed)
			}
		}
		if peer.Password != "" && challenge == "" {
			c.log(DebugLogLevel, "Sending registration auth")
			var err error
			frame = NewFullFrame(FrmIAXCtl, IAXCtlRegAuth)
			frame.AddIE(StringIE(IEUsername, peer.User))
			frame.AddIE(Uint16IE(IEAuthMethods, 0x0002)) // MD5
			challenge = makeNonce(16)
			frame.AddIE(StringIE(IEChallenge, challenge))
			frame, err = c.sendFullFrame(frame)
			if err != nil {
				return c.kill(err)
			}
			if frame.FrameType() != FrmIAXCtl || frame.Subclass() != IAXCtlRegReq {
				return c.kill(ErrUnexpectedFrame)
			}
			continue
		}

		frame = NewFullFrame(FrmIAXCtl, IAXCtlRegAck)
		frame.AddIE(StringIE(IEUsername, peer.User))
		frame.AddIE(Uint16IE(IERefresh, uint16(refreshInterval.Seconds())))
		frame.AddIE(Uint32IE(IEDateTime, TimeToIaxTime(time.Now())))
		_, err := c.sendFullFrame(frame)
		if err != nil {
			return c.kill(err)
		}
		peer.lastRegInTime = time.Now()
		peer.regInAddr = frame.PeerAddr()
		peer.regInInterval = refreshInterval
		c.log(DebugLogLevel, "Registration success from %s (%s)", peer.User, refreshInterval)
		return c.kill(fmt.Errorf("%w, unneeded temp call", ErrLocalHangup))
	}
}

func (c *Call) processIncomingCall(frame *FullFrame) error {
	if c.State() != IdleCallState {
		return c.kill(ErrInvalidState)
	}
	ie := frame.FindIE(IEUsername)
	if ie == nil {
		return c.kill(fmt.Errorf("%w: %s", ErrMissingIE, "No username"))
	}
	c.peer = c.iaxTrunk.Peer(ie.AsString())
	if c.peer == nil {
		return c.kill(ErrPeerNotFound)
	}
	c.dialOptions = &DialOptions{}

	ie = frame.FindIE(IECallingNumber)
	if ie != nil {
		c.dialOptions.CallingNumber = ie.AsString()
	}
	ie = frame.FindIE(IECalledNumber)
	if ie != nil {
		c.dialOptions.CalledNumber = ie.AsString()
	}
	ie = frame.FindIE(IECapability2)
	if ie != nil {
		c.dialOptions.CodecCaps = CodecMask(binary.BigEndian.Uint64(ie.AsBytes()[1:]))
	} else {
		ie = frame.FindIE(IECapability)
		if ie != nil {
			c.dialOptions.CodecCaps = CodecMask(ie.AsUint32())
		}
	}
	ie = frame.FindIE(IEFormat2)
	if ie != nil {
		mask := CodecMask(binary.BigEndian.Uint64(ie.AsBytes()[1:]))
		c.dialOptions.CodecFormat = mask.FirstCodec()
	} else {
		ie = frame.FindIE(IEFormat)
		if ie != nil {
			mask := CodecMask(ie.AsUint32())
			c.dialOptions.CodecFormat = mask.FirstCodec()
		}
	}
	ie = frame.FindIE(IECodecPrefs)
	if ie != nil {
		c.dialOptions.CodecPrefs = DecodePreferredCodecs(ie.AsString())
	}
	ie = frame.FindIE(IELanguage)
	if ie != nil {
		c.dialOptions.Language = ie.AsString()
	}
	ie = frame.FindIE(IEADSICPE)
	if ie != nil {
		c.dialOptions.ADSICPE = ie.AsUint16()
	}
	ie = frame.FindIE(IECallingTON)
	if ie != nil {
		c.dialOptions.CallingTON = ie.AsUint8()
	}
	ie = frame.FindIE(IECallingTNS)
	if ie != nil {
		c.dialOptions.CallingTNS = ie.AsUint16()
	}
	ie = frame.FindIE(IECallingANI)
	if ie != nil {
		c.dialOptions.CallingANI = ie.AsString()
	}
	ie = frame.FindIE(IEDNID)
	if ie != nil {
		c.dialOptions.DNID = ie.AsString()
	}
	ie = frame.FindIE(IEDateTime)
	if ie != nil {
		c.dialOptions.PeerTime = IaxTimeToTime(ie.AsUint32())
	}
	c.outgoing = false
	c.log(DebugLogLevel, "Incoming call info %+v", c.dialOptions)
	c.setState(IncomingCallState)
	return nil
}

func (c *Call) sendMiniFrame(frame *MiniFrame) { // TODO: make it private
	frame.SetSrcCallNumber(c.localCallID)
	if frame.timestamp == 0 {
		frame.SetTimestamp(uint32(time.Since(c.creationTs).Milliseconds()))
	}
	frame.SetPeerAddr(c.peerAddr)
	c.iaxTrunk.SendFrame(frame)
}

func (c *Call) sendFullFrame(frame *FullFrame) (*FullFrame, error) {
	c.sendLock.Lock()
	defer c.sendLock.Unlock()
	if c.peerAddr == nil {
		c.log(DebugLogLevel, "Peer address not set")
		return nil, ErrPeerUnreachable
	}
	frame.SetPeerAddr(c.peerAddr)
	emptyQueue := false
	for !emptyQueue { // Empty response queue
		select {
		case <-c.respQueue:
		default:
			emptyQueue = true
		}
	}
	if frame.timestamp == 0 {
		frame.SetTimestamp(uint32(time.Since(c.creationTs).Milliseconds()))
	}
	frame.SetSrcCallNumber(c.localCallID)
	frame.SetDstCallNumber(c.remoteCallID)
	frame.SetOSeqNo(c.oseqno)
	c.oseqno++
	frame.SetISeqNo(c.iseqno)

	for retry := 1; retry <= 5; retry++ {
		if c.iaxTrunk.logLevel > DisabledLogLevel {
			if c.iaxTrunk.logLevel <= DebugLogLevel {
				c.log(DebugLogLevel, ">> TX frame, type %s, subclass %s, retransmit %t", frame.FrameType(), SubclassToString(frame.FrameType(), frame.Subclass()), frame.Retransmit())
			}
		}
		c.iaxTrunk.SendFrame(frame)
		if !frame.NeedACK() && !frame.NeedResponse() {
			return nil, nil
		}
		var rFrm *FullFrame
		var err error
		frameTimeout := c.iaxTrunk.options.FrameTimeout
		if frameTimeout == 0 {
			frameTimeout = time.Millisecond * 250
		}
		for {
			rFrm, err = c.waitResponse(frameTimeout)
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) {
					c.log(DebugLogLevel, "Timeout waiting for response, retry %d", retry)
					frame.SetRetransmit(true)
					break
				}
				return nil, err
			}
			if rFrm.FrameType() == FrmIAXCtl && rFrm.Subclass() == IAXCtlAck {
				if !frame.NeedACK() {
					c.log(DebugLogLevel, "Unexpected ACK")
					continue
				}
				if frame.Timestamp() != rFrm.Timestamp() {
					c.log(DebugLogLevel, "Unexpected ACK timestamp")
				}
			}
			break
		}
		if err == nil {
			return rFrm, nil
		}
	}
	return nil, ErrTimeout
}

func (c *Call) sendACK(ackFrm *FullFrame) {
	frame := NewFullFrame(FrmIAXCtl, IAXCtlAck)
	frame.SetSrcCallNumber(c.localCallID)
	frame.SetDstCallNumber(c.remoteCallID)
	frame.SetOSeqNo(c.oseqno)
	frame.SetISeqNo(c.iseqno)
	frame.SetTimestamp(ackFrm.Timestamp())
	frame.SetPeerAddr(c.peerAddr)
	if c.iaxTrunk.logLevel <= DebugLogLevel {
		c.log(DebugLogLevel, ">> TX frame, type %s, subclass %s, retransmit %t", frame.FrameType(), SubclassToString(frame.FrameType(), frame.Subclass()), frame.Retransmit())
	}
	c.iaxTrunk.SendFrame(frame)
}

func (c *Call) waitResponse(timeout time.Duration) (*FullFrame, error) {
	ctx, cancel := context.WithTimeout(c.ctx, timeout)
	defer cancel()
	select {
	case frame := <-c.respQueue:
		return frame, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *Call) IAXTrunk() *IAXTrunk {
	return c.iaxTrunk
}

// WaitEvent waits for a call event to occur.
// The timeout parameter specifies the maximum time to wait for an event.
func (c *Call) WaitEvent(timeout time.Duration) (CallEvent, error) {
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

// destroy destroys the call and releases resources
func (c *Call) destroy() {
	c.iaxTrunk.lock.Lock()
	delete(c.iaxTrunk.localCallMap, c.localCallID)
	delete(c.iaxTrunk.remoteCallMap, c.remoteCallKey)
	c.iaxTrunk.lock.Unlock()
	c.cancel()
}

func (c *Call) pushEvent(evt CallEvent) {
	if evt.Timestamp().IsZero() {
		evt.SetTimestamp(time.Now())
	}
	select {
	case c.evtQueue <- evt:
	default:
		c.log(ErrorLogLevel, "Event queue full")
	}
}

func (c *Call) poke(peerUsr string) (*FullFrame, error) {
	if c.State() != IdleCallState {
		return nil, ErrInvalidState
	}
	defer c.kill(ErrLocalHangup)
	peerAddr, err := c.iaxTrunk.PeerAddress(peerUsr)
	if err != nil {
		return nil, err
	}
	c.peerAddr = peerAddr
	oFrm := NewFullFrame(FrmIAXCtl, IAXCtlPoke)
	rfrm, err := c.sendFullFrame(oFrm)
	if err != nil {
		return nil, err
	}
	if rfrm.FrameType() != FrmIAXCtl || rfrm.Subclass() != IAXCtlPong {
		return nil, ErrUnexpectedFrame
	}
	return rfrm, nil
}

func (c *Call) register(peerUsr string) error {
	if c.State() != IdleCallState {
		return ErrInvalidState
	}
	defer c.kill(ErrLocalHangup)
	peer := c.iaxTrunk.Peer(peerUsr)
	if peer == nil {
		return ErrPeerNotFound
	}
	if peer.RegOutInterval == 0 {
		return ErrPeerUnreachable
	}
	peer.nextRegOutTime = time.Now().Add(peer.RegOutInterval) // Set new default registration time
	peerAddr, err := c.iaxTrunk.PeerAddress(peerUsr)
	if err != nil {
		return err
	}
	c.peerAddr = peerAddr
	md5Resp := ""
	token := ""
	for {
		oFrm := NewFullFrame(FrmIAXCtl, IAXCtlRegReq)
		oFrm.AddIE(StringIE(IEUsername, peer.User))
		oFrm.AddIE(Uint16IE(IERefresh, uint16(peer.RegOutInterval.Seconds())))
		if md5Resp != "" {
			oFrm.AddIE(StringIE(IEMD5Result, md5Resp))
		}
		if peer.EnableCallToken {
			oFrm.AddIE(StringIE(IECallToken, token))
		}
		rFrm, err := c.sendFullFrame(oFrm)
		if err != nil {
			return err
		}
		if rFrm.FrameType() != FrmIAXCtl {
			return ErrUnexpectedFrame
		}
		switch rFrm.Subclass() {
		case IAXCtlCallToken:
			if !peer.EnableCallToken {
				return ErrUnexpectedFrame
			}
			ie := rFrm.FindIE(IECallToken)
			if ie == nil {
				return ErrMissingIE
			}
			token = ie.AsString()
			c.iseqno = 0
			c.oseqno = 0
		case IAXCtlRegAck:
			peer.lastRegOutTime = time.Now()
			ie := rFrm.FindIE(IERefresh)
			if ie != nil {
				peer.nextRegOutTime = time.Now().Add(time.Duration(ie.AsUint16()) * time.Second)
			}
			return nil
		case IAXCtlRegRej:
			return ErrRemoteReject
		case IAXCtlRegAuth:
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
			md5Resp = challengeResponse(peer.Password, challenge)
		}
	}
}

// State returns the call state
func (c *Call) State() CallState {
	return CallState(atomic.LoadInt32((*int32)(&c.state)))
}

// KillErr returns the error that caused the call to hang up.
func (c *Call) KillCauseErr() error {
	return c.killErr
}

// Accept accepts the call.
func (c *Call) Accept(codec Codec) error {
	if c.State() == IncomingCallState {
		if c.peer.Password != "" {
			frame := NewFullFrame(FrmIAXCtl, IAXCtlAuthReq)
			frame.AddIE(StringIE(IEUsername, c.peer.User))
			frame.AddIE(Uint16IE(IEAuthMethods, 0x0002)) // MD5
			nonce := makeNonce(16)
			frame.AddIE(StringIE(IEChallenge, nonce))
			rFrm, err := c.sendFullFrame(frame)
			if err != nil {
				return c.kill(err)
			}
			if rFrm.FindIE(IEMD5Result).AsString() != challengeResponse(c.peer.Password, nonce) {
				c.Reject("Authentication failed", 0)
				return ErrAuthFailed
			}
		}
		frame := NewFullFrame(FrmIAXCtl, IAXCtlAccept)
		frame.AddIE(Uint32IE(IEFormat, uint32(codec.BitMask())))
		buf := make([]byte, 9)
		binary.BigEndian.PutUint64(buf[1:], uint64(codec.BitMask()))
		frame.AddIE(BytesIE(IEFormat2, buf))
		rfrm, err := c.sendFullFrame(frame)
		if err != nil {
			return c.kill(err)
		}
		if rfrm.FrameType() != FrmIAXCtl {
			return c.kill(ErrUnexpectedFrame)
		}
		switch rfrm.Subclass() {
		case IAXCtlReject:
			return c.processHangup(rfrm)
		case IAXCtlAck:
			c.acceptCodec = codec
			c.setState(AcceptCallState)
		default:
			return c.kill(ErrUnexpectedFrame)
		}
	} else {
		return c.kill(ErrInvalidState)
	}
	return nil
}

// Reject rejects the call.
func (c *Call) Reject(cause string, code uint8) error {
	if c.State() == IncomingCallState {
		frame := NewFullFrame(FrmIAXCtl, IAXCtlReject)
		if cause != "" {
			frame.AddIE(StringIE(IECause, cause))
		}
		if code != 0 {
			frame.AddIE(Uint8IE(IECauseCode, code))
		}
		_, err := c.sendFullFrame(frame)
		c.kill(fmt.Errorf("%w: cause:%s code:%d", ErrLocalReject, cause, code))
		if err != nil {
			return err
		}
	} else {
		return ErrInvalidState
	}
	return nil
}

func (c *Call) kill(err error) error {
	if c.killErr == nil {
		c.killErr = err
		c.log(DebugLogLevel, "Killed \"%s\"", err)
	}
	c.setState(HangupCallState)
	return err
}

// Hangup hangs up the call and frees resources.
func (c *Call) Hangup(cause string, code uint8) error {
	if c.State() != HangupCallState {
		if c.State() != IdleCallState {
			frame := NewFullFrame(FrmIAXCtl, IAXCtlHangup)
			if cause != "" {
				frame.AddIE(StringIE(IECause, cause))
			}
			if code != 0 {
				frame.AddIE(Uint8IE(IECauseCode, code))
			}
			c.sendFullFrame(frame)
		}
		c.kill(fmt.Errorf("%w: cause \"%s\", code %d", ErrLocalHangup, cause, code))
	} else {
		return c.kill(ErrInvalidState)
	}
	return nil
}

// SendRinging sends a ringing signal to the peer.
// Call must be incomming and in AcceptCallState.
func (c *Call) SendRinging() error {
	if c.State() == AcceptCallState && !c.outgoing {
		frame := NewFullFrame(FrmControl, CtlRinging)
		_, err := c.sendFullFrame(frame)
		if err != nil {
			return c.kill(err)
		}
	} else {
		return c.kill(ErrInvalidState)
	}
	return nil
}

// SendProceeding sends a proceeding signal to the peer.
// Call must be incomming and in AcceptCallState.
func (c *Call) SendProceeding() error {
	if c.State() == AcceptCallState && !c.outgoing {
		frame := NewFullFrame(FrmControl, CtlProceeding)
		_, err := c.sendFullFrame(frame)
		if err != nil {
			return c.kill(err)
		}
	} else {
		return c.kill(ErrInvalidState)
	}
	return nil
}

// Answer answers the call.
// Call must be incomming and in AcceptCallState.
func (c *Call) Answer() error {
	if c.State() == AcceptCallState && !c.outgoing {
		frame := NewFullFrame(FrmControl, CtlAnswer)
		_, err := c.sendFullFrame(frame)
		if err != nil {
			return c.kill(err)
		}
		c.setState(OnlineCallState)
	} else {
		return c.kill(ErrInvalidState)
	}
	return nil
}

// SendPing sends a ping to the peer.
// returns pong frame or error.
func (c *Call) SendPing() (*FullFrame, error) {
	frame := NewFullFrame(FrmIAXCtl, IAXCtlPing)
	rFrm, err := c.sendFullFrame(frame)
	if err != nil {
		return nil, c.kill(err)
	}
	return rFrm, nil
}

// SendLagRqst sends a lag request to the peer.
// returns the lag time or error.
func (c *Call) SendLagRqst() (time.Duration, error) {
	frame := NewFullFrame(FrmIAXCtl, IAXCtlLagRqst)
	rFrm, err := c.sendFullFrame(frame)
	if err != nil {
		return 0, c.kill(err)
	}
	if rFrm.FrameType() != FrmIAXCtl || rFrm.Subclass() != IAXCtlLagRply {
		return 0, c.kill(ErrUnexpectedFrame)
	}
	sentTs := c.creationTs.Add(time.Millisecond * time.Duration(rFrm.Timestamp()))
	return time.Since(sentTs), nil
}

// SendDTMF sends a DTMF digit to the peer.
// dur and gap are in milliseconds if dur is 0, it defaults to 150ms. If gap is 0, it defaults to 50ms.
// if dur is negative, the digit is sent as a system default duration tone.
// tones are sent sequentially with a gap of gap milliseconds.
// Call state must be in OnlineCallState.
func (c *Call) SendDTMF(digits string, dur, gap int) error {
	if dur == 0 {
		dur = 150
	}
	if gap <= 0 {
		gap = 50
	}
	for _, digit := range digits {
		if dur > 0 {
			frame := NewFullFrame(FrmDTMFBegin, Subclass(digit))
			_, err := c.sendFullFrame(frame)
			if err != nil {
				return c.kill(err)
			}
			time.Sleep(time.Millisecond * time.Duration(dur))
		}
		frame := NewFullFrame(FrmDTMFEnd, Subclass(digit))
		_, err := c.sendFullFrame(frame)
		if err != nil {
			return c.kill(err)
		}
		time.Sleep(time.Millisecond * time.Duration(gap))
	}
	return nil
}

func (c *Call) setState(state CallState) {
	if c.State() == HangupCallState { // Ignore state changes after hangup
		c.log(ErrorLogLevel, "Already hung up")
		return
	}
	if state != c.State() {
		oldState := c.State()
		atomic.StoreInt32((*int32)(&c.state), int32(state))
		c.pushEvent(&CallStateChangeEvent{
			State:     state,
			PrevState: oldState,
		})
		c.log(DebugLogLevel, "State change %v -> %v", oldState, state)
		switch state {
		case AcceptCallState:
			go c.tickerLoop()
			go c.mediaOutputLoop()
		case HangupCallState:
			c.destroy()
		}
	}
}

func (c *Call) log(level LogLevel, format string, args ...interface{}) {
	format = fmt.Sprintf("Call %d: %s", c.localCallID, format)
	c.iaxTrunk.log(level, format, args...)
}
