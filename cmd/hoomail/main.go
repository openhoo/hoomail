package main

import (
	"bufio"
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
	"strings"
	"syscall"
	"time"

	"github.com/openhoo/hoomail/internal/httpserver"
	"github.com/openhoo/hoomail/internal/pop3server"
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
	pop3Port := environment("HOOMAIL_POP3_PORT", "3110")
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
	if err := connection.Close(); err != nil {
		return fmt.Errorf("close SMTP healthcheck: %w", err)
	}
	connection, err = net.DialTimeout("tcp", "127.0.0.1:"+pop3Port, 2*time.Second)
	if err != nil {
		return fmt.Errorf("POP3 healthcheck: %w", err)
	}
	defer connection.Close()
	if err := connection.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return fmt.Errorf("set POP3 healthcheck deadline: %w", err)
	}
	greeting, err := bufio.NewReader(connection).ReadString('\n')
	if err != nil {
		return fmt.Errorf("read POP3 healthcheck: %w", err)
	}
	if !strings.HasPrefix(greeting, "+OK") {
		return fmt.Errorf("POP3 healthcheck returned %q", strings.TrimSpace(greeting))
	}
	return nil
}

func run() error {
	port := environment("PORT", "3000")
	smtpPort := environment("HOOMAIL_SMTP_PORT", "2525")
	pop3Port := environment("HOOMAIL_POP3_PORT", "3110")
	databasePath := environment("HOOMAIL_DB_PATH", filepath.Join("data", "hoomail.db"))

	data, err := store.Open(databasePath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer data.Close()

	smtpAddress := ":" + smtpPort
	pop3Address := ":" + pop3Port
	smtpService := smtpserver.New(data)
	pop3Service := pop3server.New(data)
	handler := httpserver.New(data, httpserver.StaticConfig{FS: webassets.FS}, sendtest.Sender{Address: "127.0.0.1:" + smtpPort})
	httpService := &http.Server{
		Addr:              ":" + port,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	smtpListener, err := net.Listen("tcp", smtpAddress)
	if err != nil {
		return fmt.Errorf("listen SMTP: %w", err)
	}
	pop3Listener, err := net.Listen("tcp", pop3Address)
	if err != nil {
		_ = smtpListener.Close()
		return fmt.Errorf("listen POP3: %w", err)
	}
	httpListener, err := net.Listen("tcp", httpService.Addr)
	if err != nil {
		_ = smtpListener.Close()
		_ = pop3Listener.Close()
		return fmt.Errorf("listen HTTP: %w", err)
	}

	errorsChannel := make(chan error, 3)
	go func() {
		log.Printf("[hoomail] SMTP server listening on port %s", smtpPort)
		errorsChannel <- smtpService.Serve(smtpListener)
	}()
	go func() {
		log.Printf("[hoomail] POP3 server listening on port %s", pop3Port)
		errorsChannel <- pop3Service.Serve(pop3Listener)
	}()
	go func() {
		log.Printf("[hoomail] HTTP server listening on port %s", port)
		errorsChannel <- httpService.Serve(httpListener)
	}()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)

	select {
	case signal := <-signals:
		log.Printf("[hoomail] shutting down after %s", signal)
	case serveErr := <-errorsChannel:
		if !isExpectedClose(serveErr) {
			_ = shutdown(httpService, smtpService, pop3Service)
			return serveErr
		}
	}

	return shutdown(httpService, smtpService, pop3Service)
}

func shutdown(httpService *http.Server, smtpService *smtpserver.Service, pop3Service *pop3server.Service) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	httpErr := httpService.Shutdown(ctx)
	smtpErr := smtpService.Shutdown(ctx)
	pop3Err := pop3Service.Shutdown(ctx)
	if httpErr != nil {
		return fmt.Errorf("shutdown HTTP server: %w", httpErr)
	}
	if smtpErr != nil && !isExpectedClose(smtpErr) {
		return fmt.Errorf("shutdown SMTP server: %w", smtpErr)
	}
	if pop3Err != nil && !isExpectedClose(pop3Err) {
		return fmt.Errorf("shutdown POP3 server: %w", pop3Err)
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
	return err == nil || errors.Is(err, http.ErrServerClosed) || errors.Is(err, smtpserver.ErrServerClosed) || errors.Is(err, pop3server.ErrServerClosed)
}
