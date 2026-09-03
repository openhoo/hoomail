package smtp

import (
	"bufio"
	"context"
	"io"
	"log"
	"net"
	"sync"
	"testing"
	"time"
)

type shutdownBarrierSession struct {
	mailEntered  chan struct{}
	rcptEntered  chan struct{}
	dataEntered  chan struct{}
	mailRelease  chan struct{}
	rcptRelease  chan struct{}
	dataRelease  chan struct{}
	logoutCalled chan struct{}
	logoutOnce   sync.Once
}

func (s *shutdownBarrierSession) Reset() {}

func (s *shutdownBarrierSession) Logout() error {
	s.logoutOnce.Do(func() { close(s.logoutCalled) })
	return nil
}

func (s *shutdownBarrierSession) Mail(string, *MailOptions) error {
	close(s.mailEntered)
	<-s.mailRelease
	return nil
}

func (s *shutdownBarrierSession) Rcpt(string, *RcptOptions) error {
	close(s.rcptEntered)
	<-s.rcptRelease
	return nil
}

func (s *shutdownBarrierSession) Data(io.Reader) error {
	close(s.dataEntered)
	<-s.dataRelease
	return nil
}

func smtpTestResponse(t *testing.T, reader *bufio.Reader, command string) {
	t.Helper()
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read response to %q: %v", command, err)
		}
		if len(line) >= 4 && line[3] == ' ' {
			return
		}
	}
}

func smtpTestCommand(t *testing.T, conn net.Conn, reader *bufio.Reader, command string) {
	t.Helper()
	if _, err := io.WriteString(conn, command+"\r\n"); err != nil {
		t.Fatalf("write %q: %v", command, err)
	}
	smtpTestResponse(t, reader, command)
}

func waitShutdownCallback(t *testing.T, callback <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-callback:
	case <-time.After(time.Second):
		t.Fatalf("%s callback did not start", name)
	}
}

func smtpTestStartCommand(t *testing.T, conn net.Conn, command string) {
	t.Helper()
	if _, err := io.WriteString(conn, command+"\r\n"); err != nil {
		t.Fatalf("write %q: %v", command, err)
	}
}

func smtpTestReleasedCommand(t *testing.T, conn net.Conn, reader *bufio.Reader, command string, entered <-chan struct{}, release chan struct{}) {
	t.Helper()
	if _, err := io.WriteString(conn, command+"\r\n"); err != nil {
		t.Fatalf("write %q: %v", command, err)
	}
	waitShutdownCallback(t, entered, command)
	close(release)
	smtpTestResponse(t, reader, command)
}

func TestShutdownDefersLogoutUntilHandlerExit(t *testing.T) {
	for _, test := range []struct {
		name               string
		command            string
		callback           func(*shutdownBarrierSession) <-chan struct{}
		release            func(*shutdownBarrierSession)
		setup              func(*testing.T, net.Conn, *bufio.Reader, *shutdownBarrierSession)
		readsReadyResponse bool
	}{
		{
			name:     "MAIL",
			command:  "MAIL FROM:<sender@example.test>",
			callback: func(session *shutdownBarrierSession) <-chan struct{} { return session.mailEntered },
			release:  func(session *shutdownBarrierSession) { close(session.mailRelease) },
		},
		{
			name:     "RCPT",
			command:  "RCPT TO:<recipient@example.test>",
			callback: func(session *shutdownBarrierSession) <-chan struct{} { return session.rcptEntered },
			release:  func(session *shutdownBarrierSession) { close(session.rcptRelease) },
			setup: func(t *testing.T, conn net.Conn, reader *bufio.Reader, session *shutdownBarrierSession) {
				smtpTestReleasedCommand(t, conn, reader, "MAIL FROM:<sender@example.test>", session.mailEntered, session.mailRelease)
			},
		},
		{
			name:               "DATA",
			command:            "DATA",
			callback:           func(session *shutdownBarrierSession) <-chan struct{} { return session.dataEntered },
			release:            func(session *shutdownBarrierSession) { close(session.dataRelease) },
			readsReadyResponse: true,
			setup: func(t *testing.T, conn net.Conn, reader *bufio.Reader, session *shutdownBarrierSession) {
				smtpTestReleasedCommand(t, conn, reader, "MAIL FROM:<sender@example.test>", session.mailEntered, session.mailRelease)
				smtpTestReleasedCommand(t, conn, reader, "RCPT TO:<recipient@example.test>", session.rcptEntered, session.rcptRelease)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			session := &shutdownBarrierSession{
				mailEntered:  make(chan struct{}),
				rcptEntered:  make(chan struct{}),
				dataEntered:  make(chan struct{}),
				mailRelease:  make(chan struct{}),
				rcptRelease:  make(chan struct{}),
				dataRelease:  make(chan struct{}),
				logoutCalled: make(chan struct{}),
			}
			server := NewServer(BackendFunc(func(*Conn) (Session, error) { return session, nil }))
			server.ErrorLog = log.New(io.Discard, "", 0)
			client, serverConn := net.Pipe()
			defer client.Close()
			conn := newConn(serverConn, server)
			server.wg.Add(1)
			go func() {
				defer server.wg.Done()
				_ = server.handleConn(conn)
			}()

			reader := bufio.NewReader(client)
			smtpTestResponse(t, reader, "greeting")
			smtpTestCommand(t, client, reader, "EHLO shutdown.test")
			if test.setup != nil {
				test.setup(t, client, reader, session)
			}
			smtpTestStartCommand(t, client, test.command)
			if test.readsReadyResponse {
				smtpTestResponse(t, reader, test.command)
			}

			shutdownDone := make(chan error, 1)
			go func() { shutdownDone <- server.Shutdown(context.Background()) }()
			select {
			case <-session.logoutCalled:
				t.Fatalf("Logout ran while %s callback was active", test.name)
			case <-shutdownDone:
				t.Fatalf("Shutdown returned while %s callback was active", test.name)
			case <-time.After(50 * time.Millisecond):
			}

			test.release(session)
			select {
			case err := <-shutdownDone:
				if err != nil {
					t.Fatalf("Shutdown: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("Shutdown did not finish after callback release")
			}
			select {
			case <-session.logoutCalled:
			case <-time.After(time.Second):
				t.Fatal("handler did not perform Logout")
			}
		})
	}
}

type bdatShutdownSession struct {
	dataEntered  chan struct{}
	logoutCalled chan struct{}
	logoutOnce   sync.Once
}

func (s *bdatShutdownSession) Reset() {}

func (s *bdatShutdownSession) Logout() error {
	s.logoutOnce.Do(func() { close(s.logoutCalled) })
	return nil
}

func (s *bdatShutdownSession) Mail(string, *MailOptions) error { return nil }

func (s *bdatShutdownSession) Rcpt(string, *RcptOptions) error { return nil }

func (s *bdatShutdownSession) Data(reader io.Reader) error {
	close(s.dataEntered)
	_, err := io.Copy(io.Discard, reader)
	return err
}

func TestShutdownAbortsStalledBDATPipe(t *testing.T) {
	session := &bdatShutdownSession{
		dataEntered:  make(chan struct{}),
		logoutCalled: make(chan struct{}),
	}
	server := NewServer(BackendFunc(func(*Conn) (Session, error) { return session, nil }))
	server.ErrorLog = log.New(io.Discard, "", 0)
	client, serverConn := net.Pipe()
	defer client.Close()
	conn := newConn(serverConn, server)
	server.wg.Add(1)
	go func() {
		defer server.wg.Done()
		_ = server.handleConn(conn)
	}()

	reader := bufio.NewReader(client)
	smtpTestResponse(t, reader, "greeting")
	smtpTestCommand(t, client, reader, "EHLO shutdown.test")
	smtpTestCommand(t, client, reader, "MAIL FROM:<sender@example.test>")
	smtpTestCommand(t, client, reader, "RCPT TO:<recipient@example.test>")
	smtpTestStartCommand(t, client, "BDAT 1 LAST")
	waitShutdownCallback(t, session.dataEntered, "BDAT DATA")

	payloadDone := make(chan error, 1)
	go func() {
		_, err := client.Write([]byte("x"))
		payloadDone <- err
	}()
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- server.Shutdown(context.Background()) }()
	select {
	case err := <-shutdownDone:
		if err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Shutdown did not abort the stalled BDAT pipe")
	}
	select {
	case <-payloadDone:
	case <-time.After(time.Second):
		t.Fatal("BDAT payload writer remained blocked after Shutdown")
	}
	select {
	case <-session.logoutCalled:
	case <-time.After(time.Second):
		t.Fatal("handler did not perform Logout after BDAT shutdown")
	}
}

var _ Session = (*shutdownBarrierSession)(nil)
