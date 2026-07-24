package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"mime"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/openhoo/hoomail/internal/calendar"
	"github.com/openhoo/hoomail/internal/events"
	"github.com/openhoo/hoomail/internal/inspect"
	"github.com/openhoo/hoomail/internal/store"
)

// SendTestRequest is the normalized request passed to TestSender.
type SendTestRequest struct {
	To      string
	Subject string
	Kind    string
}

// TestSender sends one of the built-in delivery test messages.
type TestSender interface {
	SendTest(context.Context, SendTestRequest) error
}

// TestSenderFunc adapts a function to TestSender.
type TestSenderFunc func(context.Context, SendTestRequest) error

func (send TestSenderFunc) SendTest(ctx context.Context, request SendTestRequest) error {
	return send(ctx, request)
}

// StaticConfig configures the optional single-page application handler.
type StaticConfig struct {
	FS    fs.FS
	Index string
}

type server struct {
	store  *store.Store
	static StaticConfig
	sender TestSender
}

// New constructs the legacy-compatible API and SPA handler.
func New(data *store.Store, static StaticConfig, sender TestSender) http.Handler {
	if static.Index == "" {
		static.Index = "index.html"
	}
	return &server{store: data, static: static, sender: sender}
}

func (s *server) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	route := request.URL.Path
	switch {
	case (request.Method == http.MethodGet || request.Method == http.MethodHead) && route == "/openapi.json":
		serveOpenAPI(response, request)
	case (request.Method == http.MethodGet || request.Method == http.MethodHead) && route == "/swagger":
		http.Redirect(response, request, "/swagger/", http.StatusPermanentRedirect)
	case (request.Method == http.MethodGet || request.Method == http.MethodHead) && route == "/swagger/":
		serveSwaggerUI(response, request)
	case route == "/openapi.json" || route == "/swagger" || route == "/swagger/":
		response.Header().Set("Allow", "GET, HEAD")
		http.Error(response, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
	case request.Method == http.MethodGet && route == "/api/mailboxes":
		s.listMailboxes(response, request)
	case request.Method == http.MethodDelete && strings.HasPrefix(route, "/api/mailboxes/") && !strings.Contains(strings.TrimPrefix(route, "/api/mailboxes/"), "/"):
		s.deleteMailbox(response, request, strings.TrimPrefix(route, "/api/mailboxes/"))
	case request.Method == http.MethodGet && strings.HasPrefix(route, "/api/mailboxes/") && strings.HasSuffix(route, "/messages"):
		s.listMessages(response, request, strings.TrimSuffix(strings.TrimPrefix(route, "/api/mailboxes/"), "/messages"))
	case request.Method == http.MethodGet && strings.HasPrefix(route, "/api/mailboxes/") && strings.HasSuffix(route, "/events"):
		s.listCalendarEvents(response, request, strings.TrimSuffix(strings.TrimPrefix(route, "/api/mailboxes/"), "/events"))
	case request.Method == http.MethodPost && route == "/api/messages/actions":
		s.messageActions(response, request)
	case request.Method == http.MethodGet && strings.HasPrefix(route, "/api/messages/") && strings.HasSuffix(route, "/source"):
		s.getMessageSource(response, request, strings.TrimSuffix(strings.TrimPrefix(route, "/api/messages/"), "/source"))
	case request.Method == http.MethodGet && strings.HasPrefix(route, "/api/messages/") && strings.HasSuffix(route, "/inspect"):
		s.inspectMessage(response, request, strings.TrimSuffix(strings.TrimPrefix(route, "/api/messages/"), "/inspect"))
	case request.Method == http.MethodGet && strings.HasPrefix(route, "/api/messages/") && !strings.Contains(strings.TrimPrefix(route, "/api/messages/"), "/"):
		s.getMessage(response, request, strings.TrimPrefix(route, "/api/messages/"))
	case request.Method == http.MethodGet && strings.HasPrefix(route, "/api/attachments/") && !strings.Contains(strings.TrimPrefix(route, "/api/attachments/"), "/"):
		s.getAttachment(response, request, strings.TrimPrefix(route, "/api/attachments/"))
	case request.Method == http.MethodGet && route == "/api/events":
		s.eventStream(response, request)
	case request.Method == http.MethodPost && route == "/api/reset":
		s.reset(response, request)
	case request.Method == http.MethodPost && route == "/api/send-test":
		s.sendTest(response, request)
	case strings.HasPrefix(route, "/api/"):
		http.NotFound(response, request)
	default:
		s.serveStatic(response, request)
	}
}

func parseID(value string) (int64, bool) {
	number, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsInf(number, 0) || math.IsNaN(number) || math.Trunc(number) != number || number < math.MinInt64 || number > math.MaxInt64 {
		return 0, false
	}
	return int64(number), true
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	body, err := json.Marshal(value)
	if err != nil {
		http.Error(response, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_, _ = response.Write(body)
}

func writeError(response http.ResponseWriter, status int, message string) {
	writeJSON(response, status, map[string]string{"error": message})
}

func internalError(response http.ResponseWriter, err error) {
	if err != nil {
		http.Error(response, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
	}
}

func (s *server) listMailboxes(response http.ResponseWriter, request *http.Request) {
	mailboxes, err := s.store.ListMailboxes(request.Context())
	if err != nil {
		internalError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, struct {
		Mailboxes []store.Mailbox `json:"mailboxes"`
	}{mailboxes})
}

func (s *server) deleteMailbox(response http.ResponseWriter, request *http.Request, rawID string) {
	id, ok := parseID(rawID)
	if !ok {
		writeError(response, http.StatusBadRequest, "Invalid mailbox id")
		return
	}
	deleted, err := s.store.DeleteMailbox(request.Context(), id)
	if err != nil {
		internalError(response, err)
		return
	}
	if !deleted {
		writeError(response, http.StatusNotFound, "Mailbox not found")
		return
	}
	writeJSON(response, http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) listMessages(response http.ResponseWriter, request *http.Request, rawID string) {
	id, ok := parseID(rawID)
	if !ok {
		writeError(response, http.StatusBadRequest, "Invalid mailbox id")
		return
	}
	messages, err := s.store.ListMessages(request.Context(), id, request.URL.Query().Get("q"))
	if err != nil {
		internalError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, struct {
		Messages []store.MessageListItem `json:"messages"`
	}{messages})
}

type calendarEventResponse struct {
	ID               int64   `json:"id"`
	UID              string  `json:"uid"`
	Sequence         int     `json:"sequence"`
	Summary          *string `json:"summary"`
	Description      *string `json:"description"`
	Location         *string `json:"location"`
	Status           string  `json:"status"`
	OrganizerAddress *string `json:"organizerAddress"`
	OrganizerName    *string `json:"organizerName"`
	Attendees        any     `json:"attendees"`
	DTStart          int64   `json:"dtstart"`
	DTEnd            *int64  `json:"dtend"`
	AllDay           bool    `json:"allDay"`
	LastMessageID    *int64  `json:"lastMessageId"`
	UpdatedAt        int64   `json:"updatedAt"`
}

func (s *server) listCalendarEvents(response http.ResponseWriter, request *http.Request, rawID string) {
	id, ok := parseID(rawID)
	if !ok {
		writeError(response, http.StatusBadRequest, "Invalid mailbox id")
		return
	}
	rows, err := s.store.ListCalendarEvents(request.Context(), id)
	if err != nil {
		internalError(response, err)
		return
	}
	result := make([]calendarEventResponse, 0, len(rows))
	for _, row := range rows {
		var attendees any
		if err := json.Unmarshal([]byte(row.AttendeesJSON), &attendees); err != nil {
			internalError(response, err)
			return
		}
		result = append(result, calendarEventResponse{row.ID, row.UID, row.Sequence, row.Summary, row.Description, row.Location, row.Status, row.OrganizerAddress, row.OrganizerName, attendees, row.DTStart, row.DTEnd, row.AllDay == 1, row.LastMessageID, row.UpdatedAt})
	}
	writeJSON(response, http.StatusOK, struct {
		Events []calendarEventResponse `json:"events"`
	}{result})
}

type messageResponse struct {
	ID          int64   `json:"id"`
	MailboxID   int64   `json:"mailboxId"`
	FromAddress *string `json:"fromAddress"`
	FromName    *string `json:"fromName"`
	To          any     `json:"to"`
	CC          any     `json:"cc"`
	Subject     *string `json:"subject"`
	HTML        *string `json:"html"`
	Text        *string `json:"text"`
	Headers     any     `json:"headers"`
	Size        int64   `json:"size"`
	ReceivedAt  int64   `json:"receivedAt"`
	ICalEvents  any     `json:"icalEvents"`
}
type attachmentInfoResponse struct {
	ID          int64   `json:"id"`
	Filename    *string `json:"filename"`
	ContentType *string `json:"contentType"`
	Size        int64   `json:"size"`
}

func decodeJSONField(value string) (any, error) {
	var result any
	err := json.Unmarshal([]byte(value), &result)
	return result, err
}

func (s *server) getMessage(response http.ResponseWriter, request *http.Request, rawID string) {
	id, ok := parseID(rawID)
	if !ok {
		writeError(response, http.StatusBadRequest, "Invalid message id")
		return
	}
	detail, err := s.store.GetMessage(request.Context(), id)
	if err != nil {
		internalError(response, err)
		return
	}
	if detail == nil {
		writeError(response, http.StatusNotFound, "Message not found")
		return
	}
	if err := s.store.MarkRead(request.Context(), id, detail.Message.MailboxID, detail.Message.IsRead); err != nil {
		internalError(response, err)
		return
	}
	to, err := decodeJSONField(detail.Message.ToJSON)
	if err != nil {
		internalError(response, err)
		return
	}
	cc, err := decodeJSONField(detail.Message.CCJSON)
	if err != nil {
		internalError(response, err)
		return
	}
	headers, err := decodeJSONField(detail.Message.HeadersJSON)
	if err != nil {
		internalError(response, err)
		return
	}
	var iCalEvents any = []any{}
	if detail.Message.ICalJSON != nil {
		iCalEvents, err = decodeJSONField(*detail.Message.ICalJSON)
		if err != nil {
			internalError(response, err)
			return
		}
	}
	html := detail.Message.HTML
	if html != nil {
		cidMap := make(map[string]int64)
		for _, attachment := range detail.Attachments {
			if attachment.ContentID != nil {
				cidMap[*attachment.ContentID] = attachment.ID
			}
		}
		clean := inspect.SanitizeEmailHTML(*html, cidMap)
		html = &clean
	}
	attachments := make([]attachmentInfoResponse, 0, len(detail.Attachments))
	for _, attachment := range detail.Attachments {
		if attachment.ContentID != nil {
			continue
		}
		contentType, filename := "", ""
		if attachment.ContentType != nil {
			contentType = *attachment.ContentType
		}
		if attachment.Filename != nil {
			filename = *attachment.Filename
		}
		if detail.Message.ICalJSON != nil && calendar.IsCalendarPart(contentType, filename) {
			continue
		}
		attachments = append(attachments, attachmentInfoResponse{attachment.ID, attachment.Filename, attachment.ContentType, attachment.Size})
	}
	message := messageResponse{detail.Message.ID, detail.Message.MailboxID, detail.Message.FromAddress, detail.Message.FromName, to, cc, detail.Message.Subject, html, detail.Message.Text, headers, detail.Message.Size, detail.Message.ReceivedAt, iCalEvents}
	writeJSON(response, http.StatusOK, struct {
		Message     messageResponse          `json:"message"`
		Attachments []attachmentInfoResponse `json:"attachments"`
	}{message, attachments})
}

func (s *server) getMessageSource(response http.ResponseWriter, request *http.Request, rawID string) {
	id, ok := parseID(rawID)
	if !ok {
		writeError(response, http.StatusBadRequest, "Invalid message id")
		return
	}
	raw, found, err := s.store.GetMessageSource(request.Context(), id)
	if err != nil {
		internalError(response, err)
		return
	}
	if !found {
		writeError(response, http.StatusNotFound, "Message not found")
		return
	}
	response.Header().Set("Cache-Control", "private, no-store")
	response.Header().Set("Content-Type", "message/rfc822")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write(raw)
}

func (s *server) inspectMessage(response http.ResponseWriter, request *http.Request, rawID string) {
	id, ok := parseID(rawID)
	if !ok {
		writeError(response, http.StatusBadRequest, "Invalid message id")
		return
	}
	row, err := s.store.GetMessageRaw(request.Context(), id)
	if err != nil {
		internalError(response, err)
		return
	}
	if row == nil {
		writeError(response, http.StatusNotFound, "Message not found")
		return
	}
	var headers map[string]string
	if err := json.Unmarshal([]byte(row.HeadersJSON), &headers); err != nil {
		internalError(response, err)
		return
	}
	report, err := inspect.Analyze(inspect.Input{
		Raw:        row.Raw,
		LegacyHTML: row.HTML,
		LegacyText: row.Text,
		StoredSize: row.Size,
	})
	if err != nil {
		internalError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, report)
}

func decodeBody(request *http.Request) (any, error) {
	decoder := json.NewDecoder(request.Body)
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("extra JSON value")
	}
	return value, nil
}

func validIDs(value any) []int64 {
	values, ok := value.([]any)
	if !ok {
		return []int64{}
	}
	ids := make([]int64, 0, len(values))
	for _, value := range values {
		number, ok := value.(float64)
		if ok && math.Trunc(number) == number && number >= math.MinInt64 && number <= math.MaxInt64 {
			ids = append(ids, int64(number))
		}
	}
	return ids
}

func (s *server) messageActions(response http.ResponseWriter, request *http.Request) {
	value, err := decodeBody(request)
	if err != nil {
		writeError(response, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	body, _ := value.(map[string]any)
	ids := validIDs(body["ids"])
	if len(ids) == 0 {
		writeError(response, http.StatusBadRequest, "No valid message ids provided")
		return
	}
	action, _ := body["action"].(string)
	switch action {
	case "delete":
		_, err = s.store.DeleteMessages(request.Context(), ids)
	case "read":
		_, err = s.store.SetReadState(request.Context(), ids, true)
	case "unread":
		_, err = s.store.SetReadState(request.Context(), ids, false)
	default:
		writeError(response, http.StatusBadRequest, "Unknown action")
		return
	}
	if err != nil {
		internalError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, map[string]bool{"ok": true})
}

var inlineAttachmentTypes = map[string]bool{
	"image/gif":  true,
	"image/jpeg": true,
	"image/png":  true,
	"image/webp": true,
	"text/csv":   true,
	"text/plain": true,
}

func normalizeAttachmentContentType(value *string) string {
	if value == nil {
		return "application/octet-stream"
	}
	raw := strings.TrimSpace(*value)
	if raw == "" {
		return "application/octet-stream"
	}
	mediaType, _, err := mime.ParseMediaType(raw)
	if err != nil {
		return "application/octet-stream"
	}
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	if mediaType == "" {
		return "application/octet-stream"
	}
	if parsed, _, err := mime.ParseMediaType(mediaType); err != nil || parsed != mediaType {
		return "application/octet-stream"
	}
	return mediaType
}

func sanitizeAttachmentFilename(value *string, id int64) string {
	fallback := "attachment-" + strconv.FormatInt(id, 10)
	if value == nil || *value == "" {
		return fallback
	}
	name := path.Base(strings.ReplaceAll(*value, `\`, "/"))
	var clean strings.Builder
	clean.Grow(len(name))
	for _, character := range name {
		if unicode.IsControl(character) || character == '/' || character == '\\' {
			continue
		}
		clean.WriteRune(character)
	}
	name = clean.String()
	if name == "" || name == "." || name == ".." {
		return fallback
	}
	return name
}

func formatAttachmentDisposition(disposition, filename string) string {
	var ascii strings.Builder
	ascii.Grow(len(filename))
	nonASCII := false
	replacing := false
	for _, character := range filename {
		switch {
		case character > unicode.MaxASCII:
			nonASCII = true
			if !replacing {
				ascii.WriteByte('_')
				replacing = true
			}
		case character == '"' || character == '\\':
			replacing = false
		default:
			ascii.WriteRune(character)
			replacing = false
		}
	}
	quoted := ascii.String()
	if quoted == "" {
		quoted = "attachment"
	}
	header := disposition + `; filename="` + quoted + `"`
	if nonASCII {
		header += "; filename*=UTF-8''" + encodeRFC5987(filename)
	}
	return header
}

func encodeRFC5987(value string) string {
	const hexadecimal = "0123456789ABCDEF"
	var encoded strings.Builder
	for index := range len(value) {
		character := value[index]
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || strings.ContainsRune("!#$&+-.^_`|~", rune(character)) {
			encoded.WriteByte(character)
			continue
		}
		encoded.WriteByte('%')
		encoded.WriteByte(hexadecimal[character>>4])
		encoded.WriteByte(hexadecimal[character&15])
	}
	return encoded.String()
}

func (s *server) getAttachment(response http.ResponseWriter, request *http.Request, rawID string) {
	response.Header().Set("X-Content-Type-Options", "nosniff")
	id, ok := parseID(rawID)
	if !ok {
		writeError(response, http.StatusBadRequest, "Invalid attachment id")
		return
	}
	attachment, err := s.store.GetAttachment(request.Context(), id)
	if err != nil {
		internalError(response, err)
		return
	}
	if attachment == nil || attachment.Content == nil {
		writeError(response, http.StatusNotFound, "Attachment not found")
		return
	}
	contentType := normalizeAttachmentContentType(attachment.ContentType)
	filename := sanitizeAttachmentFilename(attachment.Filename, attachment.ID)
	disposition := "attachment"
	if request.URL.Query().Get("download") != "1" && inlineAttachmentTypes[contentType] {
		disposition = "inline"
	}
	response.Header().Set("Content-Type", contentType)
	response.Header().Set("Content-Length", strconv.FormatInt(attachment.Size, 10))
	response.Header().Set("Content-Disposition", formatAttachmentDisposition(disposition, filename))
	response.Header().Set("Cache-Control", "private, no-store")
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write(attachment.Content)
}

func (s *server) eventStream(response http.ResponseWriter, request *http.Request) {
	flusher, ok := response.(http.Flusher)
	if !ok {
		http.Error(response, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	response.Header().Set("Content-Type", "text/event-stream")
	response.Header().Set("Cache-Control", "no-cache, no-transform")
	response.Header().Set("Connection", "keep-alive")
	response.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(response, "data: {\"type\":\"connected\"}\n\n")
	flusher.Flush()
	stream, unsubscribe := events.Subscribe()
	defer unsubscribe()
	heartbeat := time.NewTicker(25 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-request.Context().Done():
			return
		case event := <-stream:
			body, err := json.Marshal(event)
			if err != nil {
				continue
			}
			if _, err = fmt.Fprintf(response, "data: %s\n\n", body); err != nil {
				return
			}
			flusher.Flush()
		case <-heartbeat.C:
			if _, err := io.WriteString(response, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (s *server) reset(response http.ResponseWriter, request *http.Request) {
	if err := s.store.WipeAll(request.Context()); err != nil {
		internalError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, map[string]bool{"ok": true})
}

var recipientPattern = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)

func (s *server) sendTest(response http.ResponseWriter, request *http.Request) {
	body := map[string]any{}
	if value, err := decodeBody(request); err == nil {
		body, _ = value.(map[string]any)
	}
	to, _ := body["to"].(string)
	to = strings.ToLower(strings.TrimSpace(to))
	if to == "" {
		to = "test@hoomail.local"
	}
	if !recipientPattern.MatchString(to) {
		writeError(response, http.StatusBadRequest, "Invalid recipient address")
		return
	}
	kind, _ := body["kind"].(string)
	switch kind {
	case "plain", "invite", "update", "cancellation":
	default:
		kind = "plain"
	}
	subject, _ := body["subject"].(string)
	subject = strings.TrimSpace(subject)
	if s.sender == nil || s.sender.SendTest(request.Context(), SendTestRequest{To: to, Subject: subject, Kind: kind}) != nil {
		writeError(response, http.StatusBadGateway, "Could not reach the SMTP server. Is it running?")
		return
	}
	writeJSON(response, http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) serveStatic(response http.ResponseWriter, request *http.Request) {
	if s.static.FS == nil || (request.Method != http.MethodGet && request.Method != http.MethodHead) {
		http.NotFound(response, request)
		return
	}
	name := strings.TrimPrefix(path.Clean("/"+request.URL.Path), "/")
	if name == "." || name == "" {
		name = s.static.Index
	}
	data, err := fs.ReadFile(s.static.FS, name)
	if err != nil {
		data, err = fs.ReadFile(s.static.FS, s.static.Index)
		name = s.static.Index
	}
	if err != nil {
		http.NotFound(response, request)
		return
	}
	if contentType := mime.TypeByExtension(path.Ext(name)); contentType != "" {
		response.Header().Set("Content-Type", contentType)
	}
	http.ServeContent(response, request, name, time.Time{}, bytes.NewReader(data))
}
