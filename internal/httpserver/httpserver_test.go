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
	html := `<p onclick="bad()"><img src="cid:owl"><a href="javascript:bad()">go</a></p><script>alert(1)</script>`
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
	if strings.Contains(clean, "onclick") || strings.Contains(clean, "<script") || strings.Contains(clean, "javascript:") {
		t.Fatalf("unsafe html: %s", clean)
	}
	if !strings.Contains(clean, `src="/api/attachments/`) {
		t.Fatalf("CID not rewritten: %s", clean)
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

func jsonNumber(value int64) string { return strconv.FormatInt(value, 10) }

func TestAttachmentHeaders(t *testing.T) {
	data := testStore(t)
	stored, err := data.StoreMessage(context.Background(), store.StoreMessageInput{Recipients: []string{"a@example.com"}, To: []store.AddressEntry{}, CC: []store.AddressEntry{}, Headers: map[string]string{}, Attachments: []store.AttachmentInput{{Filename: pointer(`quo"te\\.txt`), ContentType: pointer("text/plain"), Content: []byte("hoot")}}})
	if err != nil {
		t.Fatal(err)
	}
	var attachmentID int64
	if err := data.DB().QueryRow(`SELECT id FROM attachments WHERE message_id=?`, stored[0].MessageID).Scan(&attachmentID); err != nil {
		t.Fatal(err)
	}
	handler := New(data, StaticConfig{}, nil)
	response := request(t, handler, http.MethodGet, "/api/attachments/"+jsonNumber(attachmentID), "")
	if response.Code != 200 || response.Body.String() != "hoot" {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Content-Disposition"); got != `inline; filename="quote.txt"` {
		t.Fatalf("disposition=%q", got)
	}
	if response.Header().Get("Content-Type") != "text/plain" || response.Header().Get("Content-Length") != "4" || response.Header().Get("Cache-Control") != "private, max-age=3600" {
		t.Fatalf("headers=%v", response.Header())
	}
	download := request(t, handler, http.MethodGet, "/api/attachments/"+jsonNumber(attachmentID)+"?download=1", "")
	if got := download.Header().Get("Content-Disposition"); !strings.HasPrefix(got, "attachment;") {
		t.Fatalf("download disposition=%q", got)
	}
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

func TestInspectNotFoundAndShape(t *testing.T) {
	data := testStore(t)
	handler := New(data, StaticConfig{}, nil)
	assertResponse(t, request(t, handler, http.MethodGet, "/api/messages/8/inspect", ""), 404, `{"error":"Message not found"}`)
	stored, err := data.StoreMessage(context.Background(), store.StoreMessageInput{Recipients: []string{"inspect@example.com"}, To: []store.AddressEntry{}, CC: []store.AddressEntry{}, Headers: map[string]string{"subject": "Hello"}, Text: pointer("visit https://example.com"), Raw: []byte("Subject: Hello\r\nContent-Type: text/plain\r\n\r\nbody")})
	if err != nil {
		t.Fatal(err)
	}
	response := request(t, handler, http.MethodGet, "/api/messages/"+jsonNumber(stored[0].MessageID)+"/inspect", "")
	if response.Code != 200 {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"mimeTree", "links", "checks"} {
		if _, ok := body[key]; !ok {
			t.Errorf("missing %s", key)
		}
	}
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
