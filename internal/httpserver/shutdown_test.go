package httpserver

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/openhoo/hoomail/internal/events"
)

// TestShutdownDrainsActiveSSESubscriber mirrors the wiring in cmd/hoomail:
// an http.Server registers the events-hub close as an OnShutdown hook before
// Serve, so Shutdown drains an open /api/events connection quickly instead of
// waiting for the context deadline.
func TestShutdownDrainsActiveSSESubscriber(t *testing.T) {
	hub := events.NewHub()
	handler := &server{store: testStore(t), subscribe: hub.Subscribe}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	httpService := &http.Server{Handler: handler}
	httpService.RegisterOnShutdown(hub.Close)
	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- httpService.Serve(listener)
	}()

	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+listener.Addr().String()+"/api/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	hello := make([]byte, len("data: {\"type\":\"connected\"}\n\n"))
	if _, err := io.ReadFull(response.Body, hello); err != nil {
		t.Fatal(err)
	}
	streamEnded := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(io.Discard, response.Body)
		streamEnded <- copyErr
	}()

	shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	started := time.Now()
	if err := httpService.Shutdown(shutdownContext); err != nil {
		t.Fatalf("shutdown error=%v elapsed=%s", err, time.Since(started))
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("shutdown took %s; SSE subscriber did not drain", elapsed)
	}
	select {
	case copyErr := <-streamEnded:
		if copyErr != nil {
			t.Fatalf("SSE stream ended with error: %v", copyErr)
		}
	case <-time.After(time.Second):
		t.Fatal("SSE stream did not end after shutdown")
	}
	select {
	case serveErr := <-serveErrors:
		if !errors.Is(serveErr, http.ErrServerClosed) {
			t.Fatalf("serve error=%v", serveErr)
		}
	case <-time.After(time.Second):
		t.Fatal("Serve did not return after Shutdown")
	}
}
