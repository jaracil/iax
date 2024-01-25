package iax

import (
	"context"
	"errors"
	"time"
)

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
}

func (c *Call) ProcessFrame(frame Frame) {
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
			c.SendACK(ffrm)
		}
		if ffrm.IsResponse() {
			c.respQueue <- ffrm
			return
		}
	} else {
		c.miniFrameQueue <- frame.(*MiniFrame)
	}
}

func (c *Call) SendMiniFrame(frame *MiniFrame) {
	frame.SetSrcCallNumber(c.localCallID)
	frame.SetTimestamp(uint32(time.Since(c.firstFrameTs).Milliseconds()))
	c.client.SendFrame(frame)
}

func (c *Call) SendFullFrame(ctx context.Context, frame *FullFrame) (*FullFrame, error) {
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
		if !frame.NeedResponse() {
			return nil, nil
		}
		childCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		rFrm, err := c.WaitResponse(childCtx)
		cancel()
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				c.client.Log(DebugLogLevel, "Call %d: Timeout waiting for response, retry %d", c.localCallID, retry)
				frame.SetRetransmit(true)
				continue
			}
			return nil, err
		}
		return rFrm, nil
	}
	return nil, ErrTimeout
}

func (c *Call) SendACK(ackFrm *FullFrame) {
	frame := NewFullFrame(FrmIAXCtl, IAXCtlAck)
	frame.SetSrcCallNumber(c.localCallID)
	frame.SetDstCallNumber(c.remoteCallID)
	frame.SetOSeqNo(c.oseqno)
	frame.SetISeqNo(c.iseqno)
	frame.SetTimestamp(ackFrm.Timestamp())
	c.client.SendFrame(frame)
}

func (c *Call) WaitResponse(ctx context.Context) (*FullFrame, error) {
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
