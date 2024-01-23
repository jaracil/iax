package iax

import (
	"context"
	"errors"
	"time"
)

type Call struct {
	client         *Client
	fullFrameQueue chan *FullFrame
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
		if frame.OSeqNo() != c.iseqno {
			c.client.Log(DebugLogLevel, "Call %d: Out of order frame received. Expected %d, got %d", c.localCallID, c.iseqno, frame.OSeqNo())
			return
		}
		c.iseqno++

		if c.remoteCallID == 0 && frame.SrcCallNumber() != 0 {
			c.remoteCallID = frame.SrcCallNumber()
			c.client.lock.Lock()
			c.client.remoteCallMap[c.remoteCallID] = c
			c.client.lock.Unlock()
			c.client.Log(DebugLogLevel, "Call %d: Remote call ID set to %d", c.localCallID, c.remoteCallID)
		}
		c.fullFrameQueue <- frame.(*FullFrame)
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
		childCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		iFrm, err := c.WaitForFrame(childCtx)
		cancel()
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				c.client.Log(DebugLogLevel, "Call %d: Timeout waiting for response, retry %d", c.localCallID, retry)
				frame.SetRetransmit(true)
				continue
			}
			return nil, err
		}
		return iFrm, nil
	}
	return nil, ErrTimeout
}

func (c *Call) WaitForFrame(ctx context.Context) (*FullFrame, error) {
	select {
	case frame := <-c.fullFrameQueue:
		return frame, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
