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

type Call struct {
	client         *Client
	respQueue      chan *FullFrame
	miniFrameQueue chan *MiniFrame
	localCallID    uint16
	remoteCallID   uint16
	oseqno         uint8
	iseqno         uint8
	firstFrameTs   time.Time
	isFirstFrame   bool
	sendLock       sync.Mutex
	state          CallState
	stateLock      sync.Mutex
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
		c.miniFrameQueue <- frame.(*MiniFrame)
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

	for retry := 1; retry <= 3; retry++ {
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

func (c *Call) Destroy() {
	c.client.lock.Lock()
	delete(c.client.localCallMap, c.localCallID)
	delete(c.client.remoteCallMap, c.remoteCallID)
	c.client.lock.Unlock()
	close(c.respQueue)
	close(c.miniFrameQueue)
}

// State returns the call state
func (c *Call) State() CallState {
	c.stateLock.Lock()
	defer c.stateLock.Unlock()
	return c.state
}

// SetState sets the call state
func (c *Call) SetState(state CallState) {
	c.stateLock.Lock()
	defer c.stateLock.Unlock()
	c.state = state
}
