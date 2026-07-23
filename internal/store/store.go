package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/openhoo/hoomail/internal/calendar"
	"github.com/openhoo/hoomail/internal/events"
	_ "modernc.org/sqlite"
)

type Store struct {
	db        *sql.DB
	broadcast func(events.Event)
	now       func() time.Time
}

type Option func(*Store)

func WithBroadcaster(broadcast func(events.Event)) Option {
	return func(store *Store) { store.broadcast = broadcast }
}

func WithClock(now func() time.Time) Option {
	return func(store *Store) { store.now = now }
}

func Open(path string, options ...Option) (*Store, error) {
	if path == "" {
		return nil, errors.New("database path is empty")
	}
	if path != ":memory:" && !strings.HasPrefix(path, "file:") {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db, broadcast: events.Broadcast, now: time.Now}
	for _, option := range options {
		option(store)
	}
	if err := store.init(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func (store *Store) Close() error { return store.db.Close() }
func (store *Store) DB() *sql.DB  { return store.db }

func (store *Store) init(ctx context.Context) error {
	if _, err := store.db.ExecContext(ctx, `PRAGMA foreign_keys = ON; PRAGMA journal_mode = WAL;`); err != nil {
		return fmt.Errorf("configure database: %w", err)
	}
	if _, err := store.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("initialize schema: %w", err)
	}
	columns, err := store.tableColumns(ctx, "messages")
	if err != nil {
		return err
	}
	for _, migration := range []struct{ column, statement string }{
		{"ical_json", `ALTER TABLE messages ADD COLUMN ical_json TEXT`},
		{"raw", `ALTER TABLE messages ADD COLUMN raw BLOB`},
	} {
		if !columns[migration.column] {
			if _, err := store.db.ExecContext(ctx, migration.statement); err != nil {
				return fmt.Errorf("add messages.%s: %w", migration.column, err)
			}
		}
	}
	return nil
}

func (store *Store) tableColumns(ctx context.Context, table string) (map[string]bool, error) {
	rows, err := store.db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return nil, fmt.Errorf("inspect %s: %w", table, err)
	}
	defer rows.Close()
	columns := map[string]bool{}
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, dataType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			return nil, err
		}
		columns[name] = true
	}
	return columns, rows.Err()
}

const schema = `
CREATE TABLE IF NOT EXISTS mailboxes (
 id INTEGER PRIMARY KEY AUTOINCREMENT, address TEXT NOT NULL UNIQUE, created_at INTEGER NOT NULL, last_message_at INTEGER
);
CREATE TABLE IF NOT EXISTS messages (
 id INTEGER PRIMARY KEY AUTOINCREMENT, mailbox_id INTEGER NOT NULL REFERENCES mailboxes(id) ON DELETE CASCADE,
 from_address TEXT, from_name TEXT, to_json TEXT NOT NULL DEFAULT '[]', cc_json TEXT NOT NULL DEFAULT '[]', subject TEXT,
 html TEXT, text TEXT, headers_json TEXT NOT NULL DEFAULT '{}', size INTEGER NOT NULL DEFAULT 0,
 is_read INTEGER NOT NULL DEFAULT 0, received_at INTEGER NOT NULL, ical_json TEXT, raw BLOB
);
CREATE INDEX IF NOT EXISTS idx_messages_mailbox ON messages(mailbox_id, received_at DESC);
CREATE TABLE IF NOT EXISTS attachments (
 id INTEGER PRIMARY KEY AUTOINCREMENT, message_id INTEGER NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
 filename TEXT, content_type TEXT, content_id TEXT, size INTEGER NOT NULL DEFAULT 0, content BLOB
);
CREATE INDEX IF NOT EXISTS idx_attachments_message ON attachments(message_id);
CREATE TABLE IF NOT EXISTS calendar_events (
 id INTEGER PRIMARY KEY AUTOINCREMENT, mailbox_id INTEGER NOT NULL REFERENCES mailboxes(id) ON DELETE CASCADE,
 uid TEXT NOT NULL, sequence INTEGER NOT NULL DEFAULT 0, summary TEXT, description TEXT, location TEXT,
 status TEXT NOT NULL DEFAULT 'CONFIRMED', organizer_address TEXT, organizer_name TEXT, attendees_json TEXT NOT NULL DEFAULT '[]',
 dtstart INTEGER NOT NULL, dtend INTEGER, all_day INTEGER NOT NULL DEFAULT 0, last_message_id INTEGER, updated_at INTEGER NOT NULL,
 UNIQUE(mailbox_id, uid)
);
CREATE INDEX IF NOT EXISTS idx_calendar_events_mailbox ON calendar_events(mailbox_id, dtstart);`

type Mailbox struct {
	ID            int64  `json:"id"`
	Address       string `json:"address"`
	CreatedAt     int64  `json:"created_at"`
	LastMessageAt *int64 `json:"last_message_at"`
	TotalCount    int64  `json:"total_count"`
	UnreadCount   int64  `json:"unread_count"`
}

type AddressEntry struct {
	Address string  `json:"address"`
	Name    *string `json:"name,omitempty"`
}

type MessageListItem struct {
	ID              int64   `json:"id"`
	FromAddress     *string `json:"from_address"`
	FromName        *string `json:"from_name"`
	Subject         *string `json:"subject"`
	Snippet         string  `json:"snippet"`
	IsRead          int     `json:"is_read"`
	ReceivedAt      int64   `json:"received_at"`
	HasICal         int     `json:"has_ical"`
	AttachmentCount int64   `json:"attachment_count"`
}

type Message struct {
	ID          int64   `json:"id"`
	MailboxID   int64   `json:"mailbox_id"`
	FromAddress *string `json:"from_address"`
	FromName    *string `json:"from_name"`
	ToJSON      string  `json:"to_json"`
	CCJSON      string  `json:"cc_json"`
	Subject     *string `json:"subject"`
	HTML        *string `json:"html"`
	Text        *string `json:"text"`
	HeadersJSON string  `json:"headers_json"`
	Size        int64   `json:"size"`
	IsRead      int     `json:"is_read"`
	ReceivedAt  int64   `json:"received_at"`
	ICalJSON    *string `json:"ical_json"`
}

type AttachmentInfo struct {
	ID          int64   `json:"id"`
	Filename    *string `json:"filename"`
	ContentType *string `json:"content_type"`
	ContentID   *string `json:"content_id"`
	Size        int64   `json:"size"`
}
type Attachment struct {
	AttachmentInfo
	Content []byte `json:"content"`
}
type MessageDetail struct {
	Message     Message          `json:"message"`
	Attachments []AttachmentInfo `json:"attachments"`
}
type RawMessage struct {
	Raw         []byte  `json:"raw"`
	HTML        *string `json:"html"`
	Text        *string `json:"text"`
	HeadersJSON string  `json:"headers_json"`
	Size        int64   `json:"size"`
}

type CalendarEvent struct {
	ID               int64   `json:"id"`
	UID              string  `json:"uid"`
	Sequence         int     `json:"sequence"`
	Summary          *string `json:"summary"`
	Description      *string `json:"description"`
	Location         *string `json:"location"`
	Status           string  `json:"status"`
	OrganizerAddress *string `json:"organizer_address"`
	OrganizerName    *string `json:"organizer_name"`
	AttendeesJSON    string  `json:"attendees_json"`
	DTStart          int64   `json:"dtstart"`
	DTEnd            *int64  `json:"dtend"`
	AllDay           int     `json:"all_day"`
	LastMessageID    *int64  `json:"last_message_id"`
	UpdatedAt        int64   `json:"updated_at"`
}

type AttachmentInput struct {
	Filename    *string
	ContentType *string
	ContentID   *string
	Content     []byte
}
type StoreMessageInput struct {
	Recipients  []string
	FromAddress *string
	FromName    *string
	To          []AddressEntry
	CC          []AddressEntry
	Subject     *string
	HTML        *string
	Text        *string
	Headers     map[string]string
	Size        int64
	Attachments []AttachmentInput
	ICalEvents  []calendar.ParsedCalendarEvent
	Raw         []byte
}
type StoredMessage struct {
	MailboxID int64
	MessageID int64
}

func (store *Store) ListMailboxes(ctx context.Context) ([]Mailbox, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT m.id,m.address,m.created_at,m.last_message_at,(SELECT COUNT(*) FROM messages WHERE mailbox_id=m.id),(SELECT COUNT(*) FROM messages WHERE mailbox_id=m.id AND is_read=0) FROM mailboxes m ORDER BY COALESCE(m.last_message_at,m.created_at) DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Mailbox{}
	for rows.Next() {
		var row Mailbox
		if err := rows.Scan(&row.ID, &row.Address, &row.CreatedAt, &row.LastMessageAt, &row.TotalCount, &row.UnreadCount); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

var tags = regexp.MustCompile(`(?s)<[^>]+>`)
var styleScript = regexp.MustCompile(`(?is)<(?:style|script)[^>]*>.*?</(?:style|script)>`)
var whitespace = regexp.MustCompile(`\s+`)

func (store *Store) ListMessages(ctx context.Context, mailboxID int64, query string) ([]MessageListItem, error) {
	statement := `SELECT id,from_address,from_name,subject,text,html,is_read,received_at,ical_json IS NOT NULL,(SELECT COUNT(*) FROM attachments a WHERE a.message_id=messages.id AND a.content_id IS NULL) FROM messages WHERE mailbox_id=?`
	args := []any{mailboxID}
	if q := strings.TrimSpace(query); q != "" {
		escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q)
		like := "%" + escaped + "%"
		statement += ` AND (subject LIKE ? ESCAPE '\' OR from_address LIKE ? ESCAPE '\' OR from_name LIKE ? ESCAPE '\' OR text LIKE ? ESCAPE '\')`
		args = append(args, like, like, like, like)
	}
	statement += ` ORDER BY received_at DESC`
	rows, err := store.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []MessageListItem{}
	for rows.Next() {
		var row MessageListItem
		var textValue, htmlValue *string
		if err := rows.Scan(&row.ID, &row.FromAddress, &row.FromName, &row.Subject, &textValue, &htmlValue, &row.IsRead, &row.ReceivedAt, &row.HasICal, &row.AttachmentCount); err != nil {
			return nil, err
		}
		source := ""
		if textValue != nil {
			source = *textValue
		} else if htmlValue != nil {
			source = tags.ReplaceAllString(styleScript.ReplaceAllString(*htmlValue, " "), " ")
		}
		source = strings.TrimSpace(whitespace.ReplaceAllString(source, " "))
		if len([]rune(source)) > 140 {
			source = string([]rune(source)[:140])
		}
		row.Snippet = source
		result = append(result, row)
	}
	return result, rows.Err()
}
