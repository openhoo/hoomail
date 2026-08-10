package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/openhoo/hoomail/internal/pop3server"
	"github.com/openhoo/hoomail/internal/smtpserver"
)

func TestShutdownServicesStartsSMTPAndPOP3WhileHTTPIsBlocked(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	httpStarted := make(chan struct{})
	releaseHTTP := make(chan struct{})
	smtpStarted := make(chan struct{})
	pop3Started := make(chan struct{})

	shutdown := make(chan error, 1)
	go func() {
		shutdown <- shutdownServices(ctx,
			func(got context.Context) error {
				if got != ctx {
					t.Errorf("HTTP shutdown did not receive the shared context")
				}
				close(httpStarted)
				select {
				case <-releaseHTTP:
				case <-ctx.Done():
				}
				return nil
			},
			func(got context.Context) error {
				if got != ctx {
					t.Errorf("SMTP shutdown did not receive the shared context")
				}
				close(smtpStarted)
				return nil
			},
			func(got context.Context) error {
				if got != ctx {
					t.Errorf("POP3 shutdown did not receive the shared context")
				}
				close(pop3Started)
				return nil
			},
		)
	}()

	waitForShutdownSignal(t, httpStarted, "HTTP")
	waitForShutdownSignal(t, smtpStarted, "SMTP")
	waitForShutdownSignal(t, pop3Started, "POP3")

	close(releaseHTTP)
	select {
	case err := <-shutdown:
		if err != nil {
			t.Fatalf("shutdownServices() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdownServices() did not finish after HTTP was released")
	}
}

func TestShutdownServicesPrioritizesErrorsByServiceOrder(t *testing.T) {
	ctx := context.Background()
	httpErr := errors.New("HTTP failed")
	smtpErr := errors.New("SMTP failed")
	pop3Err := errors.New("POP3 failed")

	got := shutdownServices(ctx,
		func(context.Context) error { return httpErr },
		func(context.Context) error { return smtpErr },
		func(context.Context) error { return pop3Err },
	)
	if got == nil || got.Error() != "shutdown HTTP server: HTTP failed" {
		t.Fatalf("shutdownServices() error = %v, want HTTP error", got)
	}
}

func TestShutdownServicesIgnoresExpectedMailServerClose(t *testing.T) {
	got := shutdownServices(context.Background(),
		func(context.Context) error { return nil },
		func(context.Context) error { return smtpserver.ErrServerClosed },
		func(context.Context) error { return pop3server.ErrServerClosed },
	)
	if got != nil {
		t.Fatalf("shutdownServices() error = %v, want nil", got)
	}
}

func waitForShutdownSignal(t *testing.T, signal <-chan struct{}, service string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatalf("%s shutdown did not start while HTTP shutdown was blocked", service)
	}
}
