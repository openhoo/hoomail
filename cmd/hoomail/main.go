package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/openhoo/hoomail/internal/httpserver"
	"github.com/openhoo/hoomail/internal/sendtest"
	"github.com/openhoo/hoomail/internal/smtpserver"
	"github.com/openhoo/hoomail/internal/store"
	"github.com/openhoo/hoomail/internal/version"
	webassets "github.com/openhoo/hoomail/web"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "version" {
		fmt.Println(version.Value)
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "healthcheck" {
		if err := healthcheck(); err != nil {
			log.Fatal(err)
		}
		return
	}
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func healthcheck() error {
	port := environment("PORT", "3000")
	smtpPort := environment("HOOMAIL_SMTP_PORT", "2525")
	client := http.Client{Timeout: 2 * time.Second}
	response, err := client.Get("http://127.0.0.1:" + port + "/api/mailboxes")
	if err != nil {
		return fmt.Errorf("HTTP healthcheck: %w", err)
	}
	_, copyErr := io.Copy(io.Discard, response.Body)
	closeErr := response.Body.Close()
	if copyErr != nil {
		return fmt.Errorf("read HTTP healthcheck: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close HTTP healthcheck: %w", closeErr)
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP healthcheck returned %s", response.Status)
	}
	connection, err := net.DialTimeout("tcp", "127.0.0.1:"+smtpPort, 2*time.Second)
	if err != nil {
		return fmt.Errorf("SMTP healthcheck: %w", err)
	}
	return connection.Close()
}

func run() error {
	port := environment("PORT", "3000")
	smtpPort := environment("HOOMAIL_SMTP_PORT", "2525")
	databasePath := environment("HOOMAIL_DB_PATH", filepath.Join("data", "hoomail.db"))

	data, err := store.Open(databasePath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer data.Close()

	smtpAddress := ":" + smtpPort
	smtpService := smtpserver.New(data)
	handler := httpserver.New(data, httpserver.StaticConfig{FS: webassets.FS}, sendtest.Sender{Address: "127.0.0.1:" + smtpPort})
	httpService := &http.Server{
		Addr:              ":" + port,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errorsChannel := make(chan error, 2)
	go func() {
		log.Printf("[hoomail] SMTP server listening on port %s", smtpPort)
		errorsChannel <- smtpService.ListenAndServe(smtpAddress)
	}()
	go func() {
		log.Printf("[hoomail] HTTP server listening on port %s", port)
		errorsChannel <- httpService.ListenAndServe()
	}()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)

	select {
	case signal := <-signals:
		log.Printf("[hoomail] shutting down after %s", signal)
	case serveErr := <-errorsChannel:
		if !isExpectedClose(serveErr) {
			shutdown(httpService, smtpService)
			return serveErr
		}
	}

	return shutdown(httpService, smtpService)
}

func shutdown(httpService *http.Server, smtpService *smtpserver.Service) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	httpErr := httpService.Shutdown(ctx)
	smtpErr := smtpService.Shutdown(ctx)
	if httpErr != nil {
		return fmt.Errorf("shutdown HTTP server: %w", httpErr)
	}
	if smtpErr != nil && !isExpectedClose(smtpErr) {
		return fmt.Errorf("shutdown SMTP server: %w", smtpErr)
	}
	return nil
}

func environment(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func isExpectedClose(err error) bool {
	return err == nil || errors.Is(err, http.ErrServerClosed) || errors.Is(err, smtpserver.ErrServerClosed)
}
