package netrans

import (
	"net"
	"time"
)

type TimeoutConn struct {
	net.Conn
	readTimeout  time.Duration
	writeTimeout time.Duration
}

func NewTimeoutConn(conn net.Conn, readTimeout, writeTimeout time.Duration) net.Conn {
	return &TimeoutConn{
		Conn:         conn,
		readTimeout:  readTimeout,
		writeTimeout: writeTimeout,
	}
}

func (c *TimeoutConn) Read(b []byte) (n int, err error) {
	if c.readTimeout > 0 {
		err := c.Conn.SetReadDeadline(time.Now().Add(c.readTimeout))
		if err != nil {
			return 0, err
		}
	}
	return c.Conn.Read(b)
}

func (c *TimeoutConn) Write(b []byte) (n int, err error) {
	if c.writeTimeout > 0 {
		err := c.Conn.SetWriteDeadline(time.Now().Add(c.writeTimeout))
		if err != nil {
			return 0, err
		}
	}
	return c.Conn.Write(b)
}
