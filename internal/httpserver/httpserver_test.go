package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/openhoo/hoomail/internal/inspect"
	"github.com/openhoo/hoomail/internal/store"
)

func testStore(t *testing.T) *store.Store {
	t.Helper()
	data, err := store.Open(t.TempDir() + "/test.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = data.Close() })
	return data
}

func request(t *testing.T, handler http.Handler, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(method, target, strings.NewReader(body)))
	return recorder
}

func assertResponse(t *testing.T, response *httptest.ResponseRecorder, status int, body string) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, status, response.Body.String())
	}
	if response.Body.String() != body {
		t.Fatalf("body = %q, want %q", response.Body.String(), body)
	}
}

func TestInvalidIDsJSONAndActions(t *testing.T) {
	handler := New(testStore(t), StaticConfig{}, nil)
	assertResponse(t, request(t, handler, http.MethodDelete, "/api/mailboxes/nope", ""), 400, `{"error":"Invalid mailbox id"}`)
	assertResponse(t, request(t, handler, http.MethodGet, "/api/messages/1.5", ""), 400, `{"error":"Invalid message id"}`)
	assertResponse(t, request(t, handler, http.MethodGet, "/api/attachments/NaN", ""), 400, `{"error":"Invalid attachment id"}`)
	assertResponse(t, request(t, handler, http.MethodPost, "/api/messages/actions", `{`), 400, `{"error":"Invalid JSON body"}`)
	assertResponse(t, request(t, handler, http.MethodPost, "/api/messages/actions", `{"action":"delete","ids":[1.2,"2"]}`), 400, `{"error":"No valid message ids provided"}`)
	assertResponse(t, request(t, handler, http.MethodPost, "/api/messages/actions", `{"action":"wat","ids":[1,null,2.5]}`), 400, `{"error":"Unknown action"}`)
}

func pointer(value string) *string { return &value }

func TestMessageDetailCasingSanitizeCIDAndCalendarFiltering(t *testing.T) {
	data := testStore(t)
	html := `<p onclick="bad()"><img src="cid:owl"><img src="https://tracker.example/pixel.png"><a href="https://safe.example/path">safe</a><a href="javascript:bad()">bad</a></p><div style="background-image:url(https://tracker.example/bg.png)">styled</div><script>alert(1)</script>`
	ical := `[{"uid":"invite"}]`
	stored, err := data.StoreMessage(context.Background(), store.StoreMessageInput{
		Recipients: []string{"box@example.com"}, FromAddress: pointer("from@example.com"), FromName: pointer("Owl"),
		To: []store.AddressEntry{{Address: "box@example.com"}}, CC: []store.AddressEntry{}, Subject: pointer("Hello"), HTML: &html,
		Headers: map[string]string{"x-test": "yes"}, Size: 42, ICalEvents: nil,
		Attachments: []store.AttachmentInput{
			{Filename: pointer("inline.png"), ContentType: pointer("image/png"), ContentID: pointer("owl"), Content: []byte("image")},
			{Filename: pointer("invite.ics"), ContentType: pointer("text/calendar"), Content: []byte("calendar")},
			{Filename: pointer("note.txt"), ContentType: pointer("text/plain"), Content: []byte("note")},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	messageID := stored[0].MessageID
	if _, err = data.DB().Exec(`UPDATE messages SET ical_json=? WHERE id=?`, ical, messageID); err != nil {
		t.Fatal(err)
	}

	response := request(t, New(data, StaticConfig{}, nil), http.MethodGet, "/api/messages/"+jsonNumber(messageID), "")
	if response.Code != 200 {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var decoded map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	message := decoded["message"].(map[string]any)
	for _, key := range []string{"mailboxId", "fromAddress", "fromName", "receivedAt", "icalEvents"} {
		if _, ok := message[key]; !ok {
			t.Errorf("missing camel-case key %q", key)
		}
	}
	for _, key := range []string{"mailbox_id", "from_address", "received_at", "ical_events"} {
		if _, ok := message[key]; ok {
			t.Errorf("unexpected snake-case key %q", key)
		}
	}
	clean := message["html"].(string)
	if strings.Contains(clean, "onclick") || strings.Contains(clean, "<script") || strings.Contains(clean, "javascript:") || strings.Contains(clean, "tracker.example") {
		t.Fatalf("unsafe or remote HTML: %s", clean)
	}
	if !strings.Contains(clean, `src="/api/attachments/`) {
		t.Fatalf("CID not rewritten: %s", clean)
	}
	if !strings.Contains(clean, `href="https://safe.example/path"`) || !strings.Contains(clean, `target="_blank"`) || !strings.Contains(clean, `rel="noopener noreferrer"`) {
		t.Fatalf("safe link not externalized: %s", clean)
	}
	attachments := decoded["attachments"].([]any)
	if len(attachments) != 1 || attachments[0].(map[string]any)["filename"] != "note.txt" {
		t.Fatalf("attachments=%v", attachments)
	}
	var read int
	if err := data.DB().QueryRow(`SELECT is_read FROM messages WHERE id=?`, messageID).Scan(&read); err != nil || read != 1 {
		t.Fatalf("read=%d err=%v", read, err)
	}
}

func TestMessageSourceReturnsExactRawWithoutMarkingRead(t *testing.T) {
	data := testStore(t)
	handler := New(data, StaticConfig{}, nil)
	assertResponse(t, request(t, handler, http.MethodGet, "/api/messages/nope/source", ""), 400, `{"error":"Invalid message id"}`)
	assertResponse(t, request(t, handler, http.MethodGet, "/api/messages/8/source", ""), 404, `{"error":"Message not found"}`)

	raw := []byte("From: Sender <sender@example.test>\r\nTo: source@example.test\r\nSubject: Full source\r\nMIME-Version: 1.0\r\nContent-Type: multipart/alternative; boundary=source-boundary\r\n\r\n--source-boundary\r\nContent-Type: text/plain\r\n\r\ncomplete body\r\n--source-boundary--\r\n")
	stored, err := data.StoreMessage(context.Background(), store.StoreMessageInput{
		Recipients: []string{"source@example.test"},
		To:         []store.AddressEntry{},
		CC:         []store.AddressEntry{},
		Headers:    map[string]string{"subject": "Full source"},
		Raw:        raw,
	})
	if err != nil {
		t.Fatal(err)
	}
	id := stored[0].MessageID
	response := request(t, handler, http.MethodGet, "/api/messages/"+jsonNumber(id)+"/source", "")
	if response.Code != http.StatusOK || response.Body.String() != string(raw) {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	if response.Header().Get("Content-Type") != "message/rfc822" ||
		response.Header().Get("Cache-Control") != "private, no-store" ||
		response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("headers=%v", response.Header())
	}
	var read int
	if err := data.DB().QueryRow(`SELECT is_read FROM messages WHERE id=?`, id).Scan(&read); err != nil || read != 0 {
		t.Fatalf("read=%d err=%v", read, err)
	}
}

func jsonNumber(value int64) string { return strconv.FormatInt(value, 10) }

func TestAttachmentHeaders(t *testing.T) {
	data := testStore(t)
	handler := New(data, StaticConfig{}, nil)

	tests := []struct {
		name            string
		filename        string
		contentType     string
		content         string
		wantType        string
		wantDisposition string
	}{
		{"raster image inline", "logo.png", " IMAGE/PNG ; name=logo.png", "image", "image/png", `inline; filename="logo.png"`},
		{"jpeg image inline", "photo.jpg", "image/jpeg", "jpeg", "image/jpeg", `inline; filename="photo.jpg"`},
		{"gif image inline", "motion.gif", "image/gif", "gif", "image/gif", `inline; filename="motion.gif"`},
		{"webp image inline", "photo.webp", "image/webp", "webp", "image/webp", `inline; filename="photo.webp"`},
		{"plain text inline", "note.txt", "Text/Plain; charset=utf-8", "plain", "text/plain", `inline; filename="note.txt"`},
		{"csv inline", "data.csv", "text/csv; charset=utf-8", "a,b", "text/csv", `inline; filename="data.csv"`},
		{"pdf download only", "document.pdf", "application/pdf", "%PDF", "application/pdf", `attachment; filename="document.pdf"`},
		{"html download only", "page.html", "text/html; charset=utf-8", "<script>alert(1)</script>", "text/html", `attachment; filename="page.html"`},
		{"svg download only", "vector.svg", "image/svg+xml", "<svg onload='alert(1)'></svg>", "image/svg+xml", `attachment; filename="vector.svg"`},
		{"xhtml download only", "page.xhtml", "application/xhtml+xml", "<script>alert(1)</script>", "application/xhtml+xml", `attachment; filename="page.xhtml"`},
		{"xml download only", "page.xml", "application/xml", "<script>alert(1)</script>", "application/xml", `attachment; filename="page.xml"`},
		{"mhtml download only", "page.mhtml", "multipart/related", "active", "multipart/related", `attachment; filename="page.mhtml"`},
		{"javascript download only", "page.js", "text/javascript", "alert(1)", "text/javascript", `attachment; filename="page.js"`},
		{"mislabeled active download only", "picture.png", " TeXt/HtMl ; charset=utf-8", "<script>alert(1)</script>", "text/html", `attachment; filename="picture.png"`},
		{"unknown download only", "payload.bin", "not a media type", "<script>alert(1)</script>", "application/octet-stream", `attachment; filename="payload.bin"`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			attachmentID := storeAttachment(t, data, test.filename, test.contentType, test.content)
			response := request(t, handler, http.MethodGet, "/api/attachments/"+jsonNumber(attachmentID), "")
			if response.Code != http.StatusOK || response.Body.String() != test.content {
				t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
			}
			if got := response.Header().Get("Content-Type"); got != test.wantType {
				t.Errorf("Content-Type=%q, want %q", got, test.wantType)
			}
			if got := response.Header().Get("Content-Disposition"); got != test.wantDisposition {
				t.Errorf("Content-Disposition=%q, want %q", got, test.wantDisposition)
			}
			if got := response.Header().Get("X-Content-Type-Options"); got != "nosniff" {
				t.Errorf("X-Content-Type-Options=%q", got)
			}
			if got := response.Header().Get("Content-Length"); got != strconv.Itoa(len(test.content)) {
				t.Errorf("Content-Length=%q", got)
			}
			if got := response.Header().Get("Cache-Control"); got != "private, no-store" {
				t.Errorf("Cache-Control=%q", got)
			}
		})
	}
}

func TestAttachmentDownloadAndSafeUnicodeFilename(t *testing.T) {
	data := testStore(t)
	attachmentID := storeAttachment(t, data, "../ignored/\x00Résumé Q.txt", "text/plain", "hoot")
	handler := New(data, StaticConfig{}, nil)

	response := request(t, handler, http.MethodGet, "/api/attachments/"+jsonNumber(attachmentID), "")
	if got := response.Header().Get("Content-Disposition"); got != `inline; filename="R_sum_ Q.txt"; filename*=UTF-8''R%C3%A9sum%C3%A9%20Q.txt` {
		t.Fatalf("Content-Disposition=%q", got)
	}
	if strings.ContainsAny(response.Header().Get("Content-Disposition"), "\x00\r\n") || strings.Contains(response.Header().Get("Content-Disposition"), "../") {
		t.Fatalf("unsafe Content-Disposition=%q", response.Header().Get("Content-Disposition"))
	}

	download := request(t, handler, http.MethodGet, "/api/attachments/"+jsonNumber(attachmentID)+"?download=1", "")
	if got := download.Header().Get("Content-Disposition"); !strings.HasPrefix(got, "attachment;") {
		t.Fatalf("download Content-Disposition=%q", got)
	}
	if got := download.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("download X-Content-Type-Options=%q", got)
	}
}

func storeAttachment(t *testing.T, data *store.Store, filename, contentType, content string) int64 {
	t.Helper()
	stored, err := data.StoreMessage(context.Background(), store.StoreMessageInput{
		Recipients: []string{"a@example.com"},
		To:         []store.AddressEntry{},
		CC:         []store.AddressEntry{},
		Headers:    map[string]string{},
		Attachments: []store.AttachmentInput{{
			Filename:    pointer(filename),
			ContentType: pointer(contentType),
			Content:     []byte(content),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var attachmentID int64
	if err := data.DB().QueryRow(`SELECT id FROM attachments WHERE message_id=?`, stored[0].MessageID).Scan(&attachmentID); err != nil {
		t.Fatal(err)
	}
	return attachmentID
}

func TestSSEHello(t *testing.T) {
	server := httptest.NewServer(New(testStore(t), StaticConfig{}, nil))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/events", nil)
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	buffer := make([]byte, len("data: {\"type\":\"connected\"}\n\n"))
	if _, err := io.ReadFull(response.Body, buffer); err != nil {
		t.Fatal(err)
	}
	if string(buffer) != "data: {\"type\":\"connected\"}\n\n" {
		t.Fatalf("hello=%q", buffer)
	}
	if response.Header.Get("Content-Type") != "text/event-stream" || response.Header.Get("Cache-Control") != "no-cache, no-transform" {
		t.Fatalf("headers=%v", response.Header)
	}
}

func TestStaticSPAFallback(t *testing.T) {
	static := fstest.MapFS{"index.html": {Data: []byte("<main>app</main>")}, "assets/app.js": {Data: []byte("js")}}
	handler := New(testStore(t), StaticConfig{FS: static}, nil)
	assertResponse(t, request(t, handler, http.MethodGet, "/assets/app.js", ""), 200, "js")
	fallback := request(t, handler, http.MethodGet, "/mailboxes/12", "")
	assertResponse(t, fallback, 200, "<main>app</main>")
	if !strings.HasPrefix(fallback.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("content type=%q", fallback.Header().Get("Content-Type"))
	}
	assertResponse(t, request(t, handler, http.MethodGet, "/api/not-real", ""), 404, "404 page not found\n")
}

type recordingSender struct {
	request SendTestRequest
	err     error
}

func (sender *recordingSender) SendTest(_ context.Context, request SendTestRequest) error {
	sender.request = request
	return sender.err
}

func TestSendTestValidationDefaultsAndError(t *testing.T) {
	sender := &recordingSender{}
	handler := New(testStore(t), StaticConfig{}, sender)
	assertResponse(t, request(t, handler, http.MethodPost, "/api/send-test", `{"to":"bad"}`), 400, `{"error":"Invalid recipient address"}`)
	assertResponse(t, request(t, handler, http.MethodPost, "/api/send-test", `{`), 200, `{"ok":true}`)
	if sender.request != (SendTestRequest{To: "test@hoomail.local", Kind: "plain"}) {
		t.Fatalf("defaults=%+v", sender.request)
	}
	assertResponse(t, request(t, handler, http.MethodPost, "/api/send-test", `{"to":" USER@Example.COM ","subject":" Hi ","kind":"update"}`), 200, `{"ok":true}`)
	if sender.request != (SendTestRequest{To: "user@example.com", Subject: "Hi", Kind: "update"}) {
		t.Fatalf("parsed=%+v", sender.request)
	}
	sender.err = errors.New("smtp unavailable")
	assertResponse(t, request(t, handler, http.MethodPost, "/api/send-test", `{}`), 502, `{"error":"Could not reach the SMTP server. Is it running?"}`)
}

func TestInspectNotFoundAndReportShape(t *testing.T) {
	data := testStore(t)
	handler := New(data, StaticConfig{}, nil)
	assertResponse(t, request(t, handler, http.MethodGet, "/api/messages/nope/inspect", ""), 400, `{"error":"Invalid message id"}`)
	assertResponse(t, request(t, handler, http.MethodGet, "/api/messages/8/inspect", ""), 404, `{"error":"Message not found"}`)
	stored, err := data.StoreMessage(context.Background(), store.StoreMessageInput{
		Recipients: []string{"inspect@example.com"},
		To:         []store.AddressEntry{},
		CC:         []store.AddressEntry{},
		Headers:    map[string]string{"subject": "Hello"},
		Text:       pointer("visit https://example.com"),
		Raw:        []byte("Date: Thu, 24 Jul 2026 12:00:00 +0000\r\nFrom: sender@example.com\r\nTo: inspect@example.com\r\nMessage-ID: <inspect@example.com>\r\nSubject: Hello\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nvisit https://example.com"),
	})
	if err != nil {
		t.Fatal(err)
	}
	response := request(t, handler, http.MethodGet, "/api/messages/"+jsonNumber(stored[0].MessageID)+"/inspect", "")
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if contentType := response.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("content type=%q", contentType)
	}
	var report inspect.Report
	if err := json.Unmarshal(response.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Analysis.Version != 1 || report.Analysis.State != "complete" {
		t.Fatalf("analysis=%+v", report.Analysis)
	}
	if report.MIMETree == nil || len(report.Findings) == 0 || report.Resources == nil {
		t.Fatalf("report shape=%+v", report)
	}
	var read int
	if err := data.DB().QueryRow(`SELECT is_read FROM messages WHERE id=?`, stored[0].MessageID).Scan(&read); err != nil || read != 0 {
		t.Fatalf("read=%d err=%v", read, err)
	}
}

func TestInspectRawlessPartialAndCorruptHeaders(t *testing.T) {
	data := testStore(t)
	handler := New(data, StaticConfig{}, nil)
	legacyHTML := `<p>Legacy <a href="https://example.com">link</a></p>`
	stored, err := data.StoreMessage(context.Background(), store.StoreMessageInput{
		Recipients: []string{"legacy@example.com"},
		To:         []store.AddressEntry{},
		CC:         []store.AddressEntry{},
		Headers:    map[string]string{"subject": "Legacy"},
		HTML:       &legacyHTML,
	})
	if err != nil {
		t.Fatal(err)
	}
	id := stored[0].MessageID
	response := request(t, handler, http.MethodGet, "/api/messages/"+jsonNumber(id)+"/inspect", "")
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var report inspect.Report
	if err := json.Unmarshal(response.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Analysis.State != "partial" || report.MIMETree != nil {
		t.Fatalf("legacy analysis=%+v mime=%+v", report.Analysis, report.MIMETree)
	}
	found := false
	for _, finding := range report.Findings {
		if finding.ID == "analysis.raw-unavailable" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("missing raw-unavailable finding: %+v", report.Findings)
	}
	if _, err := data.DB().Exec(`UPDATE messages SET headers_json='{' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	assertResponse(t, request(t, handler, http.MethodGet, "/api/messages/"+jsonNumber(id)+"/inspect", ""), 500, "Internal Server Error\n")
}

func TestSSERequestReturnsPromptlyAfterCancel(t *testing.T) {
	server := httptest.NewServer(New(testStore(t), StaticConfig{}, nil))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/events", nil)
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	_ = response.Body.Close()
	select {
	case <-time.After(100 * time.Millisecond):
	default:
	}
}
