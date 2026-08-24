package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/openhoo/hoomail/internal/events"
	"github.com/openhoo/hoomail/internal/inspect"
	"github.com/openhoo/hoomail/internal/store"
	"github.com/openhoo/hoomail/internal/version"
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

func TestGeneratedOpenAPIAndSwaggerEndpoints(t *testing.T) {
	handler := New(testStore(t), StaticConfig{}, nil)

	openAPI := request(t, handler, http.MethodGet, "/openapi.json", "")
	if openAPI.Code != http.StatusOK {
		t.Fatalf("openapi status=%d body=%s", openAPI.Code, openAPI.Body.String())
	}
	if openAPI.Header().Get("Content-Type") != "application/json" ||
		openAPI.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("openapi headers=%v", openAPI.Header())
	}
	var document struct {
		OpenAPI string `json:"openapi"`
		Info    struct {
			Version string `json:"version"`
		} `json:"info"`
		Paths      map[string]map[string]any `json:"paths"`
		Components struct {
			Schemas map[string]struct {
				Properties map[string]any `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(openAPI.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if document.OpenAPI != "3.0.3" {
		t.Fatalf("openapi=%q", document.OpenAPI)
	}
	if document.Info.Version != version.Value {
		t.Fatalf("openapi version=%q want=%q", document.Info.Version, version.Value)
	}
	expected := map[string]string{
		"/api/mailboxes":                      "get",
		"/api/mailboxes/{mailboxId}":          "delete",
		"/api/mailboxes/{mailboxId}/messages": "get",
		"/api/mailboxes/{mailboxId}/events":   "get",
		"/api/messages/actions":               "post",
		"/api/messages/{messageId}":           "get",
		"/api/messages/{messageId}/source":    "get",
		"/api/messages/{messageId}/inspect":   "get",
		"/api/attachments/{attachmentId}":     "get",
		"/api/events":                         "get",
		"/api/reset":                          "post",
		"/api/send-test":                      "post",
	}
	eventSchema, ok := document.Components.Schemas["Event"]
	if !ok {
		t.Fatal("openapi Event schema missing")
	}
	typeSchema, ok := eventSchema.Properties["type"].(map[string]any)
	if !ok {
		t.Fatalf("Event.type schema=%#v", eventSchema.Properties["type"])
	}
	enum, ok := typeSchema["enum"].([]any)
	if !ok {
		t.Fatalf("Event.type enum=%#v", typeSchema["enum"])
	}
	foundConnected := false
	for _, value := range enum {
		if value == "connected" {
			foundConnected = true
			break
		}
	}
	if !foundConnected {
		t.Fatalf("Event.type enum missing connected: %#v", enum)
	}
	if len(document.Paths) != len(expected) {
		t.Fatalf("documented paths=%d want=%d", len(document.Paths), len(expected))
	}
	for path, method := range expected {
		if document.Paths[path][method] == nil {
			t.Errorf("missing %s %s", method, path)
		}
	}
	reportSchema := document.Components.Schemas["InspectionReport"]
	for _, property := range []string{"headers", "htmlCompatibility", "mimeTree"} {
		if reportSchema.Properties[property] == nil {
			t.Errorf("inspection report schema missing %s", property)
		}
	}
	mimeSchema := document.Components.Schemas["MimeNode"]
	if mimeSchema.Properties["checksums"] == nil {
		t.Error("MIME node schema missing checksums")
	}
	actionSchema := document.Components.Schemas["MessageAction"]
	idsSchema, ok := actionSchema.Properties["ids"].(map[string]any)
	if !ok || idsSchema["minItems"] != float64(1) || idsSchema["maxItems"] != float64(maxMessageActionIDs) {
		t.Fatalf("MessageAction.ids schema=%#v", actionSchema.Properties["ids"])
	}
	listOperation, ok := document.Paths["/api/mailboxes/{mailboxId}/messages"]["get"].(map[string]any)
	if !ok {
		t.Fatal("messages GET operation missing")
	}
	parameters, ok := listOperation["parameters"].([]any)
	if !ok || len(parameters) != 2 {
		t.Fatalf("messages parameters=%#v", listOperation["parameters"])
	}
	queryParameter, ok := parameters[1].(map[string]any)
	querySchema, schemaOK := queryParameter["schema"].(map[string]any)
	if !ok || !schemaOK || querySchema["maxLength"] != float64(maxSearchPatternBytes) {
		t.Fatalf("messages q schema=%#v", parameters[1])
	}

	head := request(t, handler, http.MethodHead, "/openapi.json", "")
	if head.Code != http.StatusOK || head.Body.Len() != 0 {
		t.Fatalf("openapi HEAD status=%d body=%q", head.Code, head.Body.String())
	}
	disallowed := request(t, handler, http.MethodPost, "/openapi.json", "")
	if disallowed.Code != http.StatusMethodNotAllowed || disallowed.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("openapi POST status=%d headers=%v", disallowed.Code, disallowed.Header())
	}

	redirect := request(t, handler, http.MethodGet, "/swagger", "")
	if redirect.Code != http.StatusPermanentRedirect || redirect.Header().Get("Location") != "/swagger/" {
		t.Fatalf("swagger redirect status=%d location=%q", redirect.Code, redirect.Header().Get("Location"))
	}
	swagger := request(t, handler, http.MethodGet, "/swagger/", "")
	if swagger.Code != http.StatusOK ||
		!strings.Contains(swagger.Body.String(), `url:"/openapi.json"`) ||
		!strings.Contains(swagger.Body.String(), "swagger-ui-dist@5.11.0") {
		t.Fatalf("swagger status=%d body=%q", swagger.Code, swagger.Body.String())
	}
	if swagger.Header().Get("Content-Security-Policy") == "" ||
		swagger.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("swagger headers=%v", swagger.Header())
	}
	swaggerHead := request(t, handler, http.MethodHead, "/swagger/", "")
	if swaggerHead.Code != http.StatusOK || swaggerHead.Body.Len() != 0 {
		t.Fatalf("swagger HEAD status=%d body=%q", swaggerHead.Code, swaggerHead.Body.String())
	}
}

func TestGeneratedOpenAPIAttachmentContract(t *testing.T) {
	var document struct {
		Paths map[string]map[string]struct {
			Parameters []struct {
				Name   string `json:"name"`
				In     string `json:"in"`
				Schema struct {
					Enum []string `json:"enum"`
				} `json:"schema"`
			} `json:"parameters"`
			Responses map[string]struct {
				Content map[string]struct {
					Schema struct {
						Ref string `json:"$ref"`
					} `json:"schema"`
				} `json:"content"`
			} `json:"responses"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(generatedOpenAPI, &document); err != nil {
		t.Fatal(err)
	}
	operation := document.Paths["/api/attachments/{attachmentId}"]["get"]
	var inlineValues []string
	for _, parameter := range operation.Parameters {
		if parameter.Name == "inline" && parameter.In == "query" {
			inlineValues = parameter.Schema.Enum
			break
		}
	}
	if len(inlineValues) != 1 || inlineValues[0] != "cid" {
		t.Fatalf("inline query enum=%v, want [cid]", inlineValues)
	}
	response, ok := operation.Responses["422"]
	if !ok {
		t.Fatal("attachment GET has no 422 response")
	}
	if got := response.Content["application/json"].Schema.Ref; got != "#/components/schemas/Error" {
		t.Fatalf("attachment 422 schema ref=%q, want Error", got)
	}
}

func TestGeneratedOpenAPINullableReferencesAndCalendarAttendeeContract(t *testing.T) {
	var document map[string]any
	if err := json.Unmarshal(generatedOpenAPI, &document); err != nil {
		t.Fatal(err)
	}
	var walk func(any, string)
	walk = func(value any, path string) {
		switch node := value.(type) {
		case map[string]any:
			if _, hasRef := node["$ref"]; hasRef {
				if _, hasNullable := node["nullable"]; hasNullable {
					t.Errorf("%s has nullable beside $ref", path)
				}
			}
			for key, child := range node {
				walk(child, path+"."+key)
			}
		case []any:
			for index, child := range node {
				walk(child, fmt.Sprintf("%s[%d]", path, index))
			}
		}
	}
	walk(document, "openapi")

	components := document["components"].(map[string]any)
	schemas := components["schemas"].(map[string]any)
	assertNullableRef := func(schemaName, propertyName, target string) {
		t.Helper()
		schema := schemas[schemaName].(map[string]any)
		properties := schema["properties"].(map[string]any)
		property := properties[propertyName].(map[string]any)
		if property["nullable"] != true {
			t.Errorf("%s.%s nullable=%v, want true", schemaName, propertyName, property["nullable"])
		}
		allOf := property["allOf"].([]any)
		if len(allOf) != 1 || allOf[0].(map[string]any)["$ref"] != "#/components/schemas/"+target {
			t.Errorf("%s.%s allOf=%v, want a single ref to %s", schemaName, propertyName, allOf, target)
		}
		if _, hasRef := property["$ref"]; hasRef {
			t.Errorf("%s.%s must not have a direct $ref", schemaName, propertyName)
		}
	}
	assertNullableRef("Finding", "reference", "Reference")
	assertNullableRef("InspectionReport", "mimeTree", "MimeNode")
	assertNullableRef("InspectionReport", "htmlCompatibility", "HTMLCompatibility")

	attendee := schemas["CalendarAttendee"].(map[string]any)
	required := attendee["required"].([]any)
	if len(required) != 1 || required[0] != "address" {
		t.Fatalf("CalendarAttendee required=%v, want [address]", required)
	}
	attendeeProperties := attendee["properties"].(map[string]any)
	for _, field := range []string{"address", "name", "partstat", "role"} {
		fieldSchema, ok := attendeeProperties[field].(map[string]any)
		if !ok || fieldSchema["type"] != "string" {
			t.Errorf("CalendarAttendee.%s=%v, want string schema", field, attendeeProperties[field])
		}
	}
	event := schemas["CalendarEvent"].(map[string]any)
	eventProperties := event["properties"].(map[string]any)
	attendees := eventProperties["attendees"].(map[string]any)
	items := attendees["items"].(map[string]any)
	if items["$ref"] != "#/components/schemas/CalendarAttendee" {
		t.Fatalf("CalendarEvent.attendees.items=%v, want CalendarAttendee ref", items)
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
	assertResponse(t, request(t, handler, http.MethodGet, "/api/messages/9223372036854775807", ""), 404, `{"error":"Message not found"}`)
	assertResponse(t, request(t, handler, http.MethodGet, "/api/messages/9223372036854775808", ""), 400, `{"error":"Invalid message id"}`)
}

func TestJSONBodyLimitAndExactIntegerIDs(t *testing.T) {
	handler := New(testStore(t), StaticConfig{}, nil)
	oversized := `{"action":"read","ids":[1],"padding":"` + strings.Repeat("x", 1<<20) + `"}`
	assertResponse(t, request(t, handler, http.MethodPost, "/api/messages/actions", oversized), http.StatusBadRequest, `{"error":"Invalid JSON body"}`)

	data := testStore(t)
	stored, err := data.StoreMessage(context.Background(), store.StoreMessageInput{
		Recipients: []string{"exact@example.com"},
		To:         []store.AddressEntry{},
		CC:         []store.AddressEntry{},
		Headers:    map[string]string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	id := stored[0].MessageID
	if got := validIDs([]any{json.Number("9223372036854775807"), json.Number("9223372036854775808"), json.Number("9007199254740992.0"), json.Number("9007199254740993")}); !equalInt64s(got, []int64{9223372036854775807, 9007199254740993}) {
		t.Fatalf("validIDs=%v", got)
	}
	assertResponse(t, request(t, New(data, StaticConfig{}, nil), http.MethodPost, "/api/messages/actions", `{"action":"read","ids":[`+jsonNumber(id)+`]}`), http.StatusOK, `{"ok":true}`)
}

func equalInt64s(left, right []int64) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func TestMessageActionsDeduplicateAndCapIDs(t *testing.T) {
	data := testStore(t)
	stored, err := data.StoreMessage(context.Background(), store.StoreMessageInput{
		Recipients: []string{"actions@example.com"},
		To:         []store.AddressEntry{},
		CC:         []store.AddressEntry{},
		Headers:    map[string]string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	id := stored[0].MessageID
	handler := New(data, StaticConfig{}, nil)

	duplicateBody := `{"action":"read","ids":[` + jsonNumber(id) + `,` + jsonNumber(id) + `]}`
	assertResponse(t, request(t, handler, http.MethodPost, "/api/messages/actions", duplicateBody), http.StatusOK, `{"ok":true}`)
	var read int
	if err := data.DB().QueryRow(`SELECT is_read FROM messages WHERE id=?`, id).Scan(&read); err != nil {
		t.Fatal(err)
	}
	if read != 1 {
		t.Fatalf("is_read=%d, want 1", read)
	}

	values := make([]string, maxMessageActionIDs+1)
	for index := range values {
		values[index] = strconv.Itoa(index + 1)
	}
	oversizedBody := `{"action":"read","ids":[` + strings.Join(values, ",") + `]}`
	assertResponse(t, request(t, handler, http.MethodPost, "/api/messages/actions", oversizedBody), http.StatusBadRequest, `{"error":"Too many message ids provided; maximum is 10000"}`)
}

func TestSearchQueryLimit(t *testing.T) {
	data := testStore(t)
	if _, err := data.StoreMessage(context.Background(), store.StoreMessageInput{
		Recipients: []string{"search@example.com"},
		To:         []store.AddressEntry{},
		CC:         []store.AddressEntry{},
		Headers:    map[string]string{"subject": "search"},
	}); err != nil {
		t.Fatal(err)
	}
	var mailboxID int64
	if err := data.DB().QueryRow(`SELECT id FROM mailboxes LIMIT 1`).Scan(&mailboxID); err != nil {
		t.Fatal(err)
	}
	handler := New(data, StaticConfig{}, nil)
	target := func(query string) string {
		return "/api/mailboxes/" + jsonNumber(mailboxID) + "/messages?q=" + url.QueryEscape(query)
	}
	tests := []struct {
		name   string
		query  string
		status int
		body   string
	}{
		{"boundary", strings.Repeat("a", maxSearchPatternBytes), http.StatusOK, `{"messages":[]}`},
		{"raw over limit", strings.Repeat("a", maxSearchPatternBytes+1), http.StatusBadRequest, `{"error":"Search query too long"}`},
		{"escaped over limit", strings.Repeat("%", maxSearchPatternBytes/2+1), http.StatusBadRequest, `{"error":"Search query too long"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertResponse(t, request(t, handler, http.MethodGet, target(test.query), ""), test.status, test.body)
		})
	}
}

func TestMessageDetailCorruptJSONDoesNotMarkRead(t *testing.T) {
	data := testStore(t)
	stored, err := data.StoreMessage(context.Background(), store.StoreMessageInput{
		Recipients: []string{"corrupt@example.com"},
		To:         []store.AddressEntry{},
		CC:         []store.AddressEntry{},
		Headers:    map[string]string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	id := stored[0].MessageID
	if _, err := data.DB().Exec(`UPDATE messages SET to_json='{' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	assertResponse(t, request(t, New(data, StaticConfig{}, nil), http.MethodGet, "/api/messages/"+jsonNumber(id), ""), http.StatusInternalServerError, "Internal Server Error\n")
	var read int
	if err := data.DB().QueryRow(`SELECT is_read FROM messages WHERE id=?`, id).Scan(&read); err != nil {
		t.Fatal(err)
	}
	if read != 0 {
		t.Fatalf("read=%d", read)
	}
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

func TestAttachmentLookupHeadersAndEmptyContent(t *testing.T) {
	data := testStore(t)
	handler := New(data, StaticConfig{}, nil)

	missing := request(t, handler, http.MethodGet, "/api/attachments/999999", "")
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing status=%d", missing.Code)
	}
	if got := missing.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("missing nosniff=%q", got)
	}
	if got := missing.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Errorf("missing cache-control=%q", got)
	}

	invalid := request(t, handler, http.MethodGet, "/api/attachments/nope", "")
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid status=%d", invalid.Code)
	}
	if got := invalid.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Errorf("invalid cache-control=%q", got)
	}

	stored, err := data.StoreMessage(context.Background(), store.StoreMessageInput{
		Recipients: []string{"empty@example.com"},
		To:         []store.AddressEntry{},
		CC:         []store.AddressEntry{},
		Headers:    map[string]string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := data.DB().Exec(`INSERT INTO attachments (message_id, size, content) VALUES (?, 0, NULL)`, stored[0].MessageID)
	if err != nil {
		t.Fatal(err)
	}
	attachmentID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	empty := request(t, handler, http.MethodGet, "/api/attachments/"+jsonNumber(attachmentID), "")
	if empty.Code != http.StatusOK || empty.Body.Len() != 0 {
		t.Fatalf("empty status=%d body=%q", empty.Code, empty.Body.String())
	}
	if got := empty.Header().Get("Content-Length"); got != "0" {
		t.Errorf("empty content-length=%q", got)
	}
	if got := empty.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Errorf("empty cache-control=%q", got)
	}
}

func TestMarkedInlineSVGIsSanitizedAndDirectDownloadRemainsRaw(t *testing.T) {
	data := testStore(t)
	raw := `<?xml version="1.0" encoding="UTF-8"?><svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"><script>alert(2)</script><style>*{background:url(https://evil.invalid/x)}</style><foreignObject><body>bad</body></foreignObject><rect id="safe" width="10" height="10" fill="red"/><use href="#safe"/><use href="https://evil.invalid/x"/></svg>`
	attachmentID := storeAttachment(t, data, "vector.svg", "image/svg+xml", raw)
	handler := New(data, StaticConfig{}, nil)

	inline := request(t, handler, http.MethodGet, "/api/attachments/"+jsonNumber(attachmentID)+"?inline=cid", "")
	if inline.Code != http.StatusOK {
		t.Fatalf("inline status=%d body=%s", inline.Code, inline.Body.String())
	}
	body := inline.Body.String()
	if strings.Contains(body, "<script") || strings.Contains(body, "<style") || strings.Contains(body, "foreignObject") || strings.Contains(body, "evil.invalid") || strings.Contains(body, "onload") {
		t.Fatalf("unsafe SVG survived: %s", body)
	}
	if !strings.Contains(body, `id="safe"`) || !strings.Contains(body, `href="#safe"`) {
		t.Fatalf("safe SVG content missing: %s", body)
	}
	if got := inline.Header().Get("Content-Type"); got != "image/svg+xml" {
		t.Errorf("Content-Type=%q", got)
	}
	if got := inline.Header().Get("Content-Disposition"); !strings.HasPrefix(got, "inline;") {
		t.Errorf("Content-Disposition=%q", got)
	}
	if got := inline.Header().Get("Content-Security-Policy"); !strings.Contains(got, "script-src 'none'") {
		t.Errorf("CSP=%q", got)
	}
	if got := inline.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Errorf("Cache-Control=%q", got)
	}

	download := request(t, handler, http.MethodGet, "/api/attachments/"+jsonNumber(attachmentID), "")
	if download.Body.String() != raw {
		t.Fatalf("direct SVG was sanitized: %q", download.Body.String())
	}
	if got := download.Header().Get("Content-Disposition"); !strings.HasPrefix(got, "attachment;") {
		t.Errorf("direct Content-Disposition=%q", got)
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

func TestSSEClosedSubscriptionReturns(t *testing.T) {
	stream := make(chan events.Event)
	handler := &server{
		store: testStore(t),
		subscribe: func() (<-chan events.Event, func()) {
			return stream, func() {}
		},
	}
	request := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	recorder := httptest.NewRecorder()
	close(stream)
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(recorder, request)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("SSE handler did not return after subscription closed")
	}
	if !strings.HasPrefix(recorder.Body.String(), "data: {\"type\":\"connected\"}\n\n") {
		t.Fatalf("body=%q", recorder.Body.String())
	}
}

func TestStaticSPAFallback(t *testing.T) {
	static := fstest.MapFS{"index.html": {Data: []byte("<main>app</main>")}, "assets/app.js": {Data: []byte("js")}}
	handler := New(testStore(t), StaticConfig{FS: static}, nil)
	asset := request(t, handler, http.MethodGet, "/assets/app.js", "")
	assertResponse(t, asset, 200, "js")
	if got := asset.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("asset cache control=%q", got)
	}
	fallback := request(t, handler, http.MethodGet, "/mailboxes/12", "")
	assertResponse(t, fallback, 200, "<main>app</main>")
	if !strings.HasPrefix(fallback.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("content type=%q", fallback.Header().Get("Content-Type"))
	}
	if got := fallback.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("fallback cache control=%q", got)
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
		HTML:       pointer(`<a href="https://example.com" style="display:grid">visit</a>`),
		Raw:        []byte("Date: Thu, 24 Jul 2026 12:00:00 +0000\r\nFrom: sender@example.com\r\nTo: inspect@example.com\r\nMessage-ID: <inspect@example.com>\r\nSubject: Hello\r\nMIME-Version: 1.0\r\nContent-Type: text/html; charset=utf-8\r\n\r\n<a href=\"https://example.com\" style=\"display:grid\">visit</a>"),
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
	if report.MIMETree == nil || report.MIMETree.Checksums == nil || len(report.Findings) == 0 || report.Resources == nil || len(report.Headers) == 0 || report.HTMLCompatibility == nil || len(report.HTMLCompatibility.Warnings) == 0 {
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
	handler := New(testStore(t), StaticConfig{}, nil)
	done := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r)
		close(done)
	}))
	defer func() {
		server.CloseClientConnections()
		server.Close()
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/events", nil)
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	_ = response.Body.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("SSE handler did not return after client cancellation")
	}
}
