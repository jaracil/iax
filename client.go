package iax

import (
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

type ClientState int

const (
	Disconnected ClientState = iota
	Disconnecting
	Connected
	Connecting
)

type ClientOptions struct {
	Host              string
	Port              int
	Username          string
	Password          string
	KeepAliveInterval time.Duration
}

type Client struct {
	options       *ClientOptions
	conn          *net.UDPConn
	sendQueue     chan Frame
	lock          sync.RWMutex
	state         ClientState
	callIDCount   uint16
	localCallMap  map[uint16]*Call
	remoteCallMap map[uint16]*Call
}

// NewCall returns new Call
func (c *Client) NewCall() *Call {
	c.lock.Lock()
	defer c.lock.Unlock()
	for {
		c.callIDCount++
		if c.callIDCount > 0x7fff {
			c.callIDCount = 1
		}
		if _, ok := c.localCallMap[c.callIDCount]; !ok {
			call := &Call{client: c, frameQueue: make(chan Frame, 10), localCallID: c.callIDCount}
			c.localCallMap[c.callIDCount] = call
			return call
		}
	}
}

// routeFrame routes a frame to the appropriate call
func (c *Client) routeFrame(frame Frame) {
	c.lock.RLock()
	call, ok := c.remoteCallMap[frame.SrcCallNumber()]
	c.lock.RUnlock()
	if ok {
		call.ProcessFrame(frame)
		return
	}
	c.lock.RLock()
	call, ok = c.localCallMap[frame.DstCallNumber()]
	c.lock.RUnlock()
	if ok {
		call.ProcessFrame(frame)
		return
	}
	// Check for new incoming call
}

// sender sends frames to the server
func (c *Client) sender() {
	for c.state != Disconnected {
		frm, ok := <-c.sendQueue
		if !ok {
			break
		}
		data := frm.Encode()
		n, err := c.conn.Write(data)
		if err != nil || n != len(data) {
			break
		}
	}
}

// receiver receives frames from the server
func (c *Client) receiver() {
	for c.state != Disconnected {
		data := make([]byte, FrameMaxSize)
		_, err := c.conn.Read(data)
		if err != nil {
			break
		}

		frm, err := DecodeFrame(data)
		if err != nil {
			// Log error
			continue
		}
		c.routeFrame(frm)
	}

}

// NewClient creates a new IAX2 client
func NewClient(options *ClientOptions) *Client {
	return &Client{
		options: options,
		state:   Disconnected,
	}
}

// Connect connects to the server
func (c *Client) Connect() error {
	c.state = Connecting
	c.sendQueue = make(chan Frame, 100)
	c.localCallMap = make(map[uint16]*Call)
	c.remoteCallMap = make(map[uint16]*Call)

	defer func() {
		if c.state == Connecting {
			c.Disconnect()
		}
	}()

	if c.options.Port == 0 {
		c.options.Port = 4569 // Default IAX port
	}

	if strings.Contains(c.options.Host, ":") {
		c.options.Host = c.options.Host + ":" + strconv.Itoa(c.options.Port)
	}

	addr, err := net.ResolveUDPAddr("udp", c.options.Host)
	if err != nil {
		return err
	}

	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		return err
	}

	c.conn = conn
	c.state = Connected

	go c.sender()
	go c.receiver()

	return nil
}

// Disconnect disconnects from the server
func (c *Client) Disconnect() {
	if c.state == Connected || c.state == Connecting {
		c.state = Disconnecting
		// TODO: send unregistration
		c.state = Disconnected

		if c.sendQueue != nil {
			close(c.sendQueue)
			c.sendQueue = nil
		}

		if c.conn != nil {
			c.conn.Close()
			c.conn = nil
		}

	}
}

// SendFrame sends a frame to the server
func (c *Client) SendFrame(frame Frame) {
	if c.state != Disconnected {
		c.sendQueue <- frame
	}
}
