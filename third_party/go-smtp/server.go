package smtp

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log"
	"net"
	"os"
	"sync"
	"time"
)

var ErrServerClosed = errors.New("smtp: server already closed")

// Logger interface is used by Server to report unexpected internal errors.
type Logger interface {
	Printf(format string, v ...interface{})
	Println(v ...interface{})
}

// A SMTP server.
type Server struct {
	// The type of network, "tcp" or "unix".
	Network string
	// TCP or Unix address to listen on.
	Addr string
	// The server TLS configuration.
	TLSConfig *tls.Config
	// Enable LMTP mode, as defined in RFC 2033.
	LMTP bool

	Domain            string
	MaxRecipients     int
	MaxMessageBytes   int64
	MaxLineLength     int
	AllowInsecureAuth bool
	Debug             io.Writer
	ErrorLog          Logger
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration

	// Advertise SMTPUTF8 (RFC 6531) capability.
	// Should be used only if backend supports it.
	EnableSMTPUTF8 bool

	// Advertise REQUIRETLS (RFC 8689) capability.
	// Should be used only if backend supports it.
	EnableREQUIRETLS bool

	// Advertise BINARYMIME (RFC 3030) capability.
	// Should be used only if backend supports it.
	EnableBINARYMIME bool

	// Advertise DSN (RFC 3461) capability.
	// Should be used only if backend supports it.
	EnableDSN bool

	// Advertise RRVS (RFC 7293) capability.
	// Should be used only if backend supports it.
	EnableRRVS bool

	// Advertise DELIVERBY (RFC 2852) capability.
	// Should be used only if backend supports it.
	EnableDELIVERBY bool
	// The minimum time, with seconds precision, that a client
	// may specify in the BY argument with return mode.
	// A zero value indicates no set minimum.
	// Only use if DELIVERBY is enabled.
	MinimumDeliverByTime time.Duration

	// Advertise MT-PRIORITY (RFC 6710) capability.
	// Should only be used if backend supports it.
	EnableMTPRIORITY bool
	// The priority profile mapping as defined
	// in RFC 6710 section 10.2.
	//
	// Default value of NONE to advertise no specific profile.
	MtPriorityProfile PriorityProfile

	// The server backend.
	Backend Backend

	wg   sync.WaitGroup
	done chan struct{}

	locker    sync.Mutex
	listeners []net.Listener
	conns     map[*Conn]struct{}
}

// New creates a new SMTP server.
func NewServer(be Backend) *Server {
	return &Server{
		// Doubled maximum line length per RFC 5321 (Section 4.5.3.1.6)
		MaxLineLength: 2000,

		Backend:  be,
		done:     make(chan struct{}, 1),
		ErrorLog: log.New(os.Stderr, "smtp/server ", log.LstdFlags),
		conns:    make(map[*Conn]struct{}),
	}
}

// Serve accepts incoming connections on the Listener l.
func (s *Server) Serve(l net.Listener) error {
	s.locker.Lock()
	select {
	case <-s.done:
		s.locker.Unlock()
		_ = l.Close()
		return ErrServerClosed
	default:
		s.listeners = append(s.listeners, l)
		s.locker.Unlock()
	}

	var tempDelay time.Duration // how long to sleep on accept failure

	for {
		c, err := l.Accept()
		if err != nil {
			select {
			case <-s.done:
				// we called Close()
				return nil
			default:
			}
			if ne, ok := err.(net.Error); ok && ne.Temporary() {
				if tempDelay == 0 {
					tempDelay = 5 * time.Millisecond
				} else {
					tempDelay *= 2
				}
				if max := 1 * time.Second; tempDelay > max {
					tempDelay = max
				}
				s.ErrorLog.Printf("accept error: %s; retrying in %s", err, tempDelay)
				time.Sleep(tempDelay)
				continue
			}
			return err
		}

		conn := newConn(c, s)
		s.locker.Lock()
		select {
		case <-s.done:
			s.locker.Unlock()
			_ = conn.Close()
			return nil
		default:
			s.conns[conn] = struct{}{}
			s.wg.Add(1)
			s.locker.Unlock()
		}
		go func() {
			defer s.wg.Done()

			err := s.handleConn(conn)
			if err != nil {
				s.ErrorLog.Printf("error handling %v: %s", c.RemoteAddr(), err)
			}
		}()
	}
}

func (s *Server) handleConn(c *Conn) error {
	s.locker.Lock()
	s.conns[c] = struct{}{}
	s.locker.Unlock()

	defer func() {
		c.Close()

		s.locker.Lock()
		delete(s.conns, c)
		s.locker.Unlock()
	}()

	if tlsConn, ok := c.conn.(*tls.Conn); ok {
		if d := s.ReadTimeout; d != 0 {
			c.conn.SetReadDeadline(time.Now().Add(d))
		}
		if d := s.WriteTimeout; d != 0 {
			c.conn.SetWriteDeadline(time.Now().Add(d))
		}
		if err := tlsConn.Handshake(); err != nil {
			return err
		}
	}

	c.greet()

	for {
		line, err := c.readLine()
		if err == nil {
			cmd, arg, err := parseCmd(line)
			if err != nil {
				c.protocolError(501, EnhancedCode{5, 5, 2}, "Bad command")
				continue
			}

			c.handle(cmd, arg)
		} else {
			if err == io.EOF || errors.Is(err, net.ErrClosed) {
				return nil
			}
			if err == ErrTooLongLine {
				c.writeResponse(500, EnhancedCode{5, 4, 0}, "Too long line, closing connection")
				return nil
			}

			if neterr, ok := err.(net.Error); ok && neterr.Timeout() {
				c.writeResponse(421, EnhancedCode{4, 4, 2}, "Idle timeout, bye bye")
				return nil
			}

			c.writeResponse(421, EnhancedCode{4, 4, 0}, "Connection error, sorry")
			return err
		}
	}
}

func (s *Server) network() string {
	if s.Network != "" {
		return s.Network
	}
	if s.LMTP {
		return "unix"
	}
	return "tcp"
}

// ListenAndServe listens on the network address s.Addr and then calls Serve
// to handle requests on incoming connections.
//
// If s.Addr is blank and LMTP is disabled, ":smtp" is used.
func (s *Server) ListenAndServe() error {
	network := s.network()

	addr := s.Addr
	if !s.LMTP && addr == "" {
		addr = ":smtp"
	}

	l, err := net.Listen(network, addr)
	if err != nil {
		return err
	}

	return s.Serve(l)
}

// ListenAndServeTLS listens on the TCP network address s.Addr and then calls
// Serve to handle requests on incoming TLS connections.
//
// If s.Addr is blank and LMTP is disabled, ":smtps" is used.
func (s *Server) ListenAndServeTLS() error {
	network := s.network()

	addr := s.Addr
	if !s.LMTP && addr == "" {
		addr = ":smtps"
	}

	l, err := tls.Listen(network, addr, s.TLSConfig)
	if err != nil {
		return err
	}

	return s.Serve(l)
}

// Close immediately closes all active listeners and connections.
//
// Close returns any error returned from closing the server's underlying
// listener(s).
func (s *Server) Close() error {
	listeners, conns, alreadyClosed := s.beginShutdown()
	if alreadyClosed {
		return ErrServerClosed
	}
	return closeResources(listeners, conns)
}

// Shutdown closes all open listeners and active connections, then waits for
// every connection goroutine to return. If the provided context expires before
// the shutdown is complete, Shutdown returns the context's error, otherwise
// it returns any error returned from closing the Server's underlying
// Listener(s).
func (s *Server) Shutdown(ctx context.Context) error {
	listeners, conns, alreadyClosed := s.beginShutdown()
	if alreadyClosed {
		return ErrServerClosed
	}
	err := closeResources(listeners, conns)

	connDone := make(chan struct{})
	go func() {
		defer close(connDone)
		s.wg.Wait()
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-connDone:
		return err
	}
}

func (s *Server) beginShutdown() ([]net.Listener, []*Conn, bool) {
	s.locker.Lock()
	defer s.locker.Unlock()
	select {
	case <-s.done:
		return nil, nil, true
	default:
		close(s.done)
	}
	listeners := append([]net.Listener(nil), s.listeners...)
	conns := make([]*Conn, 0, len(s.conns))
	for conn := range s.conns {
		conns = append(conns, conn)
	}
	return listeners, conns, false
}

func closeResources(listeners []net.Listener, conns []*Conn) error {
	var err error
	for _, listener := range listeners {
		if closeErr := listener.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}
	for _, conn := range conns {
		_ = conn.closeTransport()
	}
	return err
}
