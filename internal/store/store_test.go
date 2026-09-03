package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openhoo/hoomail/internal/calendar"
	"github.com/openhoo/hoomail/internal/events"
	_ "modernc.org/sqlite"
)

func openTestStore(t *testing.T, options ...Option) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "hoomail.db"), options...)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestOpenMigratesLegacyMessagesColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE messages (id INTEGER PRIMARY KEY AUTOINCREMENT, mailbox_id INTEGER NOT NULL, from_address TEXT, from_name TEXT, to_json TEXT NOT NULL DEFAULT '[]', cc_json TEXT NOT NULL DEFAULT '[]', subject TEXT, html TEXT, text TEXT, headers_json TEXT NOT NULL DEFAULT '{}', size INTEGER NOT NULL DEFAULT 0, is_read INTEGER NOT NULL DEFAULT 0, received_at INTEGER NOT NULL);`)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	columns, err := store.tableColumns(context.Background(), "messages")
	if err != nil {
		t.Fatal(err)
	}
	if !columns["ical_json"] || !columns["raw"] || !columns["snippet"] {
		t.Fatalf("missing migrations: %#v", columns)
	}
}

func TestStoreMessageStoresEnvelopeRecipientsSeparatelyAndEmitsAfterCommit(t *testing.T) {
	var emitted []events.Event
	store := openTestStore(t, WithClock(func() time.Time { return time.UnixMilli(1234) }), WithBroadcaster(func(event events.Event) {
		var count int
		if err := storeQueryCount(storePlaceholder, &count); err != nil {
			panic(err)
		}
		emitted = append(emitted, event)
	}))
	storePlaceholder = store
	subject := "subject"
	content := []byte("hello")
	stored, err := store.StoreMessage(context.Background(), StoreMessageInput{Recipients: []string{" First@Example.com ", "second@example.com"}, Subject: &subject, Headers: map[string]string{"x": "y"}, Attachments: []AttachmentInput{{Content: content}}, Raw: []byte("raw")})
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 2 {
		t.Fatalf("stored=%v", stored)
	}
	mailboxes, err := store.ListMailboxes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(mailboxes) != 2 {
		t.Fatalf("mailboxes=%v", mailboxes)
	}
	if len(emitted) != 4 || emitted[0].Type != events.TypeMailboxNew || emitted[1].Type != events.TypeMessageNew || emitted[2].Type != events.TypeMailboxNew || emitted[3].Type != events.TypeMessageNew {
		t.Fatalf("events=%v", emitted)
	}
}

var storePlaceholder *Store

func storeQueryCount(store *Store, count *int) error {
	return store.db.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(count)
}

func TestListMessagesEscapesLikeWildcards(t *testing.T) {
	store := openTestStore(t, WithBroadcaster(func(events.Event) {}))
	ctx := context.Background()
	for _, subject := range []string{"literal 100%_\\ match", "literal 100XX match"} {
		if _, err := store.StoreMessage(ctx, StoreMessageInput{Recipients: []string{"box@example.com"}, Subject: &subject, Headers: map[string]string{}}); err != nil {
			t.Fatal(err)
		}
	}
	mailboxes, _ := store.ListMailboxes(ctx)
	rows, err := store.ListMessages(ctx, mailboxes[0].ID, `100%_\`)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || *rows[0].Subject != "literal 100%_\\ match" {
		t.Fatalf("rows=%v", rows)
	}
}

func TestNormalizeSnippetCollapsesWhitespaceAndTruncatesUnicode(t *testing.T) {
	if got := normalizeSnippet("  first\n\tsecond   third  ", 140); got != "first second third" {
		t.Fatalf("normalized snippet = %q", got)
	}
	if got := normalizeSnippet("äöü owl", 4); got != "äöü " {
		t.Fatalf("unicode snippet = %q", got)
	}
}

func TestOpenPOP3MailboxCreatesMissingInboxAndReturnsRawMessages(t *testing.T) {
	var emitted []events.Event
	store := openTestStore(t, WithClock(func() time.Time { return time.UnixMilli(1234) }), WithBroadcaster(func(event events.Event) {
		emitted = append(emitted, event)
	}))
	ctx := context.Background()
	snapshot, err := store.OpenPOP3Mailbox(ctx, " New@Example.com ")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Messages) != 0 {
		t.Fatalf("messages=%v", snapshot.Messages)
	}
	mailboxes, err := store.ListMailboxes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(mailboxes) != 1 || mailboxes[0].Address != "new@example.com" || mailboxes[0].LastMessageAt != nil {
		t.Fatalf("mailboxes=%+v", mailboxes)
	}
	if len(emitted) != 1 || emitted[0].Type != events.TypeMailboxNew {
		t.Fatalf("events=%v", emitted)
	}
	raw := []byte("Subject: stored\r\n\r\nbody\r\n")
	if _, err := store.StoreMessage(ctx, StoreMessageInput{Recipients: []string{"new@example.com"}, Headers: map[string]string{}, Raw: raw}); err != nil {
		t.Fatal(err)
	}
	snapshot, err = store.OpenPOP3Mailbox(ctx, "NEW@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Messages) != 1 || string(snapshot.Messages[0].Raw) != string(raw) {
		t.Fatalf("messages=%v", snapshot.Messages)
	}
	if len(emitted) != 2 || emitted[1].Type != events.TypeMessageNew {
		t.Fatalf("duplicate mailbox event: %v", emitted)
	}
}

func TestBroadcasterPanicDoesNotBreakStoreOrPOP3Lock(t *testing.T) {
	var calls int
	store := openTestStore(t, WithBroadcaster(func(events.Event) {
		calls++
		if calls == 1 {
			panic("broadcaster failure")
		}
	}))
	ctx := context.Background()
	snapshot, err := store.OpenPOP3Mailbox(ctx, "panic@example.com")
	if err != nil {
		t.Fatalf("OpenPOP3Mailbox: %v", err)
	}
	if len(snapshot.Messages) != 0 {
		t.Fatalf("new mailbox messages=%v", snapshot.Messages)
	}
	mailboxes, err := store.ListMailboxes(ctx)
	if err != nil {
		t.Fatalf("ListMailboxes after broadcaster panic: %v", err)
	}
	if len(mailboxes) != 1 || mailboxes[0].Address != "panic@example.com" {
		t.Fatalf("mailboxes after broadcaster panic=%v", mailboxes)
	}
	stored, err := store.StoreMessage(ctx, StoreMessageInput{
		Recipients: []string{"panic@example.com"},
		Headers:    map[string]string{},
	})
	if err != nil {
		t.Fatalf("StoreMessage after broadcaster panic: %v", err)
	}
	var count int
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 || len(stored) != 1 {
		t.Fatalf("stored=%v message count=%d", stored, count)
	}
	if err := store.WipeAll(ctx); err != nil {
		t.Fatalf("WipeAll after broadcaster panic: %v", err)
	}
	if calls != 3 {
		t.Fatalf("broadcaster calls=%d, want 3", calls)
	}
}

func TestStoreMessageKeepsMailboxLastMessageAtMonotoneAcrossClockChanges(t *testing.T) {
	var clockNow int64 = 1000
	store := openTestStore(t, WithClock(func() time.Time { return time.UnixMilli(clockNow) }), WithBroadcaster(func(events.Event) {}))
	ctx := context.Background()
	if _, err := store.OpenPOP3Mailbox(ctx, "box@example.com"); err != nil {
		t.Fatal(err)
	}
	storeAtClock := func(want int64) {
		t.Helper()
		if _, err := store.StoreMessage(ctx, StoreMessageInput{Recipients: []string{"box@example.com"}, Headers: map[string]string{}}); err != nil {
			t.Fatal(err)
		}
		mailboxes, err := store.ListMailboxes(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(mailboxes) != 1 || mailboxes[0].LastMessageAt == nil || *mailboxes[0].LastMessageAt != want {
			t.Fatalf("mailboxes=%+v, want last_message_at=%d", mailboxes, want)
		}
	}

	storeAtClock(1000)
	clockNow = 2000
	storeAtClock(2000)
	clockNow = 1500
	storeAtClock(2000)
	clockNow = 2000
	storeAtClock(2000)
}

func TestDeleteCascadeAndWipeResetSequences(t *testing.T) {
	store := openTestStore(t, WithBroadcaster(func(events.Event) {}))
	ctx := context.Background()
	subject := "one"
	stored, err := store.StoreMessage(ctx, StoreMessageInput{Recipients: []string{"box@example.com"}, Subject: &subject, Headers: map[string]string{}, Attachments: []AttachmentInput{{Content: []byte("x")}}})
	if err != nil {
		t.Fatal(err)
	}
	ok, err := store.DeleteMailbox(ctx, stored[0].MailboxID)
	if err != nil || !ok {
		t.Fatalf("delete=%v %v", ok, err)
	}
	var messages, attachments int
	if err = store.db.QueryRow(`SELECT (SELECT COUNT(*) FROM messages),(SELECT COUNT(*) FROM attachments)`).Scan(&messages, &attachments); err != nil {
		t.Fatal(err)
	}
	if messages != 0 || attachments != 0 {
		t.Fatalf("counts=%d,%d", messages, attachments)
	}
	if err = store.WipeAll(ctx); err != nil {
		t.Fatal(err)
	}
	stored, err = store.StoreMessage(ctx, StoreMessageInput{Recipients: []string{"new@example.com"}, Headers: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	if stored[0].MailboxID != 1 || stored[0].MessageID != 1 {
		t.Fatalf("ids=%+v", stored[0])
	}
}

func TestCalendarSequenceCancelAndReplyRules(t *testing.T) {
	store := openTestStore(t, WithBroadcaster(func(events.Event) {}))
	ctx := context.Background()
	partstatNeeds := "NEEDS-ACTION"
	partstatAccepted := "ACCEPTED"
	summaryNew := "new"
	summaryOld := "old"
	base := calendar.ParsedCalendarEvent{Method: calendar.MethodRequest, UID: "uid", Sequence: 2, Summary: &summaryNew, DTStart: 100, Attendees: []calendar.ParsedAttendee{{Address: "person@example.com", Partstat: &partstatNeeds}}}
	if _, err := store.StoreMessage(ctx, StoreMessageInput{Recipients: []string{"box@example.com"}, Headers: map[string]string{}, ICalEvents: []calendar.ParsedCalendarEvent{base}}); err != nil {
		t.Fatal(err)
	}
	stale := base
	stale.Sequence = 1
	stale.Summary = &summaryOld
	if _, err := store.StoreMessage(ctx, StoreMessageInput{Recipients: []string{"box@example.com"}, Headers: map[string]string{}, ICalEvents: []calendar.ParsedCalendarEvent{stale}}); err != nil {
		t.Fatal(err)
	}
	reply := calendar.ParsedCalendarEvent{Method: calendar.MethodReply, UID: "uid", DTStart: 100, Attendees: []calendar.ParsedAttendee{{Address: "PERSON@example.com", Partstat: &partstatAccepted}}}
	if _, err := store.StoreMessage(ctx, StoreMessageInput{Recipients: []string{"box@example.com"}, Headers: map[string]string{}, ICalEvents: []calendar.ParsedCalendarEvent{reply}}); err != nil {
		t.Fatal(err)
	}
	cancel := calendar.ParsedCalendarEvent{Method: calendar.MethodCancel, UID: "uid", Sequence: 3, DTStart: 100}
	if _, err := store.StoreMessage(ctx, StoreMessageInput{Recipients: []string{"box@example.com"}, Headers: map[string]string{}, ICalEvents: []calendar.ParsedCalendarEvent{cancel}}); err != nil {
		t.Fatal(err)
	}
	mailboxes, _ := store.ListMailboxes(ctx)
	rows, err := store.ListCalendarEvents(ctx, mailboxes[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Sequence != 3 || rows[0].Status != "CANCELLED" || *rows[0].Summary != "new" {
		t.Fatalf("event=%+v", rows)
	}
	var attendees []calendar.ParsedAttendee
	if err = json.Unmarshal([]byte(rows[0].AttendeesJSON), &attendees); err != nil {
		t.Fatal(err)
	}
	if len(attendees) != 1 || attendees[0].Partstat == nil || *attendees[0].Partstat != "ACCEPTED" {
		t.Fatalf("attendees=%v", attendees)
	}
}

func TestMarkReadRequiresMailboxAndEmitsOnlyOnChange(t *testing.T) {
	var emitted []events.Event
	store := openTestStore(t, WithBroadcaster(func(event events.Event) {
		emitted = append(emitted, event)
	}))
	ctx := context.Background()
	stored, err := store.StoreMessage(ctx, StoreMessageInput{
		Recipients: []string{"first@example.com", "second@example.com"},
		Headers:    map[string]string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	emitted = nil

	if err := store.MarkRead(ctx, stored[0].MessageID, stored[1].MailboxID, 0); err != nil {
		t.Fatal(err)
	}
	detail, err := store.GetMessage(ctx, stored[0].MessageID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Message.IsRead != 0 || len(emitted) != 0 {
		t.Fatalf("wrong-mailbox MarkRead changed row or emitted event: read=%d events=%v", detail.Message.IsRead, emitted)
	}

	if err := store.MarkRead(ctx, stored[0].MessageID, stored[0].MailboxID, 0); err != nil {
		t.Fatal(err)
	}
	if detail, err = store.GetMessage(ctx, stored[0].MessageID); err != nil {
		t.Fatal(err)
	} else if detail.Message.IsRead != 1 || len(emitted) != 1 || emitted[0].Type != events.TypeMessagesChanged {
		t.Fatalf("matching MarkRead = read %d events %v", detail.Message.IsRead, emitted)
	}

	emitted = nil
	if err := store.MarkRead(ctx, stored[0].MessageID, stored[0].MailboxID, 1); err != nil {
		t.Fatal(err)
	}
	if len(emitted) != 0 {
		t.Fatalf("already-read snapshot emitted event: %v", emitted)
	}
}

func TestStoreMessagePersistsBoundedSnippets(t *testing.T) {
	store := openTestStore(t, WithBroadcaster(func(events.Event) {}))
	ctx := context.Background()
	text := strings.Repeat("ä", 200)
	html := `<style>.hidden{display:none}</style><p>first&nbsp;second</p><script>alert(1)</script>`
	tests := []struct {
		name string
		text *string
		html *string
		want string
	}{
		{name: "text takes precedence", text: &text, html: &html, want: normalizeSnippet(text, snippetLimit)},
		{name: "html strips active blocks and tags", html: &html, want: normalizeSnippet(tags.ReplaceAllString(styleScript.ReplaceAllString(html, " "), " "), snippetLimit)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := store.StoreMessage(ctx, StoreMessageInput{Recipients: []string{test.name + "@example.com"}, Text: test.text, HTML: test.html, Headers: map[string]string{}}); err != nil {
				t.Fatal(err)
			}
			mailboxes, err := store.ListMailboxes(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var mailboxID int64
			for _, mailbox := range mailboxes {
				if mailbox.Address == test.name+"@example.com" {
					mailboxID = mailbox.ID
				}
			}
			rows, err := store.ListMessages(ctx, mailboxID, "")
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != 1 || rows[0].Snippet != test.want {
				t.Fatalf("snippet=%q want %q rows=%v", rows[0].Snippet, test.want, rows)
			}
		})
	}
}

func TestOpenBackfillsSnippetColumnForLegacyRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-snippets.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		CREATE TABLE mailboxes (id INTEGER PRIMARY KEY AUTOINCREMENT, address TEXT NOT NULL UNIQUE, created_at INTEGER NOT NULL, last_message_at INTEGER);
		CREATE TABLE messages (id INTEGER PRIMARY KEY AUTOINCREMENT, mailbox_id INTEGER NOT NULL, from_address TEXT, from_name TEXT, to_json TEXT NOT NULL DEFAULT '[]', cc_json TEXT NOT NULL DEFAULT '[]', subject TEXT, html TEXT, text TEXT, headers_json TEXT NOT NULL DEFAULT '{}', size INTEGER NOT NULL DEFAULT 0, is_read INTEGER NOT NULL DEFAULT 0, received_at INTEGER NOT NULL);
		INSERT INTO mailboxes(address, created_at) VALUES ('legacy@example.com', 1);
	`)
	if err != nil {
		t.Fatal(err)
	}
	html := `<style>ignore</style><p>legacy body</p><script>ignore</script>`
	_, err = db.Exec(`INSERT INTO messages(mailbox_id,html,received_at) VALUES(1,?,2)`, html)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	rows, err := store.ListMessages(context.Background(), 1, "")
	if err != nil {
		t.Fatal(err)
	}
	want := normalizeSnippet(tags.ReplaceAllString(styleScript.ReplaceAllString(html, " "), " "), snippetLimit)
	if len(rows) != 1 || rows[0].Snippet != want {
		t.Fatalf("backfilled snippet=%q want %q rows=%v", rows[0].Snippet, want, rows)
	}
}

func TestOpenReconcilesExistingSnippetBacklog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "partial-snippets.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		CREATE TABLE mailboxes (id INTEGER PRIMARY KEY AUTOINCREMENT, address TEXT NOT NULL UNIQUE, created_at INTEGER NOT NULL, last_message_at INTEGER);
		CREATE TABLE messages (id INTEGER PRIMARY KEY AUTOINCREMENT, mailbox_id INTEGER NOT NULL, from_address TEXT, from_name TEXT, to_json TEXT NOT NULL DEFAULT '[]', cc_json TEXT NOT NULL DEFAULT '[]', subject TEXT, html TEXT, text TEXT, headers_json TEXT NOT NULL DEFAULT '{}', size INTEGER NOT NULL DEFAULT 0, is_read INTEGER NOT NULL DEFAULT 0, received_at INTEGER NOT NULL, ical_json TEXT, raw BLOB, snippet TEXT NOT NULL DEFAULT '');
		INSERT INTO mailboxes(address, created_at) VALUES ('legacy@example.com', 1);
		INSERT INTO messages(mailbox_id,subject,text,snippet,received_at) VALUES (1,'completed','completed body','already stored',1);
		INSERT INTO messages(mailbox_id,subject,text,snippet,received_at) VALUES (1,'interrupted','recover this body','',2);
		INSERT INTO messages(mailbox_id,subject,text,snippet,received_at) VALUES (1,'empty','  '||char(10)||char(9),'',3);
		CREATE TRIGGER reject_empty_snippet_rewrite BEFORE UPDATE OF snippet ON messages WHEN OLD.subject = 'empty' BEGIN SELECT RAISE(ABORT, 'empty snippet must not be rewritten'); END;
	`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := Open(path)
	if err != nil {
		t.Fatalf("open partially migrated store: %v", err)
	}
	defer store.Close()
	rows, err := store.ListMessages(context.Background(), 1, "")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, row := range rows {
		if row.Subject == nil {
			t.Fatalf("message subject is nil: %v", row)
		}
		got[*row.Subject] = row.Snippet
	}
	want := map[string]string{
		"completed":   "already stored",
		"interrupted": "recover this body",
		"empty":       "",
	}
	if len(got) != len(want) {
		t.Fatalf("reconciled snippets=%v want %v", got, want)
	}
	for subject, wantSnippet := range want {
		if got[subject] != wantSnippet {
			t.Errorf("%s snippet=%q want %q", subject, got[subject], wantSnippet)
		}
	}
}

func TestSnippetMigrationRollsBackOnBackfillFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-snippets-failure.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		CREATE TABLE mailboxes (id INTEGER PRIMARY KEY AUTOINCREMENT, address TEXT NOT NULL UNIQUE, created_at INTEGER NOT NULL, last_message_at INTEGER);
		CREATE TABLE messages (id INTEGER PRIMARY KEY AUTOINCREMENT, mailbox_id INTEGER NOT NULL, from_address TEXT, from_name TEXT, to_json TEXT NOT NULL DEFAULT '[]', cc_json TEXT NOT NULL DEFAULT '[]', subject TEXT, html TEXT, text TEXT, headers_json TEXT NOT NULL DEFAULT '{}', size INTEGER NOT NULL DEFAULT 0, is_read INTEGER NOT NULL DEFAULT 0, received_at INTEGER NOT NULL);
		INSERT INTO mailboxes(address, created_at) VALUES ('legacy@example.com', 1);
		INSERT INTO messages(mailbox_id,text,received_at) VALUES (1,'legacy body',2);
		CREATE TRIGGER fail_snippet_backfill BEFORE UPDATE ON messages BEGIN SELECT RAISE(ABORT, 'snippet backfill blocked'); END;
	`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := Open(path); err == nil {
		t.Fatal("Open unexpectedly succeeded despite backfill trigger")
	}

	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(`PRAGMA table_info(messages)`)
	if err != nil {
		t.Fatal(err)
	}
	columns := map[string]bool{}
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, dataType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		columns[name] = true
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if columns["ical_json"] || columns["raw"] || columns["snippet"] {
		t.Fatalf("failed migration left columns behind: %#v", columns)
	}
	if _, err := db.Exec(`DROP TRIGGER fail_snippet_backfill`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := Open(path)
	if err != nil {
		t.Fatalf("retry Open: %v", err)
	}
	defer store.Close()
	messages, err := store.ListMessages(context.Background(), 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].Snippet != "legacy body" {
		t.Fatalf("retry backfill=%v", messages)
	}
}

func TestPOP3SerializationHonorsContextCancellation(t *testing.T) {
	store := openTestStore(t)
	tests := []struct {
		name string
		call func(context.Context) error
	}{
		{name: "open", call: func(ctx context.Context) error {
			_, err := store.OpenPOP3Mailbox(ctx, "blocked@example.com")
			return err
		}},
		{name: "delete", call: func(ctx context.Context) error {
			_, err := store.DeletePOP3Messages(ctx, store.pop3Generation, []int64{1})
			return err
		}},
		{name: "delete-mailbox", call: func(ctx context.Context) error {
			_, err := store.DeleteMailbox(ctx, 1)
			return err
		}},
		{name: "store-message", call: func(ctx context.Context) error {
			_, err := store.StoreMessage(ctx, StoreMessageInput{
				Recipients: []string{"blocked@example.com"},
				Headers:    map[string]string{},
			})
			return err
		}},
		{name: "wipe", call: store.WipeAll},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store.pop3Mu.Lock()
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			result := make(chan error, 1)
			go func() { result <- test.call(ctx) }()
			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("error=%v want context.Canceled", err)
				}
			case <-time.After(time.Second):
				t.Fatal("canceled POP3 operation remained blocked")
			}
			store.pop3Mu.Unlock()
		})
	}
}

// TestForeignKeysSurviveConnectionRecycling proves that foreign key
// enforcement and ON DELETE CASCADE survive database/sql connection
// recycling. Connection-scoped pragmas applied once during startup die with
// their connection; with SetMaxOpenConns(1) the first recycle would silently
// disable them for the rest of the process.
func TestForeignKeysSurviveConnectionRecycling(t *testing.T) {
	store := openTestStore(t)
	db := store.DB()
	ctx := context.Background()

	mailbox, err := db.ExecContext(ctx, `INSERT INTO mailboxes (address, created_at) VALUES ('recycle@test', 1)`)
	if err != nil {
		t.Fatalf("seed mailbox: %v", err)
	}
	mailboxID, err := mailbox.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO messages (mailbox_id, received_at) VALUES (?, 1)`, mailboxID); err != nil {
		t.Fatalf("seed message: %v", err)
	}

	// Force the pool to discard its current physical connection and open a
	// fresh one on the next checkout: with a nanosecond lifetime every pooled
	// connection is expired by the time we query again.
	db.SetConnMaxLifetime(time.Nanosecond)
	time.Sleep(5 * time.Millisecond)
	if _, err := db.ExecContext(ctx, `SELECT 1`); err != nil {
		t.Fatalf("trigger connection recycling: %v", err)
	}
	db.SetConnMaxLifetime(0)

	var foreignKeys int
	if err := db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		t.Fatalf("read foreign_keys pragma: %v", err)
	}
	if foreignKeys != 1 {
		t.Fatalf("PRAGMA foreign_keys=%d on recycled connection, want 1", foreignKeys)
	}

	if _, err := db.ExecContext(ctx, `INSERT INTO messages (mailbox_id, received_at) VALUES (999999, 1)`); err == nil {
		t.Fatal("insert referencing unknown mailbox succeeded; foreign keys are not enforced")
	}

	if _, err := db.ExecContext(ctx, `DELETE FROM mailboxes WHERE id = ?`, mailboxID); err != nil {
		t.Fatalf("delete mailbox: %v", err)
	}
	var orphans int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE mailbox_id = ?`, mailboxID).Scan(&orphans); err != nil {
		t.Fatalf("count orphaned messages: %v", err)
	}
	if orphans != 0 {
		t.Fatalf("ON DELETE CASCADE did not run: %d orphaned message(s) remain", orphans)
	}
}
