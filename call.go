package iax

import (
	"context"
	"errors"
	"sync"
	"time"
)

type CallState int

const (
	UninitializedCallState CallState = iota
	IdleCallState
	DialingCallState
	RingingCallState
	RingingBackCallState
	OnlineCallState
	HangupCallState
	DestroyedCallState
)

func (cs CallState) String() string {
	switch cs {
	case UninitializedCallState:
		return "Uninitialized"
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
	case DestroyedCallState:
		return "Destroyed"
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

type CallEvent interface {
	Kind() CallEventKind
}

type CallEventHandler func(event CallEvent)

type CallOptions struct {
	EvtQueueSize int
	EventHandler CallEventHandler
}

type Call struct {
	client       *Client
	options      *CallOptions
	respQueue    chan *FullFrame
	evtQueue     chan CallEvent
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
	// specific to media
	outgoing bool
}

func NewCall(c *Client, opts *CallOptions) *Call {
	if opts == nil {
		opts = &CallOptions{
			EvtQueueSize: 20,
		}
	}
	if opts.EvtQueueSize == 0 {
		opts.EvtQueueSize = 20
	}

	c.lock.Lock()
	defer c.lock.Unlock()
	for {
		c.callIDCount++
		if c.callIDCount > 0x7fff {
			c.callIDCount = 1
		}
		if _, ok := c.localCallMap[c.callIDCount]; !ok {
			call := &Call{
				client:       c,
				options:      opts,
				respQueue:    make(chan *FullFrame, 3),
				evtQueue:     make(chan CallEvent, opts.EvtQueueSize),
				eventHandler: opts.EventHandler,
				localCallID:  c.callIDCount,
				isFirstFrame: true,
			}
			call.setState(IdleCallState)
			c.localCallMap[c.callIDCount] = call
			return call
		}
	}
}

// Options returns a copy of the call options
func (c *Call) Options() *CallOptions {
	r := *c.options
	return &r
}

// SetOptions sets the call options
func (c *Call) SetOptions(opts *CallOptions) {
	c.options = opts
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
// Handler can be set in CallOptions when creating a new call.
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
	} else {
		// Enqueue media event

	}
}

func (c *Call) SendMiniFrame(frame *MiniFrame) { // TODO: make it private
	frame.SetSrcCallNumber(c.localCallID)
	frame.SetTimestamp(uint32(time.Since(c.firstFrameTs).Milliseconds()))
	c.client.SendFrame(frame)
}

func (c *Call) sendFullFrame(ctx context.Context, frame *FullFrame) (*FullFrame, error) {
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
			childCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
			rFrm, err = c.waitResponse(childCtx)
			cancel()
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

func (c *Call) waitResponse(ctx context.Context) (*FullFrame, error) {
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
	c.setState(DestroyedCallState)
	close(c.respQueue)
	close(c.evtQueue)
}

// State returns the call state
func (c *Call) State() CallState {
	c.stateLock.Lock()
	defer c.stateLock.Unlock()
	return c.state
}

func (c *Call) setState(state CallState) {
	c.stateLock.Lock()
	defer c.stateLock.Unlock()
	if state != c.state {
		oldState := c.state
		c.state = state
		select {
		case c.evtQueue <- &StateChangeEvent{
			State:     state,
			PrevState: oldState,
			TimeStamp: time.Now(),
		}:
		default:
			c.client.Log(ErrorLogLevel, "Call %d: Event queue full", c.localCallID)
		}
	}
}
