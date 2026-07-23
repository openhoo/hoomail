package pop3server

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"

	"github.com/openhoo/hoomail/internal/store"
)

const maxCommandBytes = 4096

var ErrServerClosed = errors.New("pop3server: server closed")

// Store is the narrow persistence contract required by the POP3 server.
type Store interface {
	OpenPOP3Mailbox(context.Context, string) ([]store.POP3Message, error)
	DeleteMessages(context.Context, []int64) ([]int64, error)
}

type Service struct {
	store Store

	mu       sync.Mutex
	listener net.Listener
	conns    map[net.Conn]struct{}
	closing  bool
	wg       sync.WaitGroup
}

func New(messageStore Store) *Service {
	return &Service{store: messageStore, conns: make(map[net.Conn]struct{})}
}

func (service *Service) Serve(listener net.Listener) error {
	if listener == nil {
		return errors.New("pop3server: nil listener")
	}

	service.mu.Lock()
	if service.closing {
		service.mu.Unlock()
		return ErrServerClosed
	}
	if service.listener != nil {
		service.mu.Unlock()
		return errors.New("pop3server: already serving")
	}
	service.listener = listener
	service.mu.Unlock()

	defer func() {
		service.mu.Lock()
		if service.listener == listener {
			service.listener = nil
		}
		service.mu.Unlock()
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			service.mu.Lock()
			closed := service.closing
			service.mu.Unlock()
			if closed || errors.Is(err, net.ErrClosed) {
				return ErrServerClosed
			}
			return err
		}

		service.mu.Lock()
		if service.closing {
			service.mu.Unlock()
			_ = conn.Close()
			return ErrServerClosed
		}
		service.conns[conn] = struct{}{}
		service.wg.Add(1)
		service.mu.Unlock()

		go service.serveConn(conn)
	}
}

func (service *Service) Shutdown(ctx context.Context) error {
	service.mu.Lock()
	service.closing = true
	listener := service.listener
	connections := make([]net.Conn, 0, len(service.conns))
	for conn := range service.conns {
		connections = append(connections, conn)
	}
	service.mu.Unlock()

	if listener != nil {
		_ = listener.Close()
	}
	for _, conn := range connections {
		_ = conn.Close()
	}

	done := make(chan struct{})
	go func() {
		service.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (service *Service) serveConn(conn net.Conn) {
	defer func() {
		_ = conn.Close()
		service.mu.Lock()
		delete(service.conns, conn)
		service.mu.Unlock()
		service.wg.Done()
	}()

	writer := bufio.NewWriter(conn)
	if service.store == nil {
		_ = writeLine(writer, "-ERR server unavailable")
		_ = writer.Flush()
		return
	}
	if err := writeLine(writer, "+OK Hoomail POP3 ready"); err != nil || writer.Flush() != nil {
		return
	}

	session := &session{store: service.store}
	reader := bufio.NewReaderSize(conn, maxCommandBytes)
	for {
		line, err := readCommand(reader)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				_ = writeLine(writer, "-ERR malformed command")
				_ = writer.Flush()
			}
			return
		}
		closeConnection := session.execute(line, writer)
		if writer.Flush() != nil || closeConnection {
			return
		}
	}
}

func readCommand(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	if len(line) > maxCommandBytes {
		return "", errors.New("command too long")
	}
	line = strings.TrimSuffix(line, "\n")
	line = strings.TrimSuffix(line, "\r")
	if strings.IndexByte(line, 0) >= 0 {
		return "", errors.New("command contains NUL")
	}
	return line, nil
}

type state uint8

const (
	stateAuthorization state = iota
	stateTransaction
)

type session struct {
	store    Store
	state    state
	user     string
	messages []store.POP3Message
	deleted  map[int]bool
}

func (session *session) execute(line string, writer *bufio.Writer) bool {
	command, argument, hasArgument := parseCommand(line)
	if command == "" {
		_ = writeLine(writer, "-ERR malformed command")
		return false
	}

	switch command {
	case "CAPA":
		if hasArgument {
			return session.badSyntax(writer)
		}
		_ = writeLine(writer, "+OK Capability list follows")
		_ = writeMultiline(writer, []byte("USER\r\nUIDL\r\nTOP\r\n"))
	case "QUIT":
		if hasArgument {
			return session.badSyntax(writer)
		}
		if session.state == stateTransaction {
			ids := session.deletedIDs()
			if len(ids) > 0 {
				if _, err := session.store.DeleteMessages(context.Background(), ids); err != nil {
					_ = writeLine(writer, "-ERR unable to delete messages")
					return true
				}
			}
		}
		_ = writeLine(writer, "+OK goodbye")
		return true
	case "USER":
		if session.state != stateAuthorization {
			return session.wrongState(writer)
		}
		if !hasArgument || strings.TrimSpace(argument) == "" || strings.ContainsAny(argument, " \t") {
			return session.badSyntax(writer)
		}
		session.user = argument
		_ = writeLine(writer, "+OK user accepted")
	case "PASS":
		if session.state != stateAuthorization {
			return session.wrongState(writer)
		}
		if session.user == "" || !hasArgument {
			return session.badSyntax(writer)
		}
		messages, err := session.store.OpenPOP3Mailbox(context.Background(), session.user)
		if err != nil {
			_ = writeLine(writer, "-ERR unable to open mailbox")
			return false
		}
		session.messages = cloneMessages(messages)
		session.deleted = make(map[int]bool)
		session.state = stateTransaction
		_ = writeLine(writer, fmt.Sprintf("+OK mailbox locked and ready, %d messages", len(messages)))
	default:
		if session.state != stateTransaction {
			return session.wrongState(writer)
		}
		session.executeTransaction(command, argument, hasArgument, writer)
	}
	return false
}

func (session *session) executeTransaction(command, argument string, hasArgument bool, writer *bufio.Writer) {
	switch command {
	case "STAT":
		if hasArgument {
			session.badSyntax(writer)
			return
		}
		count, octets := session.maildropStats()
		_ = writeLine(writer, fmt.Sprintf("+OK %d %d", count, octets))
	case "LIST":
		session.list(argument, hasArgument, writer)
	case "UIDL":
		session.uidl(argument, hasArgument, writer)
	case "RETR":
		number, ok := session.messageNumber(argument, hasArgument)
		if !ok {
			_ = writeLine(writer, "-ERR no such message")
			return
		}
		message := session.messages[number-1]
		_ = writeLine(writer, fmt.Sprintf("+OK %d octets", len(message.Raw)))
		_ = writeMultiline(writer, message.Raw)
	case "TOP":
		fields := strings.Fields(argument)
		if !hasArgument || len(fields) != 2 {
			session.badSyntax(writer)
			return
		}
		number, errNumber := strconv.Atoi(fields[0])
		lineCount, errLines := strconv.Atoi(fields[1])
		if errNumber != nil || errLines != nil || lineCount < 0 || !session.available(number) {
			_ = writeLine(writer, "-ERR no such message or invalid line count")
			return
		}
		_ = writeLine(writer, "+OK top of message follows")
		_ = writeMultiline(writer, topBytes(session.messages[number-1].Raw, lineCount))
	case "DELE":
		number, ok := session.messageNumber(argument, hasArgument)
		if !ok {
			_ = writeLine(writer, "-ERR no such message")
			return
		}
		session.deleted[number] = true
		_ = writeLine(writer, "+OK message deleted")
	case "RSET":
		if hasArgument {
			session.badSyntax(writer)
			return
		}
		clear(session.deleted)
		count, octets := session.maildropStats()
		_ = writeLine(writer, fmt.Sprintf("+OK %d messages (%d octets)", count, octets))
	case "NOOP":
		if hasArgument {
			session.badSyntax(writer)
			return
		}
		_ = writeLine(writer, "+OK")
	default:
		_ = writeLine(writer, "-ERR unknown command")
	}
}

func parseCommand(line string) (command, argument string, hasArgument bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", "", false
	}
	index := strings.IndexAny(line, " \t")
	if index < 0 {
		return strings.ToUpper(line), "", false
	}
	command = strings.ToUpper(line[:index])
	argument = strings.TrimSpace(line[index:])
	return command, argument, argument != ""
}

func (session *session) list(argument string, hasArgument bool, writer *bufio.Writer) {
	if hasArgument {
		number, ok := session.messageNumber(argument, true)
		if !ok {
			_ = writeLine(writer, "-ERR no such message")
			return
		}
		_ = writeLine(writer, fmt.Sprintf("+OK %d %d", number, len(session.messages[number-1].Raw)))
		return
	}
	count, octets := session.maildropStats()
	_ = writeLine(writer, fmt.Sprintf("+OK %d messages (%d octets)", count, octets))
	var data bytes.Buffer
	for index, message := range session.messages {
		number := index + 1
		if !session.deleted[number] {
			fmt.Fprintf(&data, "%d %d\r\n", number, len(message.Raw))
		}
	}
	_ = writeMultiline(writer, data.Bytes())
}

func (session *session) uidl(argument string, hasArgument bool, writer *bufio.Writer) {
	if hasArgument {
		number, ok := session.messageNumber(argument, true)
		if !ok {
			_ = writeLine(writer, "-ERR no such message")
			return
		}
		_ = writeLine(writer, fmt.Sprintf("+OK %d %d", number, session.messages[number-1].ID))
		return
	}
	_ = writeLine(writer, "+OK unique-id listing follows")
	var data bytes.Buffer
	for index, message := range session.messages {
		number := index + 1
		if !session.deleted[number] {
			fmt.Fprintf(&data, "%d %d\r\n", number, message.ID)
		}
	}
	_ = writeMultiline(writer, data.Bytes())
}

func (session *session) messageNumber(argument string, hasArgument bool) (int, bool) {
	if !hasArgument || strings.ContainsAny(argument, " \t") {
		return 0, false
	}
	number, err := strconv.Atoi(argument)
	return number, err == nil && session.available(number)
}

func (session *session) available(number int) bool {
	return number >= 1 && number <= len(session.messages) && !session.deleted[number]
}

func (session *session) maildropStats() (count, octets int) {
	for index, message := range session.messages {
		if !session.deleted[index+1] {
			count++
			octets += len(message.Raw)
		}
	}
	return count, octets
}

func (session *session) deletedIDs() []int64 {
	ids := make([]int64, 0, len(session.deleted))
	for index, message := range session.messages {
		if session.deleted[index+1] {
			ids = append(ids, message.ID)
		}
	}
	return ids
}

func (session *session) badSyntax(writer *bufio.Writer) bool {
	_ = writeLine(writer, "-ERR malformed command")
	return false
}

func (session *session) wrongState(writer *bufio.Writer) bool {
	_ = writeLine(writer, "-ERR command not valid in this state")
	return false
}

func cloneMessages(messages []store.POP3Message) []store.POP3Message {
	cloned := make([]store.POP3Message, len(messages))
	for index, message := range messages {
		cloned[index] = store.POP3Message{ID: message.ID, Raw: bytes.Clone(message.Raw)}
	}
	return cloned
}

func topBytes(raw []byte, bodyLines int) []byte {
	headerEnd, separatorLength := headerBoundary(raw)
	if headerEnd < 0 {
		return raw
	}
	result := append([]byte(nil), raw[:headerEnd+separatorLength]...)
	body := raw[headerEnd+separatorLength:]
	position := 0
	for line := 0; line < bodyLines && position < len(body); line++ {
		next := bytes.IndexByte(body[position:], '\n')
		if next < 0 {
			result = append(result, body[position:]...)
			position = len(body)
		} else {
			next += position + 1
			result = append(result, body[position:next]...)
			position = next
		}
	}
	return result
}

func headerBoundary(raw []byte) (int, int) {
	if index := bytes.Index(raw, []byte("\r\n\r\n")); index >= 0 {
		return index, 4
	}
	if index := bytes.Index(raw, []byte("\n\n")); index >= 0 {
		return index, 2
	}
	return -1, 0
}

func writeLine(writer *bufio.Writer, line string) error {
	_, err := writer.WriteString(line + "\r\n")
	return err
}

func writeMultiline(writer *bufio.Writer, data []byte) error {
	lineStart := true
	for index := 0; index < len(data); index++ {
		value := data[index]
		if lineStart && value == '.' {
			if err := writer.WriteByte('.'); err != nil {
				return err
			}
		}
		if value == '\r' {
			if index+1 < len(data) && data[index+1] == '\n' {
				index++
			}
			if _, err := writer.WriteString("\r\n"); err != nil {
				return err
			}
			lineStart = true
			continue
		}
		if value == '\n' {
			if _, err := writer.WriteString("\r\n"); err != nil {
				return err
			}
			lineStart = true
			continue
		}
		if err := writer.WriteByte(value); err != nil {
			return err
		}
		lineStart = false
	}
	if !lineStart {
		if _, err := writer.WriteString("\r\n"); err != nil {
			return err
		}
	}
	_, err := writer.WriteString(".\r\n")
	return err
}
