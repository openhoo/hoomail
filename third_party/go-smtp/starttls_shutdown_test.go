package smtp

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"math/big"
	"net"
	"sync"
	"testing"
	"time"
)

type startTLSShutdownConn struct {
	net.Conn
	closeStarted chan struct{}
	closeRelease chan struct{}
	startOnce    sync.Once
	releaseOnce  sync.Once
}

func (c *startTLSShutdownConn) Close() error {
	c.startOnce.Do(func() { close(c.closeStarted) })
	<-c.closeRelease
	return c.Conn.Close()
}

func (c *startTLSShutdownConn) releaseClose() {
	c.releaseOnce.Do(func() { close(c.closeRelease) })
}

type startTLSShutdownSession struct {
	logoutCalled chan struct{}
	logoutOnce   sync.Once
}

func (s *startTLSShutdownSession) Reset() {}

func (s *startTLSShutdownSession) Logout() error {
	s.logoutOnce.Do(func() { close(s.logoutCalled) })
	return nil
}

func (s *startTLSShutdownSession) Mail(string, *MailOptions) error { return nil }

func (s *startTLSShutdownSession) Rcpt(string, *RcptOptions) error { return nil }

func (s *startTLSShutdownSession) Data(io.Reader) error { return nil }

func startTLSShutdownTLSConfig(t *testing.T) *tls.Config {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate TLS key: %v", err)
	}
	certificate := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create TLS certificate: %v", err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
	}
}

func TestShutdownDuringSTARTTLS(t *testing.T) {
	session := &startTLSShutdownSession{logoutCalled: make(chan struct{})}
	server := NewServer(BackendFunc(func(*Conn) (Session, error) { return session, nil }))
	server.TLSConfig = startTLSShutdownTLSConfig(t)

	client, accepted := net.Pipe()
	gated := &startTLSShutdownConn{
		Conn:         accepted,
		closeStarted: make(chan struct{}),
		closeRelease: make(chan struct{}),
	}
	defer client.Close()
	defer gated.releaseClose()

	conn := newConn(gated, server)
	server.wg.Add(1)
	go func() {
		defer server.wg.Done()
		_ = server.handleConn(conn)
	}()

	reader := bufio.NewReader(client)
	smtpTestResponse(t, reader, "greeting")
	smtpTestCommand(t, client, reader, "EHLO shutdown.test")
	smtpTestStartCommand(t, client, "STARTTLS")
	smtpTestResponse(t, reader, "STARTTLS")

	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- server.Shutdown(context.Background()) }()
	select {
	case <-gated.closeStarted:
	case <-time.After(time.Second):
		t.Fatal("Shutdown did not close the accepted transport")
	}
	select {
	case <-shutdownDone:
		t.Fatal("Shutdown returned before the accepted transport was released")
	case <-time.After(50 * time.Millisecond):
	}

	tlsClient := tls.Client(client, &tls.Config{InsecureSkipVerify: true})
	handshakeDone := make(chan error, 1)
	go func() { handshakeDone <- tlsClient.Handshake() }()
	select {
	case err := <-handshakeDone:
		if err != nil {
			t.Fatalf("STARTTLS handshake: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("STARTTLS handshake did not complete while Shutdown was waiting")
	}
	select {
	case <-session.logoutCalled:
	case <-time.After(time.Second):
		t.Fatal("STARTTLS handler did not finish its TLS transition")
	}

	gated.releaseClose()
	select {
	case err := <-shutdownDone:
		if err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Shutdown did not finish after releasing the accepted transport")
	}

	_ = tlsClient.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := tlsClient.Read(make([]byte, 1)); err == nil {
		t.Fatal("TLS client transport remained open after Shutdown")
	}
}

var _ Session = (*startTLSShutdownSession)(nil)
