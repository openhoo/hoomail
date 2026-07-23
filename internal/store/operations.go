package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/openhoo/hoomail/internal/calendar"
	"github.com/openhoo/hoomail/internal/events"
)

func (store *Store) GetMessage(ctx context.Context, id int64) (*MessageDetail, error) {
	var message Message
	err := store.db.QueryRowContext(ctx, `SELECT id,mailbox_id,from_address,from_name,to_json,cc_json,subject,html,text,headers_json,size,is_read,received_at,ical_json FROM messages WHERE id=?`, id).Scan(
		&message.ID, &message.MailboxID, &message.FromAddress, &message.FromName, &message.ToJSON, &message.CCJSON, &message.Subject, &message.HTML, &message.Text, &message.HeadersJSON, &message.Size, &message.IsRead, &message.ReceivedAt, &message.ICalJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rows, err := store.db.QueryContext(ctx, `SELECT id,filename,content_type,content_id,size FROM attachments WHERE message_id=?`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	attachments := []AttachmentInfo{}
	for rows.Next() {
		var attachment AttachmentInfo
		if err := rows.Scan(&attachment.ID, &attachment.Filename, &attachment.ContentType, &attachment.ContentID, &attachment.Size); err != nil {
			return nil, err
		}
		attachments = append(attachments, attachment)
	}
	return &MessageDetail{Message: message, Attachments: attachments}, rows.Err()
}

func (store *Store) GetAttachment(ctx context.Context, id int64) (*Attachment, error) {
	var attachment Attachment
	err := store.db.QueryRowContext(ctx, `SELECT id,filename,content_type,content_id,size,content FROM attachments WHERE id=?`, id).Scan(&attachment.ID, &attachment.Filename, &attachment.ContentType, &attachment.ContentID, &attachment.Size, &attachment.Content)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &attachment, err
}

func (store *Store) GetMessageRaw(ctx context.Context, id int64) (*RawMessage, error) {
	var raw RawMessage
	err := store.db.QueryRowContext(ctx, `SELECT raw,html,text,headers_json,size FROM messages WHERE id=?`, id).Scan(&raw.Raw, &raw.HTML, &raw.Text, &raw.HeadersJSON, &raw.Size)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &raw, err
}

func (store *Store) MarkRead(ctx context.Context, id, mailboxID int64, wasRead int) error {
	if wasRead != 0 {
		return nil
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE messages SET is_read=1 WHERE id=?`, id); err != nil {
		return err
	}
	store.emit(events.MessagesChanged(mailboxID))
	return nil
}

func placeholders(count int) string { return strings.TrimSuffix(strings.Repeat("?,", count), ",") }
func intArgs(ids []int64) []any {
	result := make([]any, len(ids))
	for index, id := range ids {
		result[index] = id
	}
	return result
}

func (store *Store) SetReadState(ctx context.Context, ids []int64, isRead bool) ([]int64, error) {
	if len(ids) == 0 {
		return []int64{}, nil
	}
	args := intArgs(ids)
	affected, err := store.affectedMailboxes(ctx, ids)
	if err != nil {
		return nil, err
	}
	value := 0
	if isRead {
		value = 1
	}
	updateArgs := append([]any{value}, args...)
	if _, err = store.db.ExecContext(ctx, `UPDATE messages SET is_read=? WHERE id IN (`+placeholders(len(ids))+`)`, updateArgs...); err != nil {
		return nil, err
	}
	for _, id := range affected {
		store.emit(events.MessagesChanged(id))
	}
	return affected, nil
}

func (store *Store) DeleteMessages(ctx context.Context, ids []int64) ([]int64, error) {
	if len(ids) == 0 {
		return []int64{}, nil
	}
	affected, err := store.affectedMailboxes(ctx, ids)
	if err != nil {
		return nil, err
	}
	if _, err = store.db.ExecContext(ctx, `DELETE FROM messages WHERE id IN (`+placeholders(len(ids))+`)`, intArgs(ids)...); err != nil {
		return nil, err
	}
	for _, id := range affected {
		store.emit(events.MessagesChanged(id))
	}
	return affected, nil
}

func (store *Store) affectedMailboxes(ctx context.Context, ids []int64) ([]int64, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT DISTINCT mailbox_id FROM messages WHERE id IN (`+placeholders(len(ids))+`)`, intArgs(ids)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		result = append(result, id)
	}
	return result, rows.Err()
}

func (store *Store) ListCalendarEvents(ctx context.Context, mailboxID int64) ([]CalendarEvent, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT id,uid,sequence,summary,description,location,status,organizer_address,organizer_name,attendees_json,dtstart,dtend,all_day,last_message_id,updated_at FROM calendar_events WHERE mailbox_id=? ORDER BY dtstart ASC`, mailboxID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []CalendarEvent{}
	for rows.Next() {
		var row CalendarEvent
		if err := rows.Scan(&row.ID, &row.UID, &row.Sequence, &row.Summary, &row.Description, &row.Location, &row.Status, &row.OrganizerAddress, &row.OrganizerName, &row.AttendeesJSON, &row.DTStart, &row.DTEnd, &row.AllDay, &row.LastMessageID, &row.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func (store *Store) DeleteMailbox(ctx context.Context, id int64) (bool, error) {
	result, err := store.db.ExecContext(ctx, `DELETE FROM mailboxes WHERE id=?`, id)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if count == 0 {
		return false, nil
	}
	store.emit(events.MailboxDeleted(id))
	return true, nil
}

func (store *Store) WipeAll(ctx context.Context) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	for _, statement := range []string{`DELETE FROM attachments`, `DELETE FROM calendar_events`, `DELETE FROM messages`, `DELETE FROM mailboxes`, `DELETE FROM sqlite_sequence WHERE name IN ('attachments','calendar_events','messages','mailboxes')`} {
		if _, err = tx.ExecContext(ctx, statement); err != nil {
			tx.Rollback()
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	store.emit(events.Reset())
	return nil
}

func (store *Store) StoreMessage(ctx context.Context, input StoreMessageInput) ([]StoredMessage, error) {
	now := store.now().UnixMilli()
	toJSON, err := json.Marshal(input.To)
	if err != nil {
		return nil, err
	}
	ccJSON, err := json.Marshal(input.CC)
	if err != nil {
		return nil, err
	}
	headersJSON, err := json.Marshal(input.Headers)
	if err != nil {
		return nil, err
	}
	var icalJSON any
	if len(input.ICalEvents) > 0 {
		data, marshalErr := json.Marshal(input.ICalEvents)
		if marshalErr != nil {
			return nil, marshalErr
		}
		icalJSON = string(data)
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	pending := []events.Event{}
	stored := []StoredMessage{}
	for _, rawAddress := range input.Recipients {
		address := strings.TrimSpace(strings.ToLower(rawAddress))
		if address == "" {
			continue
		}
		var existingID int64
		lookupErr := tx.QueryRowContext(ctx, `SELECT id FROM mailboxes WHERE address=?`, address).Scan(&existingID)
		isNew := errors.Is(lookupErr, sql.ErrNoRows)
		if lookupErr != nil && !isNew {
			return nil, lookupErr
		}
		var mailboxID int64
		err = tx.QueryRowContext(ctx, `INSERT INTO mailboxes(address,created_at,last_message_at) VALUES(?,?,?) ON CONFLICT(address) DO UPDATE SET last_message_at=excluded.last_message_at RETURNING id`, address, now, now).Scan(&mailboxID)
		if err != nil {
			return nil, err
		}
		if isNew {
			pending = append(pending, events.MailboxNew(mailboxID, address))
		}
		result, execErr := tx.ExecContext(ctx, `INSERT INTO messages(mailbox_id,from_address,from_name,to_json,cc_json,subject,html,text,headers_json,size,is_read,received_at,ical_json,raw) VALUES(?,?,?,?,?,?,?,?,?,?,0,?,?,?)`, mailboxID, input.FromAddress, input.FromName, string(toJSON), string(ccJSON), input.Subject, input.HTML, input.Text, string(headersJSON), input.Size, now, icalJSON, input.Raw)
		if execErr != nil {
			return nil, execErr
		}
		messageID, execErr := result.LastInsertId()
		if execErr != nil {
			return nil, execErr
		}
		if len(input.ICalEvents) > 0 {
			if err = applyCalendarEvents(ctx, tx, mailboxID, messageID, input.ICalEvents, now); err != nil {
				return nil, err
			}
			pending = append(pending, events.CalendarChanged(mailboxID))
		}
		for _, attachment := range input.Attachments {
			if _, err = tx.ExecContext(ctx, `INSERT INTO attachments(message_id,filename,content_type,content_id,size,content) VALUES(?,?,?,?,?,?)`, messageID, attachment.Filename, attachment.ContentType, attachment.ContentID, len(attachment.Content), attachment.Content); err != nil {
				return nil, err
			}
		}
		pending = append(pending, events.MessageNew(mailboxID, messageID, input.Subject, input.FromAddress, input.FromName))
		stored = append(stored, StoredMessage{MailboxID: mailboxID, MessageID: messageID})
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	for _, event := range pending {
		store.emit(event)
	}
	return stored, nil
}

func (store *Store) emit(event events.Event) {
	if store.broadcast != nil {
		store.broadcast(event)
	}
}

func applyCalendarEvents(ctx context.Context, tx *sql.Tx, mailboxID, messageID int64, parsed []calendar.ParsedCalendarEvent, now int64) error {
	for _, event := range parsed {
		var id int64
		var sequence int
		var attendeesJSON string
		err := tx.QueryRowContext(ctx, `SELECT id,sequence,attendees_json FROM calendar_events WHERE mailbox_id=? AND uid=?`, mailboxID, event.UID).Scan(&id, &sequence, &attendeesJSON)
		exists := err == nil
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if event.Method == calendar.MethodCancel {
			if exists {
				_, err = tx.ExecContext(ctx, `UPDATE calendar_events SET status='CANCELLED',sequence=MAX(sequence,?),last_message_id=?,updated_at=? WHERE id=?`, event.Sequence, messageID, now, id)
			} else {
				err = insertCalendarEvent(ctx, tx, mailboxID, messageID, event, "CANCELLED", now)
			}
			if err != nil {
				return err
			}
			continue
		}
		if event.Method == calendar.MethodReply {
			if exists && len(event.Attendees) > 0 {
				var attendees []calendar.ParsedAttendee
				if err = json.Unmarshal([]byte(attendeesJSON), &attendees); err != nil {
					return err
				}
				for _, reply := range event.Attendees {
					found := false
					for index := range attendees {
						if strings.EqualFold(attendees[index].Address, reply.Address) {
							attendees[index].Partstat = reply.Partstat
							found = true
							break
						}
					}
					if !found {
						attendees = append(attendees, reply)
					}
				}
				data, _ := json.Marshal(attendees)
				if _, err = tx.ExecContext(ctx, `UPDATE calendar_events SET attendees_json=?,last_message_id=?,updated_at=? WHERE id=?`, string(data), messageID, now, id); err != nil {
					return err
				}
			}
			continue
		}
		status := "CONFIRMED"
		if event.Status != nil && *event.Status != "" {
			status = *event.Status
		}
		if status == "CANCELLED" {
			status = "CANCELLED"
		}
		attendees, _ := json.Marshal(event.Attendees)
		if exists {
			if event.Sequence >= sequence {
				_, err = tx.ExecContext(ctx, `UPDATE calendar_events SET sequence=?,summary=?,description=?,location=?,status=?,organizer_address=?,organizer_name=?,attendees_json=?,dtstart=?,dtend=?,all_day=?,last_message_id=?,updated_at=? WHERE id=?`, event.Sequence, event.Summary, event.Description, event.Location, status, event.OrganizerAddress, event.OrganizerName, string(attendees), event.DTStart, event.DTEnd, boolInt(event.AllDay), messageID, now, id)
			}
		} else {
			err = insertCalendarEvent(ctx, tx, mailboxID, messageID, event, status, now)
		}
		if err != nil {
			return err
		}
	}
	return nil
}
func insertCalendarEvent(ctx context.Context, tx *sql.Tx, mailboxID, messageID int64, event calendar.ParsedCalendarEvent, status string, now int64) error {
	attendees, err := json.Marshal(event.Attendees)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO calendar_events(mailbox_id,uid,sequence,summary,description,location,status,organizer_address,organizer_name,attendees_json,dtstart,dtend,all_day,last_message_id,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, mailboxID, event.UID, event.Sequence, event.Summary, event.Description, event.Location, status, event.OrganizerAddress, event.OrganizerName, string(attendees), event.DTStart, event.DTEnd, boolInt(event.AllDay), messageID, now)
	return err
}
func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
