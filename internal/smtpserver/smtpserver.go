package smtpserver

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"

	"github.com/emersion/go-smtp"
	"github.com/openhoo/hoomail/internal/store"
)

const MaxMessageBytes int64 = 25 * 1024 * 1024

var ErrServerClosed = smtp.ErrServerClosed

// Store is the narrow persistence contract required by the SMTP receiver.
type Store interface {
	StoreMessage(context.Context, store.StoreMessageInput) ([]store.StoredMessage, error)
}

type Service struct {
	server *smtp.Server
	store  Store

	mu       sync.Mutex
	listener net.Listener
}

func New(messageStore Store) *Service {
	service := &Service{store: messageStore}
	server := smtp.NewServer(service)
	server.Domain = "localhost"
	server.MaxMessageBytes = MaxMessageBytes
	service.server = server
	return service
}

func (service *Service) Server() *smtp.Server { return service.server }

func (service *Service) ListenAndServe(address string) error {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	return service.Serve(listener)
}

func (service *Service) Serve(listener net.Listener) error {
	service.mu.Lock()
	if service.listener != nil {
		service.mu.Unlock()
		return errors.New("smtpserver: already serving")
	}
	service.listener = listener
	service.mu.Unlock()

	err := service.server.Serve(listener)
	service.mu.Lock()
	if service.listener == listener {
		service.listener = nil
	}
	service.mu.Unlock()
	return err
}

func (service *Service) Addr() net.Addr {
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.listener == nil {
		return nil
	}
	return service.listener.Addr()
}

func (service *Service) Shutdown(ctx context.Context) error { return service.server.Shutdown(ctx) }
func (service *Service) Close() error                       { return service.server.Close() }

func (service *Service) NewSession(*smtp.Conn) (smtp.Session, error) {
	if service.store == nil {
		return nil, errors.New("smtpserver: nil store")
	}
	return &session{store: service.store}, nil
}

type session struct {
	store      Store
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
	if _, err := session.store.StoreMessage(context.Background(), input); err != nil {
		return errors.New("message processing failed")
	}
	return nil
}

func (session *session) Reset() {
	session.mailFrom = ""
	session.recipients = session.recipients[:0]
}

func (session *session) Logout() error {
	session.Reset()
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
