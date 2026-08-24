package smtpserver

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openhoo/hoomail/internal/store"
)

type recordingStore struct {
	mu     sync.Mutex
	inputs []store.StoreMessageInput
}

func (recorder *recordingStore) StoreMessage(_ context.Context, input store.StoreMessageInput) ([]store.StoredMessage, error) {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.inputs = append(recorder.inputs, input)
	return []store.StoredMessage{{MailboxID: 1, MessageID: 1}}, nil
}

func (store *recordingStore) last(t *testing.T) store.StoreMessageInput {
	t.Helper()
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.inputs) == 0 {
		t.Fatal("no message stored")
	}
	return store.inputs[len(store.inputs)-1]
}

func TestEnvelopeRecipientsIncludeBCCAndAreDeduplicated(t *testing.T) {
	messageStore := &recordingStore{}
	address, stop := startTestServer(t, messageStore)
	defer stop()

	client := dialSMTP(t, address)
	defer client.close()
	client.command(250, "EHLO test")
	client.command(250, "MAIL FROM:<sender@example.test>")
	client.command(250, "RCPT TO:<Visible@Example.Test>")
	client.command(250, "RCPT TO:<bcc@example.test>")
	client.command(250, "RCPT TO:<BCC@example.test>")
	client.command(354, "DATA")
	client.data(250, "From: Sender <sender@example.test>\r\nTo: Visible Person <visible@example.test>\r\nSubject: BCC capture\r\n\r\nhello")
	client.command(221, "QUIT")

	input := messageStore.last(t)
	if got, want := strings.Join(input.Recipients, ","), "visible@example.test,bcc@example.test"; got != want {
		t.Fatalf("recipients = %q, want %q", got, want)
	}
	if len(input.To) != 1 || input.To[0].Address != "visible@example.test" || input.To[0].Name == nil || *input.To[0].Name != "Visible Person" {
		t.Fatalf("unexpected To addresses: %#v", input.To)
	}
	if input.FromAddress == nil || *input.FromAddress != "sender@example.test" || input.FromName == nil || *input.FromName != "Sender" {
		t.Fatalf("unexpected From: address=%v name=%v", input.FromAddress, input.FromName)
	}
	if !strings.Contains(string(input.Raw), "Subject: BCC capture") {
		t.Fatal("raw message was not retained")
	}
}

func TestAdvertisedAndActualOversizeRejection(t *testing.T) {
	messageStore := &recordingStore{}
	address, stop := startTestServer(t, messageStore)
	defer stop()

	client := dialSMTP(t, address)
	defer client.close()
	response := client.command(250, "EHLO test")
	if !strings.Contains(response, fmt.Sprintf("SIZE %d", MaxMessageBytes)) {
		t.Fatalf("EHLO did not advertise limit: %q", response)
	}
	client.command(552, fmt.Sprintf("MAIL FROM:<sender@example.test> SIZE=%d", MaxMessageBytes+1))
	client.command(250, "MAIL FROM:<sender@example.test>")
	client.command(250, "RCPT TO:<recipient@example.test>")
	client.command(354, "DATA")

	oversizedBody := strings.Repeat(strings.Repeat("x", 998)+"\r\n", int(MaxMessageBytes/1000)+1)
	if _, err := fmt.Fprintf(client.conn, "From: sender@example.test\r\nTo: recipient@example.test\r\n\r\n%s.\r\n", oversizedBody); err != nil {
		t.Fatal(err)
	}
	client.read(552)
	client.command(221, "QUIT")

	messageStore.mu.Lock()
	defer messageStore.mu.Unlock()
	if len(messageStore.inputs) != 0 {
		t.Fatalf("stored %d oversized messages", len(messageStore.inputs))
	}
}

func boundedMessage(target int) []byte {
	header := []byte("From: sender@example.test\r\nTo: recipient@example.test\r\nSubject: exact limit\r\n\r\n")
	remaining := target - 2 - len(header)
	payload := append([]byte(nil), header...)
	for remaining > 0 {
		lineLength := remaining - 2
		if lineLength > 998 {
			lineLength = 998
		}
		payload = append(payload, bytes.Repeat([]byte{'x'}, lineLength)...)
		payload = append(payload, '\r', '\n')
		remaining -= lineLength + 2
	}
	return payload
}

func TestDataBoundaryAtAdvertisedLimit(t *testing.T) {
	messageStore := &recordingStore{}
	address, stop := startTestServer(t, messageStore)
	defer stop()

	client := dialSMTP(t, address)
	defer client.close()
	response := client.command(250, "EHLO test")
	if !strings.Contains(response, fmt.Sprintf("SIZE %d", MaxMessageBytes)) {
		t.Fatalf("EHLO did not advertise limit: %q", response)
	}
	client.command(250, "MAIL FROM:<sender@example.test>")
	client.command(250, "RCPT TO:<recipient@example.test>")
	client.command(354, "DATA")

	exact := boundedMessage(int(MaxMessageBytes))
	client.rawData(250, exact)
	if input := messageStore.last(t); len(input.Raw) != int(MaxMessageBytes) {
		t.Fatalf("stored raw length = %d, want %d", len(input.Raw), MaxMessageBytes)
	}

	client.command(250, "MAIL FROM:<sender@example.test>")
	client.command(250, "RCPT TO:<recipient@example.test>")
	client.command(354, "DATA")
	oversized := boundedMessage(int(MaxMessageBytes) + 1)
	client.rawData(552, oversized)

	messageStore.mu.Lock()
	defer messageStore.mu.Unlock()
	if len(messageStore.inputs) != 1 {
		t.Fatalf("stored %d messages, want one", len(messageStore.inputs))
	}
}

func TestNewAppliesFiniteLimitsAndTimeouts(t *testing.T) {
	service := New(&recordingStore{})
	tests := []struct {
		name string
		got  any
		want any
	}{
		{"domain", service.server.Domain, "localhost"},
		{"message bytes", service.server.MaxMessageBytes, MaxMessageBytes},
		{"recipients", service.server.MaxRecipients, MaxRecipients},
		{"read timeout", service.server.ReadTimeout, readTimeout},
		{"write timeout", service.server.WriteTimeout, writeTimeout},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.got != test.want {
				t.Fatalf("got %v, want %v", test.got, test.want)
			}
		})
	}
}

func TestRcptLimitOverProtocol(t *testing.T) {
	messageStore := &recordingStore{}
	address, stop := startTestServer(t, messageStore)
	defer stop()

	client := dialSMTP(t, address)
	defer client.close()
	response := client.command(250, "EHLO test")
	if !strings.Contains(response, fmt.Sprintf("RCPTMAX=%d", MaxRecipients)) {
		t.Fatalf("EHLO did not advertise recipient limit: %q", response)
	}
	client.command(250, "MAIL FROM:<sender@example.test>")
	for i := 0; i < MaxRecipients; i++ {
		client.command(250, fmt.Sprintf("RCPT TO:<recipient-%d@example.test>", i))
	}
	client.command(452, "RCPT TO:<one-too-many@example.test>")
	client.command(354, "DATA")
	client.data(250, "From: sender@example.test\r\nTo: recipient-0@example.test\r\n\r\nhello")

	input := messageStore.last(t)
	if len(input.Recipients) != MaxRecipients {
		t.Fatalf("stored %d envelope recipients, want %d", len(input.Recipients), MaxRecipients)
	}
}

type failingStore struct{ err error }

func (store *failingStore) StoreMessage(context.Context, store.StoreMessageInput) ([]store.StoredMessage, error) {
	return nil, store.err
}

func TestStorageFailureIsRetryable(t *testing.T) {
	address, stop := startTestServer(t, &failingStore{err: errors.New("database is locked")})
	defer stop()

	client := dialSMTP(t, address)
	defer client.close()
	client.command(250, "EHLO test")
	client.command(250, "MAIL FROM:<sender@example.test>")
	client.command(250, "RCPT TO:<recipient@example.test>")
	client.command(354, "DATA")
	response := client.data(451, "From: sender@example.test\r\nTo: recipient@example.test\r\n\r\nhello")
	if !strings.Contains(response, "4.3.0") {
		t.Fatalf("response = %q, want enhanced 4.3.0", response)
	}
	if strings.Contains(response, "database is locked") {
		t.Fatalf("response leaked storage detail: %q", response)
	}
}

func TestParseFailureRemainsPermanent(t *testing.T) {
	address, stop := startTestServer(t, &recordingStore{})
	defer stop()

	client := dialSMTP(t, address)
	defer client.close()
	client.command(250, "EHLO test")
	client.command(250, "MAIL FROM:<sender@example.test>")
	client.command(250, "RCPT TO:<recipient@example.test>")
	client.command(354, "DATA")
	response := client.data(554, "not-a-header\r\n\r\nbody")
	if !strings.Contains(response, "5.0.0") {
		t.Fatalf("response = %q, want enhanced 5.0.0", response)
	}
}

type blockingStore struct {
	started chan context.Context
}

func (store *blockingStore) StoreMessage(ctx context.Context, _ store.StoreMessageInput) ([]store.StoredMessage, error) {
	store.started <- ctx
	<-ctx.Done()
	return nil, ctx.Err()
}

func validMessage() []byte {
	return []byte("From: sender@example.test\r\nTo: recipient@example.test\r\nSubject: context\r\n\r\nhello")
}

func TestShutdownCancelsStoreMessageContext(t *testing.T) {
	messageStore := &blockingStore{started: make(chan context.Context, 1)}
	service := New(messageStore)
	sessionValue, err := service.NewSession(nil)
	if err != nil {
		t.Fatal(err)
	}
	session := sessionValue.(*session)
	session.Mail("sender@example.test", nil)
	session.Rcpt("recipient@example.test", nil)
	dataDone := make(chan error, 1)
	go func() { dataDone <- session.Data(bytes.NewReader(validMessage())) }()

	ctx := <-messageStore.started
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- service.Shutdown(context.Background()) }()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("service shutdown did not cancel StoreMessage context")
	}
	if err := <-dataDone; err == nil {
		t.Fatal("StoreMessage cancellation unexpectedly succeeded")
	}
	if err := <-shutdownDone; err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestLogoutCancelsStoreMessageContext(t *testing.T) {
	messageStore := &blockingStore{started: make(chan context.Context, 1)}
	service := New(messageStore)
	sessionValue, err := service.NewSession(nil)
	if err != nil {
		t.Fatal(err)
	}
	session := sessionValue.(*session)
	session.Mail("sender@example.test", nil)
	session.Rcpt("recipient@example.test", nil)
	dataDone := make(chan error, 1)
	go func() { dataDone <- session.Data(bytes.NewReader(validMessage())) }()

	ctx := <-messageStore.started
	if err := session.Logout(); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("Logout did not cancel StoreMessage context")
	}
	<-dataDone
}

func TestServeAfterCloseReturnsErrServerClosed(t *testing.T) {
	service := New(&recordingStore{})
	if err := service.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := service.Serve(listener); !errors.Is(err, ErrServerClosed) {
		t.Fatalf("Serve after Close: %v, want %v", err, ErrServerClosed)
	}
}

func TestShutdownClosesUnregisteredListener(t *testing.T) {
	service := New(&recordingStore{})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	service.mu.Lock()
	service.listener = listener
	service.mu.Unlock()

	if err := service.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if _, err := listener.Accept(); err == nil {
		t.Fatal("listener remained open after Shutdown")
	}
}

func waitForClosed(t *testing.T, connection net.Conn) {
	t.Helper()
	if err := connection.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var buffer [256]byte
	for {
		if _, err := connection.Read(buffer[:]); err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				t.Fatal("connection did not close before deadline")
			}
			return
		}
	}
}

func TestShutdownRaceDoesNotLeaveListener(t *testing.T) {
	for range 100 {
		service := New(&recordingStore{})
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		address := listener.Addr().String()
		serveDone := make(chan error, 1)
		go func() { serveDone <- service.Serve(listener) }()

		if err := service.Shutdown(context.Background()); err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
		select {
		case err := <-serveDone:
			if err != nil && !errors.Is(err, ErrServerClosed) {
				t.Fatalf("Serve: %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("Serve remained active after Shutdown")
		}
		connection, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if err == nil {
			connection.Close()
			t.Fatal("listener accepted a connection after Shutdown")
		}
	}
}

func startTimeoutServer(t *testing.T, messageStore Store, timeout time.Duration) (string, func()) {
	t.Helper()
	service := New(messageStore)
	service.server.ReadTimeout = timeout
	service.server.WriteTimeout = timeout
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- service.Serve(listener) }()
	return listener.Addr().String(), func() {
		_ = service.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Serve: %v", err)
			}
		case <-time.After(time.Second):
			t.Error("SMTP server did not close")
		}
	}
}

func TestIdleConnectionTimesOut(t *testing.T) {
	address, stop := startTimeoutServer(t, &recordingStore{}, 100*time.Millisecond)
	defer stop()
	client := dialSMTP(t, address)
	defer client.close()
	waitForClosed(t, client.conn)
}

func TestStalledDataTransferTimesOut(t *testing.T) {
	address, stop := startTimeoutServer(t, &recordingStore{}, 100*time.Millisecond)
	defer stop()
	client := dialSMTP(t, address)
	defer client.close()
	client.command(250, "EHLO test")
	client.command(250, "MAIL FROM:<sender@example.test>")
	client.command(250, "RCPT TO:<recipient@example.test>")
	client.command(354, "DATA")
	if _, err := fmt.Fprint(client.conn, "From: sender@example.test\r\n"); err != nil {
		t.Fatal(err)
	}
	waitForClosed(t, client.conn)
}

func TestGracefulShutdownAfterClientLogout(t *testing.T) {
	messageStore := &recordingStore{}
	service := New(messageStore)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- service.Serve(listener) }()

	client := dialSMTP(t, listener.Addr().String())
	client.command(250, "EHLO test")
	client.command(221, "QUIT")
	client.close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := service.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Serve did not return after Shutdown")
	}
}

type smtpTestClient struct {
	t      *testing.T
	conn   net.Conn
	reader *bufio.Reader
}

func dialSMTP(t *testing.T, address string) *smtpTestClient {
	t.Helper()
	conn, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	client := &smtpTestClient{t: t, conn: conn, reader: bufio.NewReader(conn)}
	client.read(220)
	return client
}

func (client *smtpTestClient) command(code int, command string) string {
	client.t.Helper()
	if _, err := fmt.Fprintf(client.conn, "%s\r\n", command); err != nil {
		client.t.Fatal(err)
	}
	return client.read(code)
}

func (client *smtpTestClient) data(code int, message string) string {
	client.t.Helper()
	if _, err := fmt.Fprintf(client.conn, "%s\r\n.\r\n", message); err != nil {
		client.t.Fatal(err)
	}
	return client.read(code)
}

func (client *smtpTestClient) read(code int) string {
	client.t.Helper()
	var lines []string
	for {
		line, err := client.reader.ReadString('\n')
		if err != nil {
			client.t.Fatal(err)
		}
		line = strings.TrimRight(line, "\r\n")
		lines = append(lines, line)
		if len(line) < 4 || line[3] != '-' {
			if !strings.HasPrefix(line, fmt.Sprintf("%d ", code)) {
				client.t.Fatalf("SMTP response = %q, want code %d", strings.Join(lines, "\n"), code)
			}
			return strings.Join(lines, "\n")
		}
	}
}
func (client *smtpTestClient) rawData(code int, payload []byte) string {
	client.t.Helper()
	if _, err := client.conn.Write(payload); err != nil {
		client.t.Fatal(err)
	}
	if _, err := client.conn.Write([]byte("\r\n.\r\n")); err != nil {
		client.t.Fatal(err)
	}
	return client.read(code)
}

func (client *smtpTestClient) close() { _ = client.conn.Close() }

func startTestServer(t *testing.T, messageStore Store) (string, func()) {
	t.Helper()
	service := New(messageStore)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- service.Serve(listener) }()
	return listener.Addr().String(), func() {
		_ = service.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Serve: %v", err)
			}
		case <-time.After(time.Second):
			t.Error("SMTP server did not close")
		}
	}
}
