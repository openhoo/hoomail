package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
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
	if !columns["ical_json"] || !columns["raw"] {
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
		subject := subject
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
