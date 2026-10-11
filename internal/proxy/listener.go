package proxy

import (
	"net"
	"sync"
	"time"
)

const idleTimeout = time.Minute

// Listener tracks hijacked connections because http.Server cannot close them.
type Listener struct {
	net.Listener
	mu          sync.Mutex
	connections map[*limitedConn]struct{}
	closed      bool
	max         int
}

func Track(l net.Listener, max int) *Listener {
	return &Listener{Listener: l, connections: make(map[*limitedConn]struct{}), max: max}
}
func (l *Listener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		l.mu.Lock()
		if l.closed || len(l.connections) >= l.max {
			closed := l.closed
			l.mu.Unlock()
			c.Close()
			if closed {
				return nil, net.ErrClosed
			}
			continue
		}
		wrapped := &limitedConn{Conn: c, owner: l}
		l.connections[wrapped] = struct{}{}
		l.mu.Unlock()
		return wrapped, nil
	}
}
func (l *Listener) Close() error {
	l.mu.Lock()
	l.closed = true
	conns := make([]*limitedConn, 0, len(l.connections))
	for c := range l.connections {
		conns = append(conns, c)
	}
	l.mu.Unlock()
	err := l.Listener.Close()
	for _, c := range conns {
		c.Close()
	}
	return err
}

// limitedConn closes idle connections; an active stream may last as long as it keeps moving.
type limitedConn struct {
	net.Conn
	owner         *Listener
	once          sync.Once
	deadlineMu    sync.Mutex
	readDeadline  time.Time
	writeDeadline time.Time
}

func (c *limitedConn) deadline(requested time.Time) time.Time {
	d := time.Now().Add(idleTimeout)
	if !requested.IsZero() && requested.Before(d) {
		d = requested
	}
	return d
}

// Preserve caller deadlines: net/http interrupts its background read before Hijack.
func (c *limitedConn) SetReadDeadline(deadline time.Time) error {
	c.deadlineMu.Lock()
	defer c.deadlineMu.Unlock()
	c.readDeadline = deadline
	return c.Conn.SetReadDeadline(c.deadline(deadline))
}

func (c *limitedConn) SetWriteDeadline(deadline time.Time) error {
	c.deadlineMu.Lock()
	defer c.deadlineMu.Unlock()
	c.writeDeadline = deadline
	return c.Conn.SetWriteDeadline(c.deadline(deadline))
}

func (c *limitedConn) SetDeadline(deadline time.Time) error {
	c.deadlineMu.Lock()
	defer c.deadlineMu.Unlock()
	c.readDeadline = deadline
	c.writeDeadline = deadline
	return c.Conn.SetDeadline(c.deadline(deadline))
}

func (c *limitedConn) Read(b []byte) (int, error) {
	c.deadlineMu.Lock()
	err := c.Conn.SetReadDeadline(c.deadline(c.readDeadline))
	c.deadlineMu.Unlock()
	if err != nil {
		return 0, err
	}
	return c.Conn.Read(b)
}
func (c *limitedConn) Write(b []byte) (int, error) {
	c.deadlineMu.Lock()
	err := c.Conn.SetWriteDeadline(c.deadline(c.writeDeadline))
	c.deadlineMu.Unlock()
	if err != nil {
		return 0, err
	}
	return c.Conn.Write(b)
}
func (c *limitedConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { c.owner.mu.Lock(); delete(c.owner.connections, c); c.owner.mu.Unlock() })
	return err
}
