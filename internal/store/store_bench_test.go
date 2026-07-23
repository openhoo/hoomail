package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/openhoo/hoomail/internal/events"
)

var (
	benchmarkMessageList   []MessageListItem
	benchmarkStoredMessage []StoredMessage
)

func BenchmarkListMessages1000(b *testing.B) {
	ctx := context.Background()
	store, err := Open(filepath.Join(b.TempDir(), "hoomail.db"), WithBroadcaster(func(events.Event) {}))
	if err != nil {
		b.Fatalf("open store: %v", err)
	}
	b.Cleanup(func() { _ = store.Close() })

	result, err := store.db.ExecContext(ctx, `INSERT INTO mailboxes(address,created_at,last_message_at) VALUES(?,?,?)`, "benchmark@example.com", int64(1), int64(1000))
	if err != nil {
		b.Fatalf("insert mailbox: %v", err)
	}
	mailboxID, err := result.LastInsertId()
	if err != nil {
		b.Fatalf("mailbox id: %v", err)
	}

	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		b.Fatalf("begin seed transaction: %v", err)
	}
	statement, err := tx.PrepareContext(ctx, `INSERT INTO messages(mailbox_id,from_address,from_name,to_json,cc_json,subject,html,text,headers_json,size,is_read,received_at,raw) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		b.Fatalf("prepare seed statement: %v", err)
	}
	for index := range 1000 {
		subject := fmt.Sprintf("Routine notification %04d", index)
		if index%10 == 0 {
			subject = fmt.Sprintf("Quarterly benchmark needle %04d", index)
		}
		text := "This is a representative email body with enough content to exercise snippet normalization and truncation. " + subject
		if _, err = statement.ExecContext(ctx, mailboxID, "sender@example.com", "Benchmark Sender", `[{"address":"benchmark@example.com"}]`, `[]`, subject, nil, text, `{"content-type":"text/plain; charset=utf-8"}`, len(text), index%2, index+1, []byte(text)); err != nil {
			_ = statement.Close()
			_ = tx.Rollback()
			b.Fatalf("seed message %d: %v", index, err)
		}
	}
	if err = statement.Close(); err != nil {
		_ = tx.Rollback()
		b.Fatalf("close seed statement: %v", err)
	}
	if err = tx.Commit(); err != nil {
		b.Fatalf("commit seed transaction: %v", err)
	}

	for _, benchmark := range []struct {
		name  string
		query string
		want  int
	}{
		{name: "EmptyQuery", query: "", want: 1000},
		{name: "FilteredQuery", query: "benchmark needle", want: 100},
	} {
		b.Run(benchmark.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				messages, err := store.ListMessages(ctx, mailboxID, benchmark.query)
				if err != nil {
					b.Fatal(err)
				}
				if len(messages) != benchmark.want {
					b.Fatalf("got %d messages, want %d", len(messages), benchmark.want)
				}
				benchmarkMessageList = messages
			}
		})
	}
}

func BenchmarkStoreMessageExistingMailbox(b *testing.B) {
	ctx := context.Background()
	fixedTime := time.UnixMilli(1_750_000_000_000)
	store, err := Open(
		filepath.Join(b.TempDir(), "hoomail.db"),
		WithClock(func() time.Time { return fixedTime }),
		WithBroadcaster(func(events.Event) {}),
	)
	if err != nil {
		b.Fatalf("open store: %v", err)
	}
	b.Cleanup(func() { _ = store.Close() })

	if _, err = store.db.ExecContext(ctx, `INSERT INTO mailboxes(address,created_at,last_message_at) VALUES(?,?,NULL)`, "existing@example.com", fixedTime.UnixMilli()); err != nil {
		b.Fatalf("insert mailbox: %v", err)
	}

	fromName := "Hoomail Benchmark"
	fromAddress := "sender@example.org"
	subject := "Your monthly account statement is ready"
	html := `<html><body><h1>Account statement</h1><p>Your monthly statement is ready for review.</p><a href="https://example.org/statements/2026-07">View statement</a></body></html>`
	text := "Account statement\n\nYour monthly statement is ready for review.\nhttps://example.org/statements/2026-07"
	filename := "statement.pdf"
	contentType := "application/pdf"
	attachment := []byte("%PDF-1.7\nrepresentative benchmark attachment content\n%%EOF")
	raw := []byte("From: Hoomail Benchmark <sender@example.org>\r\nTo: Existing Recipient <existing@example.com>\r\nSubject: Your monthly account statement is ready\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=benchmark\r\n\r\n--benchmark\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" + text + "\r\n--benchmark--\r\n")
	input := StoreMessageInput{
		Recipients:  []string{"existing@example.com"},
		FromAddress: &fromAddress,
		FromName:    &fromName,
		To:          []AddressEntry{{Address: "existing@example.com"}},
		CC:          []AddressEntry{{Address: "accounts@example.org"}},
		Subject:     &subject,
		HTML:        &html,
		Text:        &text,
		Headers: map[string]string{
			"content-type": "multipart/mixed; boundary=benchmark",
			"message-id":   "<benchmark-message@example.org>",
		},
		Size: int64(len(raw) + len(attachment)),
		Attachments: []AttachmentInput{{
			Filename:    &filename,
			ContentType: &contentType,
			Content:     attachment,
		}},
		Raw: raw,
	}

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		stored, err := store.StoreMessage(ctx, input)
		if err != nil {
			b.Fatal(err)
		}
		if len(stored) != 1 {
			b.Fatalf("got %d stored messages, want 1", len(stored))
		}
		benchmarkStoredMessage = stored

		b.StopTimer()
		if _, err = store.db.ExecContext(ctx, `DELETE FROM messages WHERE id=?`, stored[0].MessageID); err != nil {
			b.Fatalf("delete benchmark message: %v", err)
		}
		b.StartTimer()
	}
}
