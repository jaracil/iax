package iax

import "time"

type Call struct {
	client       *Client
	frameQueue   chan Frame
	firstFrameTs time.Time
	localCallID  uint16
	remoteCallID uint16
	oseqno       uint8
	iseqno       uint8
}

func (c *Call) ProcessFrame(frame Frame) {
	c.frameQueue <- frame
}

func (c *Call) SendFrame(frame Frame) {
	frame.SetSrcCallNumber(c.localCallID)
	frame.SetTimestamp(uint32(time.Since(c.firstFrameTs).Milliseconds()))
	if frame.IsFullFrame() {
		frame.SetDstCallNumber(c.remoteCallID)
		frame.SetOSeqNo(c.oseqno)
		c.oseqno++
		frame.SetISeqNo(c.iseqno)
	}
	c.client.SendFrame(frame)
}
