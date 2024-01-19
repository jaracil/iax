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

type callKey struct {
	isLocal bool
	callID  uint16
}

type callEntry struct {
	frameQueue chan Frame
}

type Client struct {
	options     *ClientOptions
	conn        *net.UDPConn
	sendQueue   chan Frame
	lock        sync.RWMutex
	state       ClientState
	callIDCount uint16
	callMap     map[callKey]*callEntry
}

func (c *Client) nextCallID() uint16 {
	c.lock.Lock()
	defer c.lock.Unlock()
	for {
		c.callIDCount++
		if c.callIDCount > 0x7fff {
			c.callIDCount = 1
		}
		key := callKey{isLocal: true, callID: c.callIDCount}
		if _, ok := c.callMap[key]; !ok {
			return c.callIDCount
		}
	}
}

func (c *Client) addCallEntry(key callKey, entry *callEntry) {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.callMap[key] = entry
}

func (c *Client) removeCallEntry(key callKey) {
	c.lock.Lock()
	defer c.lock.Unlock()
	delete(c.callMap, key)
}

func (c *Client) routeFrame(frame Frame) {
	key := callKey{isLocal: false, callID: frame.SrcCallNumber()}
	c.lock.RLock()
	entry, ok := c.callMap[key]
	c.lock.RUnlock()
	if ok {
		entry.frameQueue <- frame
		return
	}
	key = callKey{isLocal: true, callID: frame.DstCallNumber()}
	c.lock.RLock()
	entry, ok = c.callMap[key]
	c.lock.RUnlock()
	if ok {
		entry.frameQueue <- frame
		return
	}
	// Check for new incoming call
}

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

func NewClient(options *ClientOptions) *Client {
	return &Client{
		options: options,
		state:   Disconnected,
	}
}

func (c *Client) Connect() error {
	c.state = Connecting
	c.sendQueue = make(chan Frame, 100)
	c.callMap = make(map[callKey]*callEntry)

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

func (c *Client) SendFrame(frame Frame) {
	if c.state != Disconnected {
		c.sendQueue <- frame
	}
}
