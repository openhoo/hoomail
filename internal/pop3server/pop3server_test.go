package pop3server

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openhoo/hoomail/internal/store"
)

type recordingStore struct {
	mu          sync.Mutex
	messages    []store.POP3Message
	openCalls   []string
	deleteCalls [][]int64
	openErr     error
	deleteErr   error
}

func (recording *recordingStore) OpenPOP3Mailbox(_ context.Context, address string) ([]store.POP3Message, error) {
	recording.mu.Lock()
	defer recording.mu.Unlock()
	recording.openCalls = append(recording.openCalls, address)
	return recording.messages, recording.openErr
}

func (recording *recordingStore) DeleteMessages(_ context.Context, ids []int64) ([]int64, error) {
	recording.mu.Lock()
	defer recording.mu.Unlock()
	recording.deleteCalls = append(recording.deleteCalls, append([]int64(nil), ids...))
	return nil, recording.deleteErr
}

func (recording *recordingStore) snapshot() ([]string, [][]int64) {
	recording.mu.Lock()
	defer recording.mu.Unlock()
	openCalls := append([]string(nil), recording.openCalls...)
	deleteCalls := make([][]int64, len(recording.deleteCalls))
	for index := range recording.deleteCalls {
		deleteCalls[index] = append([]int64(nil), recording.deleteCalls[index]...)
	}
	return openCalls, deleteCalls
}

type testServer struct {
	service *Service
	address string
	done    chan error
}

func startTestServer(t *testing.T, messageStore Store) *testServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	service := New(messageStore)
	done := make(chan error, 1)
	go func() { done <- service.Serve(listener) }()
	server := &testServer{service: service, address: listener.Addr().String(), done: done}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := service.Shutdown(ctx); err != nil {
			t.Errorf("shutdown: %v", err)
		}
		select {
		case err := <-done:
			if !errors.Is(err, ErrServerClosed) {
				t.Errorf("Serve returned %v, want ErrServerClosed", err)
			}
		case <-time.After(time.Second):
			t.Error("Serve did not return")
		}
	})
	return server
}

type pop3Client struct {
	t       *testing.T
	conn    net.Conn
	reader  *bufio.Reader
	welcome string
}

func dialPOP3(t *testing.T, address string) *pop3Client {
	t.Helper()
	conn, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	client := &pop3Client{t: t, conn: conn, reader: bufio.NewReader(conn)}
	client.welcome = client.readLine()
	return client
}

func (client *pop3Client) close() { _ = client.conn.Close() }

func (client *pop3Client) command(command string) string {
	client.t.Helper()
	if _, err := fmt.Fprintf(client.conn, "%s\r\n", command); err != nil {
		client.t.Fatal(err)
	}
	return client.readLine()
}

func (client *pop3Client) multiline(command string) (string, []string) {
	client.t.Helper()
	status := client.command(command)
	if !strings.HasPrefix(status, "+OK") {
		return status, nil
	}
	var lines []string
	for {
		line := client.readLine()
		if line == "." {
			return status, lines
		}
		if strings.HasPrefix(line, "..") {
			line = line[1:]
		}
		lines = append(lines, line)
	}
}

func (client *pop3Client) readLine() string {
	client.t.Helper()
	line, err := client.reader.ReadString('\n')
	if err != nil {
		client.t.Fatal(err)
	}
	return strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
}

func login(t *testing.T, client *pop3Client, address string) {
	t.Helper()
	if response := client.command("USER " + address); !strings.HasPrefix(response, "+OK") {
		t.Fatalf("USER response = %q", response)
	}
	if response := client.command("PASS anything-at-all"); !strings.HasPrefix(response, "+OK") {
		t.Fatalf("PASS response = %q", response)
	}
}

func TestMailboxSelectionIsDelegatedAndSnapshotTakenAfterPASS(t *testing.T) {
	recording := &recordingStore{messages: []store.POP3Message{{ID: 7, Raw: []byte("Subject: original\r\n\r\nbody\r\n")}}}
	server := startTestServer(t, recording)
	client := dialPOP3(t, server.address)
	defer client.close()

	if !strings.HasPrefix(client.welcome, "+OK") {
		t.Fatalf("welcome = %q", client.welcome)
	}
	if response := client.command("USER Missing@Example.Test"); !strings.HasPrefix(response, "+OK") {
		t.Fatalf("USER response = %q", response)
	}
	if calls, _ := recording.snapshot(); len(calls) != 0 {
		t.Fatalf("OpenPOP3Mailbox called before PASS: %v", calls)
	}
	if response := client.command("PASS ignored"); !strings.HasPrefix(response, "+OK") {
		t.Fatalf("PASS response = %q", response)
	}
	calls, _ := recording.snapshot()
	if want := []string{"Missing@Example.Test"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("OpenPOP3Mailbox calls = %v, want %v", calls, want)
	}

	// Mutating the store-owned bytes after PASS must not alter the transaction snapshot.
	recording.messages[0].Raw[9] = 'X'
	_, lines := client.multiline("RETR 1")
	if got := strings.Join(lines, "\n"); !strings.Contains(got, "Subject: original") {
		t.Fatalf("RETR did not use PASS snapshot: %q", got)
	}
}

func TestRetrievalListingUIDLTOPAndDotStuffing(t *testing.T) {
	rawOne := []byte("From: sender@example.test\r\nSubject: First\r\n\r\nline one\r\n.dot line\r\nline three\r\n")
	rawTwo := []byte("Subject: Second\r\n\r\nbody\r\n")
	recording := &recordingStore{messages: []store.POP3Message{{ID: 41, Raw: rawOne}, {ID: 99, Raw: rawTwo}}}
	server := startTestServer(t, recording)
	client := dialPOP3(t, server.address)
	defer client.close()
	login(t, client, "inbox@example.test")

	if response := client.command("STAT"); response != fmt.Sprintf("+OK 2 %d", len(rawOne)+len(rawTwo)) {
		t.Fatalf("STAT = %q", response)
	}
	if response := client.command("LIST 1"); response != fmt.Sprintf("+OK 1 %d", len(rawOne)) {
		t.Fatalf("LIST 1 = %q", response)
	}
	status, lines := client.multiline("LIST")
	if !strings.HasPrefix(status, "+OK 2 messages") || !reflect.DeepEqual(lines, []string{fmt.Sprintf("1 %d", len(rawOne)), fmt.Sprintf("2 %d", len(rawTwo))}) {
		t.Fatalf("LIST = %q, %v", status, lines)
	}
	if response := client.command("UIDL 2"); response != "+OK 2 99" {
		t.Fatalf("UIDL 2 = %q", response)
	}
	_, lines = client.multiline("UIDL")
	if want := []string{"1 41", "2 99"}; !reflect.DeepEqual(lines, want) {
		t.Fatalf("UIDL = %v, want %v", lines, want)
	}
	status, lines = client.multiline("RETR 1")
	if status != fmt.Sprintf("+OK %d octets", len(rawOne)) {
		t.Fatalf("RETR status = %q", status)
	}
	if want := []string{"From: sender@example.test", "Subject: First", "", "line one", ".dot line", "line three"}; !reflect.DeepEqual(lines, want) {
		t.Fatalf("RETR lines = %#v, want %#v", lines, want)
	}
	_, lines = client.multiline("TOP 1 2")
	if want := []string{"From: sender@example.test", "Subject: First", "", "line one", ".dot line"}; !reflect.DeepEqual(lines, want) {
		t.Fatalf("TOP lines = %#v, want %#v", lines, want)
	}
	_, capabilities := client.multiline("CAPA")
	if want := []string{"USER", "UIDL", "TOP"}; !reflect.DeepEqual(capabilities, want) {
		t.Fatalf("CAPA = %v, want %v", capabilities, want)
	}
}

func TestDeleteCommitsOnlyOnQUITAndRSETRestores(t *testing.T) {
	recording := &recordingStore{messages: []store.POP3Message{{ID: 10, Raw: []byte("A: b\r\n\r\na\r\n")}, {ID: 20, Raw: []byte("A: b\r\n\r\nb\r\n")}}}
	server := startTestServer(t, recording)

	client := dialPOP3(t, server.address)
	login(t, client, "delete@example.test")
	if response := client.command("DELE 1"); !strings.HasPrefix(response, "+OK") {
		t.Fatalf("DELE = %q", response)
	}
	if response := client.command("STAT"); !strings.HasPrefix(response, "+OK 1 ") {
		t.Fatalf("STAT after DELE = %q", response)
	}
	if response := client.command("RSET"); !strings.HasPrefix(response, "+OK 2 messages") {
		t.Fatalf("RSET = %q", response)
	}
	if response := client.command("DELE 2"); !strings.HasPrefix(response, "+OK") {
		t.Fatalf("DELE 2 = %q", response)
	}
	if response := client.command("QUIT"); response != "+OK goodbye" {
		t.Fatalf("QUIT = %q", response)
	}
	client.close()

	_, deletes := recording.snapshot()
	if want := [][]int64{{20}}; !reflect.DeepEqual(deletes, want) {
		t.Fatalf("delete calls = %v, want %v", deletes, want)
	}
}

func TestDisconnectDoesNotCommitDeletion(t *testing.T) {
	recording := &recordingStore{messages: []store.POP3Message{{ID: 10, Raw: []byte("A: b\r\n\r\na\r\n")}}}
	server := startTestServer(t, recording)
	client := dialPOP3(t, server.address)
	login(t, client, "keep@example.test")
	if response := client.command("DELE 1"); !strings.HasPrefix(response, "+OK") {
		t.Fatalf("DELE = %q", response)
	}
	client.close()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		_, deletes := recording.snapshot()
		if len(deletes) != 0 {
			t.Fatalf("disconnect committed deletion: %v", deletes)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRetrNormalizesLFAndDotStuffsOnWire(t *testing.T) {
	raw := []byte("Subject: LF only\n\nfirst\n.second\nlast")
	recording := &recordingStore{messages: []store.POP3Message{{ID: 5, Raw: raw}}}
	server := startTestServer(t, recording)
	conn, err := net.Dial("tcp", server.address)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)
	if _, err := reader.ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"USER lf@example.test\r\n", "PASS ignored\r\n"} {
		if _, err := conn.Write([]byte(command)); err != nil {
			t.Fatal(err)
		}
		if _, err := reader.ReadString('\n'); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := conn.Write([]byte("RETR 1\r\n")); err != nil {
		t.Fatal(err)
	}
	status, err := reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if status != fmt.Sprintf("+OK %d octets\r\n", len(raw)) {
		t.Fatalf("status = %q", status)
	}
	want := "Subject: LF only\r\n\r\nfirst\r\n..second\r\nlast\r\n.\r\n"
	var got strings.Builder
	for !strings.HasSuffix(got.String(), "\r\n.\r\n") {
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			t.Fatal(readErr)
		}
		got.WriteString(line)
	}
	if got.String() != want {
		t.Fatalf("wire data = %q, want %q", got.String(), want)
	}
}

func TestMalformedAndStateInvalidCommandsReturnERR(t *testing.T) {
	recording := &recordingStore{messages: []store.POP3Message{{ID: 1, Raw: []byte("A: b\r\n\r\nx\r\n")}}}
	server := startTestServer(t, recording)
	client := dialPOP3(t, server.address)
	defer client.close()

	for _, command := range []string{"STAT", "PASS password", "USER", "BOGUS"} {
		if response := client.command(command); !strings.HasPrefix(response, "-ERR") {
			t.Errorf("%q response = %q, want -ERR", command, response)
		}
	}
	login(t, client, "state@example.test")
	for _, command := range []string{"USER other@example.test", "PASS again", "STAT extra", "LIST x", "UIDL 0", "RETR 2", "TOP 1 -1", "TOP 1", "DELE nope", "RSET extra", "NOOP extra", "UNKNOWN"} {
		if response := client.command(command); !strings.HasPrefix(response, "-ERR") {
			t.Errorf("%q response = %q, want -ERR", command, response)
		}
	}
}

func TestStoreFailuresReturnERR(t *testing.T) {
	t.Run("open", func(t *testing.T) {
		recording := &recordingStore{openErr: errors.New("failed")}
		server := startTestServer(t, recording)
		client := dialPOP3(t, server.address)
		defer client.close()
		if response := client.command("USER box@example.test"); !strings.HasPrefix(response, "+OK") {
			t.Fatal(response)
		}
		if response := client.command("PASS password"); !strings.HasPrefix(response, "-ERR") {
			t.Fatalf("PASS response = %q", response)
		}
	})

	t.Run("delete", func(t *testing.T) {
		recording := &recordingStore{messages: []store.POP3Message{{ID: 8, Raw: []byte("A: b\r\n\r\nx\r\n")}}, deleteErr: errors.New("failed")}
		server := startTestServer(t, recording)
		client := dialPOP3(t, server.address)
		defer client.close()
		login(t, client, "box@example.test")
		client.command("DELE 1")
		if response := client.command("QUIT"); !strings.HasPrefix(response, "-ERR") {
			t.Fatalf("QUIT response = %q", response)
		}
	})
}
