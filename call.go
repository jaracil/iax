package iax

import (
	"context"
	"time"
)

type Call struct {
	client       *Client
	frameQueue   chan Frame
	localCallID  uint16
	remoteCallID uint16
	oseqno       uint8
	iseqno       uint8
	firstFrameTs time.Time
	firstFrame   bool
}

func (c *Call) ProcessFrame(frame Frame) {
	if frame.IsFullFrame() {
		if frame.OSeqNo() != c.iseqno {
			c.client.Log(DebugLogLevel, "Call %d: Out of order frame received. Expected %d, got %d", c.localCallID, c.iseqno, frame.OSeqNo())
			return
		}
		c.iseqno = frame.OSeqNo()

		if c.remoteCallID == 0 && frame.SrcCallNumber() != 0 {
			c.remoteCallID = frame.SrcCallNumber()
			c.client.lock.Lock()
			c.client.remoteCallMap[c.remoteCallID] = c
			c.client.lock.Unlock()
		}
	}
	c.frameQueue <- frame
}

func (c *Call) SendFrame(frame Frame) {
	frame.SetSrcCallNumber(c.localCallID)
	if c.firstFrame {
		c.firstFrameTs = time.Now()
		c.firstFrame = false
		frame.SetTimestamp(0)
	} else {
		frame.SetTimestamp(uint32(time.Since(c.firstFrameTs).Milliseconds()))
	}
	if frame.IsFullFrame() {
		frame.SetDstCallNumber(c.remoteCallID)
		frame.SetOSeqNo(c.oseqno)
		c.oseqno++
		frame.SetISeqNo(c.iseqno)
	}
	c.client.SendFrame(frame)
}

func (c *Call) WaitForFrame(ctx context.Context) (Frame, error) {
	select {
	case frame := <-c.frameQueue:
		return frame, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
