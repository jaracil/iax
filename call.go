package iax

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

type CallState int

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

type PeerInfo struct {
	CalledNumber  string
	CallingNumber string
	HangupCause   string
	HangupCode    uint8
	HungupByPeer  bool
	CodecCaps     CodecMask
	CodecPrefs    []Codec
	CodecFormat   Codec
	CodecInUse    Codec
	Language      string
	ADSICPE       uint16
	CallingTON    uint8
	CallingTNS    uint16
	CallingANI    string
	PeerTime      time.Time
}

type Call struct {
	client       *Client
	respQueue    chan *FullFrame
	evtQueue     chan CallEvent
	frmQueue     chan Frame
	mediaQueue   chan *MediaEvent
	ctx          context.Context
	cancel       context.CancelFunc
	localCallID  uint16
	remoteCallID uint16
	oseqno       uint8
	iseqno       uint8
	creationTs   time.Time
	sendLock     sync.Mutex
	raceLock     sync.Mutex
	state        CallState
	peer         PeerInfo
	acceptCodec  Codec
	outgoing     bool
	mediaPlaying bool
	mediaStop    bool
	killErr      error
}

func NewCall(c *Client) *Call {
	evtQueueSize := c.options.CallEvtQueueSize
	if evtQueueSize == 0 {
		evtQueueSize = 20
	}
	frmQueueSize := c.options.CallFrmQueueSize
	if frmQueueSize == 0 {
		frmQueueSize = 20
	}

	ctx, cancel := context.WithCancel(c.ctx)
	call := &Call{
		client:     c,
		ctx:        ctx,
		cancel:     cancel,
		respQueue:  make(chan *FullFrame, 3),
		evtQueue:   make(chan CallEvent, evtQueueSize),
		frmQueue:   make(chan Frame, frmQueueSize),
		mediaQueue: make(chan *MediaEvent),
		creationTs: time.Now(),
	}

	c.lock.Lock()
	for {
		c.callIDCount++
		if c.callIDCount > 0x7fff {
			c.callIDCount = 1
		}
		if _, ok := c.localCallMap[c.callIDCount]; !ok {
			break
		}
	}
	call.localCallID = c.callIDCount
	c.localCallMap[c.callIDCount] = call
	c.lock.Unlock()

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
		if needFullFrame {
			needFullFrame = false
			ffrm := NewFullFrame(FrmVoice, ev.Codec.Subclass())
			ffrm.SetPayload(ev.Data)
			_, err := c.sendFullFrame(ffrm)
			if err != nil {
				c.log(ErrorLogLevel, "Error sending voice full-frame: %s", err)
				c.kill(err)
				return
			}
		} else {
			mfrm := NewMiniFrame(ev.Data)
			c.sendMiniFrame(mfrm)
		}

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
	buf := make([]byte, frameSize)
	nextDeadline := time.Now().Add(time.Millisecond * 20)
	for !c.mediaStop && c.State() != HangupCallState {
		if time.Now().Before(nextDeadline) {
			time.Sleep(time.Until(nextDeadline))
		}
		nextDeadline = nextDeadline.Add(time.Millisecond * 20)
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
		return nil // Drop sync source events when playing media
	}
	select {
	case c.mediaQueue <- ev: // Block when async source
	case <-c.ctx.Done():
		return c.ctx.Err()
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
	if frame.IsFullFrame() {
		ffrm := frame.(*FullFrame)
		if c.client.logLevel > DisabledLogLevel {
			if c.client.logLevel <= DebugLogLevel {
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
			c.remoteCallID = ffrm.SrcCallNumber()
			c.client.lock.Lock()
			c.client.remoteCallMap[c.remoteCallID] = c
			c.client.lock.Unlock()
			c.log(DebugLogLevel, "Remote call ID set to %d", c.remoteCallID)
		}
		if ffrm.NeedACK() {
			c.sendACK(ffrm)
		}
		if ffrm.IsResponse() {
			c.respQueue <- ffrm
			return
		}
		switch ffrm.FrameType() {
		case FrmIAXCtl:
			switch ffrm.Subclass() {
			case IAXCtlHangup:
				ie := ffrm.FindIE(IECause)
				if ie != nil {
					c.peer.HangupCause = ie.AsString()
				}
				ie = ffrm.FindIE(IECauseCode)
				if ie != nil {
					c.peer.HangupCode = ie.AsUint8()
				}
				c.peer.HungupByPeer = true
				c.kill(ErrRemoteHangup)
			case IAXCtlNew:
				if c.State() == IdleCallState {
					c.processIncomingCall(ffrm)
				} else if c.State() == IncomingCallState && ffrm.Retransmit() {
					c.log(DebugLogLevel, "Ignoring retransmitted NEW frame")
				} else {
					c.log(DebugLogLevel, "Unexpected NEW frame")
				}
			case IAXCtlLagRqst:
				frame := NewFullFrame(FrmIAXCtl, IAXCtlLagRply)
				frame.SetTimestamp(ffrm.Timestamp())
				go c.sendFullFrame(frame)
			case IAXCtlPing, IAXCtlPoke:
				frame := NewFullFrame(FrmIAXCtl, IAXCtlPong)
				go c.sendFullFrame(frame)
			}
		case FrmDTMFBegin, FrmDTMFEnd:
			c.pushEvent(&DTMFEvent{
				Digit: string(rune(ffrm.Subclass())),
				Start: ffrm.FrameType() == FrmDTMFBegin,
			})
		case FrmVoice:
			codec := CodecFromSubclass(ffrm.Subclass())
			c.peer.CodecInUse = codec
			c.pushEvent(&MediaEvent{
				Data:  ffrm.Payload(),
				Codec: codec,
			})
			// fmt.Printf("RX Full Voice Timestamp: %d\n", frame.Timestamp())
		}
	} else {
		c.pushEvent(&MediaEvent{
			Data:  frame.Payload(),
			Codec: c.peer.CodecInUse,
		})
		// fmt.Printf("RX Miniframe Timestamp: %d\n", frame.Timestamp())
	}
}

func (c *Call) processIncomingCall(frame *FullFrame) {
	ie := frame.FindIE(IECallingNumber)
	if ie != nil {
		c.peer.CallingNumber = ie.AsString()
	}
	ie = frame.FindIE(IECalledNumber)
	if ie != nil {
		c.peer.CalledNumber = ie.AsString()
	}
	ie = frame.FindIE(IECapability2)
	if ie != nil {
		c.peer.CodecCaps = CodecMask(binary.BigEndian.Uint64(ie.AsBytes()[1:]))
	} else {
		ie = frame.FindIE(IECapability)
		if ie != nil {
			c.peer.CodecCaps = CodecMask(ie.AsUint32())
		}
	}
	ie = frame.FindIE(IEFormat2)
	if ie != nil {
		mask := CodecMask(binary.BigEndian.Uint64(ie.AsBytes()[1:]))
		c.peer.CodecFormat = mask.FirstCodec()
	} else {
		ie = frame.FindIE(IEFormat)
		if ie != nil {
			mask := CodecMask(ie.AsUint32())
			c.peer.CodecFormat = mask.FirstCodec()
		}
	}
	ie = frame.FindIE(IECodecPrefs)
	if ie != nil {
		c.peer.CodecPrefs = DecodePreferredCodecs(ie.AsString())
	}
	ie = frame.FindIE(IELanguage)
	if ie != nil {
		c.peer.Language = ie.AsString()
	}
	ie = frame.FindIE(IEADSICPE)
	if ie != nil {
		c.peer.ADSICPE = ie.AsUint16()
	}
	ie = frame.FindIE(IECallingTON)
	if ie != nil {
		c.peer.CallingTON = ie.AsUint8()
	}
	ie = frame.FindIE(IECallingTNS)
	if ie != nil {
		c.peer.CallingTNS = ie.AsUint16()
	}
	ie = frame.FindIE(IECallingANI)
	if ie != nil {
		c.peer.CallingANI = ie.AsString()
	}
	ie = frame.FindIE(IEDateTime)
	if ie != nil {
		c.peer.PeerTime = IaxTimeToTime(ie.AsUint32())
	}
	c.outgoing = false
	c.log(DebugLogLevel, "Peer info %+v", c.peer)
	c.setState(IncomingCallState)
}

func (c *Call) sendMiniFrame(frame *MiniFrame) { // TODO: make it private
	frame.SetSrcCallNumber(c.localCallID)
	if frame.timestamp == 0 {
		frame.SetTimestamp(uint32(time.Since(c.creationTs).Milliseconds()))
	}
	c.client.SendFrame(frame)
	// fmt.Printf("  TX Miniframe Timestamp: %d\n", frame.Timestamp())
}

func (c *Call) sendFullFrame(frame *FullFrame) (*FullFrame, error) {
	c.sendLock.Lock()
	defer c.sendLock.Unlock()
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
		if c.client.logLevel > DisabledLogLevel {
			if c.client.logLevel <= DebugLogLevel && frame.IsFullFrame() {
				c.log(DebugLogLevel, ">> TX frame, type %s, subclass %s, retransmit %t", frame.FrameType(), SubclassToString(frame.FrameType(), frame.Subclass()), frame.Retransmit())
			}
		}
		c.client.SendFrame(frame)
		var rFrm *FullFrame
		var err error
		frameTimeout := c.client.options.FrameTimeout
		if frameTimeout == 0 {
			frameTimeout = time.Millisecond * 100
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
	c.client.SendFrame(frame)
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

func (c *Call) Client() *Client {
	return c.client
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
	c.client.lock.Lock()
	delete(c.client.localCallMap, c.localCallID)
	delete(c.client.remoteCallMap, c.remoteCallID)
	c.client.lock.Unlock()
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

// State returns the call state
func (c *Call) State() CallState {
	c.raceLock.Lock()
	s := c.state
	c.raceLock.Unlock()
	return s
}

// KillErr returns the error that caused the call to hang up.
func (c *Call) KillErrCause() error {
	return c.killErr
}

// Accept accepts the call.
func (c *Call) Accept(codec Codec, auth bool) error {
	if c.State() == IncomingCallState {
		if auth {
			frame := NewFullFrame(FrmIAXCtl, IAXCtlAuthReq)
			frame.AddIE(StringIE(IEUsername, c.client.options.Username))
			frame.AddIE(Uint16IE(IEAuthMethods, 0x0002)) // MD5
			nonce := makeNonce(16)
			frame.AddIE(StringIE(IEChallenge, nonce))
			rFrm, err := c.sendFullFrame(frame)
			if err != nil {
				return c.kill(err)
			}
			if rFrm.FindIE(IEMD5Result).AsString() != challengeResponse(c.client.options.Password, nonce) {
				return c.kill(ErrAuthFailed)
			}
		}

		frame := NewFullFrame(FrmIAXCtl, IAXCtlAccept)
		frame.AddIE(Uint32IE(IEFormat, uint32(codec.BitMask())))
		buf := make([]byte, 9)
		binary.BigEndian.PutUint64(buf[1:], uint64(codec.BitMask()))
		frame.AddIE(BytesIE(IEFormat2, buf))
		rFrm, err := c.sendFullFrame(frame)
		if err != nil {
			return c.kill(err)
		}
		if rFrm.FrameType() != FrmIAXCtl || rFrm.Subclass() != IAXCtlAck {
			if rFrm.FrameType() == FrmIAXCtl && rFrm.Subclass() == IAXCtlReject {
				cause := rFrm.FindIE(IECause).AsString()
				code := rFrm.FindIE(IECauseCode).AsUint8()
				return c.kill(fmt.Errorf("%w: %s (%d)", ErrRejected, cause, code))
			}
			return c.kill(ErrUnexpectedFrameType)
		}
		c.acceptCodec = codec
		c.setState(AcceptCallState)
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
		c.kill(ErrRejected)
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
		c.log(DebugLogLevel, "Killed, cause \"%s\"", err)
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
		return 0, c.kill(ErrUnexpectedFrameType)
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
	c.raceLock.Lock()
	defer c.raceLock.Unlock()
	if c.state == HangupCallState { // Ignore state changes after hangup
		c.log(ErrorLogLevel, "Already hung up")
		return
	}
	if state != c.state {
		oldState := c.state
		c.state = state
		c.pushEvent(&CallStateChangeEvent{
			State:     state,
			PrevState: oldState,
		})
		c.log(DebugLogLevel, "State change %v -> %v", oldState, state)
		switch c.state {
		case AcceptCallState:
			go c.mediaOutputLoop()
		case HangupCallState:
			c.destroy()
		}
	}
}

func (c *Call) log(level LogLevel, format string, args ...interface{}) {
	format = fmt.Sprintf("Call %d: %s", c.localCallID, format)
	c.client.log(level, format, args...)
}
