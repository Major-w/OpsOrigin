package tunnel

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
)

type Conn struct {
	raw net.Conn
}

func NewConn(raw net.Conn) *Conn {
	return &Conn{raw: raw}
}

func (c *Conn) Send(msg *TunnelMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal tunnel message: %w", err)
	}

	header := make([]byte, 4)
	binary.BigEndian.PutUint32(header, uint32(len(data)))

	if _, err := c.raw.Write(header); err != nil {
		return fmt.Errorf("write header: %w", err)
	}
	if _, err := c.raw.Write(data); err != nil {
		return fmt.Errorf("write payload: %w", err)
	}
	return nil
}

func (c *Conn) Receive() (*TunnelMessage, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(c.raw, header); err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}

	length := binary.BigEndian.Uint32(header)
	if length > 10*1024*1024 {
		return nil, fmt.Errorf("message too large: %d bytes", length)
	}

	data := make([]byte, length)
	if _, err := io.ReadFull(c.raw, data); err != nil {
		return nil, fmt.Errorf("read payload: %w", err)
	}

	var msg TunnelMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, fmt.Errorf("unmarshal tunnel message: %w", err)
	}
	return &msg, nil
}

func (c *Conn) Close() error {
	return c.raw.Close()
}

func (c *Conn) RemoteAddr() net.Addr {
	return c.raw.RemoteAddr()
}
