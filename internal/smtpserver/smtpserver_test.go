package smtpserver

import (
	"bufio"
	"context"
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
