package smtpserver

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-smtp"
	"github.com/openhoo/hoomail/internal/store"
)

const MaxMessageBytes int64 = 25 * 1024 * 1024

// MaxRecipients caps RCPT TO commands per transaction at RFC 5321's required
// minimum capacity so a single session cannot accumulate unbounded recipients
// and storage fan-out.
const MaxRecipients = 100

const (
	readTimeout         = 60 * time.Second
	writeTimeout        = 60 * time.Second
	storeMessageTimeout = 30 * time.Second
)

var ErrServerClosed = smtp.ErrServerClosed

// Store is the narrow persistence contract required by the SMTP receiver.
type Store interface {
	StoreMessage(context.Context, store.StoreMessageInput) ([]store.StoredMessage, error)
}

type Service struct {
	server *smtp.Server
	store  Store

	mu         sync.Mutex
	listener   net.Listener
	closing    bool
	baseCtx    context.Context
	baseCancel context.CancelFunc
}

func New(messageStore Store) *Service {
	ctx, cancel := context.WithCancel(context.Background())
	service := &Service{store: messageStore, baseCtx: ctx, baseCancel: cancel}
	server := smtp.NewServer(service)
	server.Domain = "localhost"
	server.MaxMessageBytes = MaxMessageBytes
	server.MaxRecipients = MaxRecipients
	server.ReadTimeout = readTimeout
	server.WriteTimeout = writeTimeout
	service.server = server
	return service
}

func (service *Service) Serve(listener net.Listener) error {
	if listener == nil {
		return errors.New("smtpserver: nil listener")
	}
	service.mu.Lock()

	if service.closing {
		service.mu.Unlock()
		_ = listener.Close()
		return ErrServerClosed
	}
	if service.listener != nil {
		service.mu.Unlock()
		return errors.New("smtpserver: already serving")
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

	return service.server.Serve(listener)
}

func (service *Service) Shutdown(ctx context.Context) error {
	listener := service.enterTerminalState()
	err := service.server.Shutdown(ctx)
	if listener != nil {
		_ = listener.Close()
	}
	return err
}

func (service *Service) Close() error {
	listener := service.enterTerminalState()
	err := service.server.Close()
	if listener != nil {
		_ = listener.Close()
	}
	return err
}

func (service *Service) enterTerminalState() net.Listener {
	service.mu.Lock()
	listener := service.listener
	first := !service.closing
	service.closing = true
	service.mu.Unlock()
	if first {
		service.baseCancel()
	}
	return listener
}

func (service *Service) NewSession(*smtp.Conn) (smtp.Session, error) {
	if service.store == nil {
		return nil, errors.New("smtpserver: nil store")
	}
	ctx, cancel := context.WithCancel(service.baseCtx)
	return &session{store: service.store, ctx: ctx, cancel: cancel}, nil
}

type session struct {
	store      Store
	ctx        context.Context
	cancel     context.CancelFunc
	mailFrom   string
	recipients []string
}

func (session *session) Mail(from string, _ *smtp.MailOptions) error {
	session.mailFrom = from
	session.recipients = session.recipients[:0]
	return nil
}

func (session *session) Rcpt(to string, _ *smtp.RcptOptions) error {
	session.recipients = append(session.recipients, to)
	return nil
}

func (session *session) Data(reader io.Reader) error {
	var raw bytes.Buffer
	limited := io.LimitReader(reader, MaxMessageBytes+1)
	if _, err := raw.ReadFrom(limited); err != nil {
		if errors.Is(err, smtp.ErrDataTooLarge) {
			return smtp.ErrDataTooLarge
		}
		return err
	}
	if int64(raw.Len()) > MaxMessageBytes {
		return smtp.ErrDataTooLarge
	}

	input, err := Parse(raw.Bytes(), session.mailFrom, session.recipients)
	if err != nil {
		return errors.New("message processing failed")
	}

	ctx, cancel := context.WithTimeout(session.ctx, storeMessageTimeout)
	defer cancel()
	if _, err := session.store.StoreMessage(ctx, input); err != nil {
		log.Printf("smtpserver: storing message failed: %v", err)
		return &smtp.SMTPError{
			Code:         451,
			EnhancedCode: smtp.EnhancedCode{4, 3, 0},
			Message:      "temporary local failure, please retry",
		}
	}
	return nil
}

func (session *session) Reset() {
	session.mailFrom = ""
	session.recipients = session.recipients[:0]
}

func (session *session) Logout() error {
	session.Reset()
	session.cancel()
	return nil
}

func normalizedRecipients(envelope []string, fallback []store.AddressEntry) []string {
	addresses := envelope
	if len(addresses) == 0 {
		addresses = make([]string, 0, len(fallback))
		for _, address := range fallback {
			addresses = append(addresses, address.Address)
		}
	}

	seen := make(map[string]struct{}, len(addresses))
	out := make([]string, 0, len(addresses))
	for _, address := range addresses {
		address = strings.ToLower(strings.TrimSpace(address))
		if address == "" {
			continue
		}
		if _, duplicate := seen[address]; duplicate {
			continue
		}
		seen[address] = struct{}{}
		out = append(out, address)
	}
	return out
}
