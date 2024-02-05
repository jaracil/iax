package iax

import (
	"context"
	"errors"
	"sync"
	"time"
)

type CallState int

const (
	IdleCallState CallState = iota
	DialingCallState
	RingingCallState
	RingingBackCallState
	OnlineCallState
	HangupCallState
)

func (cs CallState) String() string {
	switch cs {
	case IdleCallState:
		return "Idle"
	case DialingCallState:
		return "Dialing"
	case RingingCallState:
		return "Ringing"
	case RingingBackCallState:
		return "RingingBack"
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
)

func (et CallEventKind) String() string {
	switch et {
	case StateChangeEventKind:
		return "StateChange"
	case MediaEventKind:
		return "Media"
	case DTMFEventKind:
		return "DTMF"
	default:
		return "Unknown"
	}
}

type StateChangeEvent struct {
	State     CallState
	PrevState CallState
	TimeStamp time.Time
}

func (e *StateChangeEvent) Kind() CallEventKind {
	return StateChangeEventKind
}

type MediaEvent struct {
	Data      []byte
	Codec     uint64
	TimeStamp time.Time
}

func (e *MediaEvent) Kind() CallEventKind {
	return MediaEventKind
}

type DTMFEvent struct {
	Digit     uint8
	TimeStamp time.Time
}

func (e *DTMFEvent) Kind() CallEventKind {
	return DTMFEventKind
}

type CallEvent interface {
	Kind() CallEventKind
}

type CallEventHandler func(event CallEvent)
type Call struct {
	client       *Client
	respQueue    chan *FullFrame
	evtQueue     chan CallEvent
	ctx          context.Context
	cancel       context.CancelFunc
	localCallID  uint16
	remoteCallID uint16
	oseqno       uint8
	iseqno       uint8
	firstFrameTs time.Time
	isFirstFrame bool
	sendLock     sync.Mutex
	state        CallState
	stateLock    sync.Mutex
	eventHandler CallEventHandler
	hangupCause  string
	hangupCode   uint8
	hangupRemote bool
	// specific to media
	outgoing bool
}

func NewCall(c *Client) *Call {
	evtQueueSize := c.options.CallEvtQueueSize
	if evtQueueSize == 0 {
		evtQueueSize = 20
	}

	c.lock.Lock()
	defer c.lock.Unlock()
	for {
		c.callIDCount++
		if c.callIDCount > 0x7fff {
			c.callIDCount = 1
		}
		ctx, cancel := context.WithCancel(context.Background())
		if _, ok := c.localCallMap[c.callIDCount]; !ok {
			call := &Call{
				client:       c,
				ctx:          ctx,
				cancel:       cancel,
				respQueue:    make(chan *FullFrame, 3),
				evtQueue:     make(chan CallEvent, evtQueueSize),
				localCallID:  c.callIDCount,
				isFirstFrame: true,
			}
			call.setState(IdleCallState)
			c.localCallMap[c.callIDCount] = call
			return call
		}
	}
}

func (c *Call) eventLoop() {
	for {
		evt, ok := <-c.evtQueue
		if !ok {
			return
		}
		if c.eventHandler != nil {
			c.eventHandler(evt)
		}
	}
}

// SetEventHandler sets the event handler for the call.
// Only one handler can be set at a time.
func (c *Call) SetEventHandler(handler CallEventHandler) {
	if handler == nil {
		c.eventHandler = handler
		go c.eventLoop()
	}
}

// IsOutgoing returns true if the call was initiated by us.
func (c *Call) IsOutgoing() bool {
	return c.outgoing
}

func (c *Call) processFrame(frame Frame) {
	if c.State() == HangupCallState {
		return
	}
	if frame.IsFullFrame() {
		ffrm := frame.(*FullFrame)
		if ffrm.OSeqNo() != c.iseqno {
			c.client.Log(DebugLogLevel, "Call %d: Out of order frame received. Expected %d, got %d", c.localCallID, c.iseqno, ffrm.OSeqNo())
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
			c.client.Log(DebugLogLevel, "Call %d: Remote call ID set to %d", c.localCallID, c.remoteCallID)
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
					c.hangupCause = ie.AsString()
				}
				ie = ffrm.FindIE(IECauseCode)
				if ie != nil {
					c.hangupCode = ie.AsUint8()
				}
				c.hangupRemote = true
				c.setState(HangupCallState)
				c.Destroy()
			case IAXCtlNew:
				if c.State() == IdleCallState {
					c.setState(RingingCallState)

				} else if c.State() == RingingCallState && ffrm.Retransmit() {
					c.client.Log(DebugLogLevel, "Call %d: Ignoring retransmitted new", c.localCallID)
				} else {
					c.client.Log(DebugLogLevel, "Call %d: Unexpected new", c.localCallID)
				}
			}
		case FrmDTMF:
			c.pushEvent(&DTMFEvent{
				Digit:     uint8(ffrm.Subclass()),
				TimeStamp: time.Now(),
			})
		}
	} else {
		// Enqueue miniframes media event
		return

	}
}

func (c *Call) SendMiniFrame(frame *MiniFrame) { // TODO: make it private
	frame.SetSrcCallNumber(c.localCallID)
	frame.SetTimestamp(uint32(time.Since(c.firstFrameTs).Milliseconds()))
	c.client.SendFrame(frame)
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
	if c.isFirstFrame {
		c.firstFrameTs = time.Now()
		c.isFirstFrame = false
		frame.SetTimestamp(0)
	} else {
		frame.SetTimestamp(uint32(time.Since(c.firstFrameTs).Milliseconds()))
	}
	frame.SetSrcCallNumber(c.localCallID)
	frame.SetDstCallNumber(c.remoteCallID)
	frame.SetOSeqNo(c.oseqno)
	c.oseqno++
	frame.SetISeqNo(c.iseqno)

	for retry := 1; retry <= 2; retry++ {
		c.client.SendFrame(frame)
		var rFrm *FullFrame
		var err error
		for {
			rFrm, err = c.waitResponse(time.Second)
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) {
					c.client.Log(DebugLogLevel, "Call %d: Timeout waiting for response, retry %d", c.localCallID, retry)
					frame.SetRetransmit(true)
					break
				}
				return nil, err
			}
			if rFrm.Subclass() == IAXCtlAck && frame.NeedResponse() {
				c.client.Log(DebugLogLevel, "Call %d: Unexpected ACK", c.localCallID)
				continue
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

// Destroy destroys the call and releases resources
func (c *Call) Destroy() {
	c.client.lock.Lock()
	delete(c.client.localCallMap, c.localCallID)
	delete(c.client.remoteCallMap, c.remoteCallID)
	c.client.lock.Unlock()
	close(c.respQueue)
	close(c.evtQueue)
	c.cancel()
}

func (c *Call) pushEvent(evt CallEvent) {
	select {
	case c.evtQueue <- evt:
	default:
		c.client.Log(ErrorLogLevel, "Call %d: Event queue full", c.localCallID)
	}
}

// State returns the call state
func (c *Call) State() CallState {
	c.stateLock.Lock()
	defer c.stateLock.Unlock()
	return c.state
}

// Hangup hangs up the call.
func (c *Call) Hangup(cause string, code uint8) error {
	if c.State() != HangupCallState {
		frame := NewFullFrame(FrmIAXCtl, IAXCtlHangup)
		if cause != "" {
			frame.AddIE(StringIE(IECause, cause))
		}
		if code != 0 {
			frame.AddIE(Uint8IE(IECauseCode, code))
		}
		_, err := c.sendFullFrame(frame)
		c.setState(HangupCallState)
		if err != nil {
			return err
		}
	} else {
		return ErrInvalidState
	}
	c.Destroy()
	return nil
}

func (c *Call) SendDTMF(digit uint8) error {
	if c.State() == OnlineCallState {
		frame := NewFullFrame(FrmDTMF, Subclass(digit))
		_, err := c.sendFullFrame(frame)
		if err != nil {
			return err
		}
	} else {
		return ErrInvalidState
	}
	return nil
}

func (c *Call) setState(state CallState) {
	c.stateLock.Lock()
	defer c.stateLock.Unlock()
	if state != c.state {
		oldState := c.state
		c.state = state
		c.pushEvent(&StateChangeEvent{
			State:     state,
			PrevState: oldState,
			TimeStamp: time.Now(),
		})
	}
}
