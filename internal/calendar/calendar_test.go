package calendar

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestParseICSRequestOrganizerAttendees(t *testing.T) {
	ics := "BEGIN:VCALENDAR\r\n" +
		"VERSION:2.0\r\n" +
		"METHOD:request\r\n" +
		"BEGIN:VEVENT\r\n" +
		"UID:meeting-1@example.com\r\n" +
		"SEQUENCE:3\r\n" +
		"DTSTART:20260723T100000Z\r\n" +
		"DTEND:20260723T113000Z\r\n" +
		"SUMMARY:Planning\\, phase 2\r\n" +
		"DESCRIPTION:First line\\nSecond line\r\n" +
		"LOCATION:Owl Room\r\n" +
		"STATUS:confirmed\r\n" +
		"ORGANIZER;CN=The Hoomail Owl:MAILTO:OWL@EXAMPLE.COM\r\n" +
		"ATTENDEE;CN=Alice;ROLE=req-participant;PARTSTAT=needs-action:mailto:Alice@Example.com\r\n" +
		"ATTENDEE;CN=\"Bob; Builder\";ROLE=OPT-PARTICIPANT;PARTSTAT=ACCEPTED:MAILTO:Bob@Example.com\r\n" +
		"END:VEVENT\r\n" +
		"END:VCALENDAR\r\n"

	events := ParseICS(ics)
	if len(events) != 1 {
		t.Fatalf("ParseICS returned %d events, want 1", len(events))
	}

	start := time.Date(2026, time.July, 23, 10, 0, 0, 0, time.UTC).UnixMilli()
	end := time.Date(2026, time.July, 23, 11, 30, 0, 0, time.UTC).UnixMilli()
	want := ParsedCalendarEvent{
		Method:           MethodRequest,
		UID:              "meeting-1@example.com",
		Sequence:         3,
		Summary:          stringPointer("Planning, phase 2"),
		Description:      stringPointer("First line\nSecond line"),
		Location:         stringPointer("Owl Room"),
		Status:           stringPointer("CONFIRMED"),
		OrganizerAddress: stringPointer("owl@example.com"),
		OrganizerName:    stringPointer("The Hoomail Owl"),
		Attendees: []ParsedAttendee{
			{Address: "alice@example.com", Name: stringPointer("Alice"), Partstat: stringPointer("NEEDS-ACTION"), Role: stringPointer("REQ-PARTICIPANT")},
			{Address: "bob@example.com", Name: stringPointer("Bob; Builder"), Partstat: stringPointer("ACCEPTED"), Role: stringPointer("OPT-PARTICIPANT")},
		},
		DTStart: start,
		DTEnd:   &end,
		AllDay:  false,
	}
	if !reflect.DeepEqual(events[0], want) {
		t.Fatalf("parsed event mismatch:\n got %#v\nwant %#v", events[0], want)
	}

	encoded, err := json.Marshal(events[0])
	if err != nil {
		t.Fatal(err)
	}
	const wantJSON = `{"method":"REQUEST","uid":"meeting-1@example.com","sequence":3,"summary":"Planning, phase 2","description":"First line\nSecond line","location":"Owl Room","status":"CONFIRMED","organizerAddress":"owl@example.com","organizerName":"The Hoomail Owl","attendees":[{"address":"alice@example.com","name":"Alice","partstat":"NEEDS-ACTION","role":"REQ-PARTICIPANT"},{"address":"bob@example.com","name":"Bob; Builder","partstat":"ACCEPTED","role":"OPT-PARTICIPANT"}],"dtstart":1784800800000,"dtend":1784806200000,"allDay":false}`
	if string(encoded) != wantJSON {
		t.Fatalf("JSON shape mismatch:\n got %s\nwant %s", encoded, wantJSON)
	}
}

func TestParseICSCancelAndReply(t *testing.T) {
	tests := []struct {
		name      string
		method    string
		status    *string
		attendees []ParsedAttendee
	}{
		{name: "cancel", method: MethodCancel, status: stringPointer("CANCELLED"), attendees: []ParsedAttendee{}},
		{name: "reply", method: MethodReply, attendees: []ParsedAttendee{{Address: "guest@example.com", Name: stringPointer("Guest"), Partstat: stringPointer("DECLINED")}}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status := ""
			attendee := ""
			if test.status != nil {
				status = "STATUS:" + *test.status + "\r\n"
			}
			if len(test.attendees) > 0 {
				attendee = "ATTENDEE;CN=Guest;PARTSTAT=declined:mailto:GUEST@EXAMPLE.COM\r\n"
			}
			ics := "BEGIN:VCALENDAR\r\nMETHOD:" + test.method + "\r\nBEGIN:VEVENT\r\n" +
				"UID:transport@example.com\r\nSEQUENCE:8\r\nDTSTART:20260723T100000Z\r\n" + status + attendee +
				"END:VEVENT\r\nEND:VCALENDAR\r\n"

			events := ParseICS(ics)
			if len(events) != 1 {
				t.Fatalf("ParseICS returned %d events, want 1", len(events))
			}
			if events[0].Method != test.method || !reflect.DeepEqual(events[0].Status, test.status) || !reflect.DeepEqual(events[0].Attendees, test.attendees) {
				t.Fatalf("transport fields mismatch: %#v", events[0])
			}
		})
	}
}

func TestParseICSRejectsMethodAfterComponents(t *testing.T) {
	tests := []struct {
		name   string
		method string
	}{
		{name: "cancel", method: MethodCancel},
		{name: "reply", method: MethodReply},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ics := "BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\n" +
				"UID:transport@example.com\r\nSEQUENCE:8\r\nDTSTART:20260723T100000Z\r\n" +
				"END:VEVENT\r\nMETHOD:" + test.method + "\r\nEND:VCALENDAR\r\n"

			events := ParseICS(ics)
			if len(events) != 0 {
				t.Fatalf("ParseICS returned %d events with METHOD after VEVENT, want 0: %#v", len(events), events)
			}
		})
	}
}

func TestParseICSAllDayDefaultsEndAndPublish(t *testing.T) {
	ics := "BEGIN:VCALENDAR\nBEGIN:VEVENT\nUID:holiday@example.com\nDTSTART;VALUE=DATE:20261224\nSUMMARY:Holiday\nEND:VEVENT\nEND:VCALENDAR\n"
	events := ParseICS(ics)
	if len(events) != 1 {
		t.Fatalf("ParseICS returned %d events, want 1", len(events))
	}
	start := time.Date(2026, time.December, 24, 0, 0, 0, 0, time.UTC).UnixMilli()
	end := time.Date(2026, time.December, 25, 0, 0, 0, 0, time.UTC).UnixMilli()
	if events[0].Method != MethodPublish || !events[0].AllDay || events[0].DTStart != start || events[0].DTEnd == nil || *events[0].DTEnd != end {
		t.Fatalf("all-day event mismatch: %#v", events[0])
	}
}

func TestParseICSDateValueAnchorsUTCMidnight(t *testing.T) {
	ics := "BEGIN:VCALENDAR\nBEGIN:VEVENT\nUID:date@example.com\nDTSTART;VALUE=DATE:20260825\nEND:VEVENT\nEND:VCALENDAR\n"
	events := ParseICS(ics)
	if len(events) != 1 {
		t.Fatalf("ParseICS returned %d events, want 1", len(events))
	}
	want := time.Date(2026, time.August, 25, 0, 0, 0, 0, time.UTC)
	if !events[0].AllDay || events[0].DTStart != want.UnixMilli() {
		t.Fatalf("all-day DTSTART = %d, want UTC midnight instant %d", events[0].DTStart, want.UnixMilli())
	}
}

func TestParseICSPreservesLiteralCaretInParameter(t *testing.T) {
	ics := "BEGIN:VCALENDAR\r\n" +
		"VERSION:2.0\r\n" +
		"METHOD:REQUEST\r\n" +
		"BEGIN:VEVENT\r\n" +
		"UID:caret@example.com\r\n" +
		"SEQUENCE:1\r\n" +
		"DTSTART:20260723T100000Z\r\n" +
		"ORGANIZER;CN=Owl^Nest:mailto:o@example.com\r\n" +
		"ATTENDEE;CN=Research^notes:mailto:r@example.com\r\n" +
		"END:VEVENT\r\n" +
		"END:VCALENDAR\r\n"

	events := ParseICS(ics)
	if len(events) != 1 {
		t.Fatalf("ParseICS returned %d events, want 1", len(events))
	}
	if len(events[0].Attendees) != 1 {
		t.Fatalf("got %d attendees, want 1", len(events[0].Attendees))
	}
	attendee := events[0].Attendees[0]
	if attendee.Name == nil || *attendee.Name != "Research^notes" {
		t.Fatalf("attendee CN = %#v, want literal \"Research^notes\"", attendee.Name)
	}
	if events[0].OrganizerName == nil || *events[0].OrganizerName != "Owl^Nest" {
		t.Fatalf("organizer CN = %#v, want literal \"Owl^Nest\"", events[0].OrganizerName)
	}
}

func TestParseICSTimedEventWithoutEndKeepsNullEnd(t *testing.T) {
	ics := "BEGIN:VCALENDAR\nBEGIN:VEVENT\nUID:instant@example.com\nDTSTART:20260723T100000Z\nEND:VEVENT\nEND:VCALENDAR\n"
	events := ParseICS(ics)
	if len(events) != 1 {
		t.Fatalf("ParseICS returned %d events, want 1", len(events))
	}
	if events[0].DTEnd != nil {
		t.Fatalf("DTEnd = %d, want nil", *events[0].DTEnd)
	}
}

func TestParseICSFoldedLineTimezoneAndDuration(t *testing.T) {
	ics := "BEGIN:VCALENDAR\r\nMETHOD:REQUEST\r\nBEGIN:VEVENT\r\n" +
		"UID:duration@example.com\r\nDTSTART;TZID=Europe/Berlin:20260115T090000\r\nDURATION:PT1H30M\r\n" +
		"SUMMARY:Quarterly planning with a long\r\n agenda\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	events := ParseICS(ics)
	if len(events) != 1 {
		t.Fatalf("ParseICS returned %d events, want 1", len(events))
	}
	start := time.Date(2026, time.January, 15, 8, 0, 0, 0, time.UTC).UnixMilli()
	end := time.Date(2026, time.January, 15, 9, 30, 0, 0, time.UTC).UnixMilli()
	if events[0].DTStart != start || events[0].DTEnd == nil || *events[0].DTEnd != end || events[0].Summary == nil || *events[0].Summary != "Quarterly planning with a longagenda" {
		t.Fatalf("timezone/duration/folding mismatch: %#v", events[0])
	}
}

func TestParseICSNormalizesNegativeSequence(t *testing.T) {
	ics := "BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\nUID:negative-sequence@example.com\r\nSEQUENCE:-1\r\nDTSTART:20260723T100000Z\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	events := ParseICS(ics)
	if len(events) != 1 {
		t.Fatalf("ParseICS returned %d events, want 1", len(events))
	}
	if events[0].Sequence != 0 {
		t.Fatalf("Sequence = %d, want 0", events[0].Sequence)
	}
}

func TestParseICSRejectsMalformedAndSkipsIncompleteEvents(t *testing.T) {
	malformed := []string{
		"not a calendar",
		"BEGIN:VCALENDAR\nBEGIN:VEVENT\nUID:x\nDTSTART:not-a-date\nEND:VEVENT\nEND:VCALENDAR",
		"BEGIN:VCALENDAR\nBEGIN:VEVENT\nUID:x\nDTSTART:20260723T100000Z\nEND:VCALENDAR",
	}
	for _, input := range malformed {
		if events := ParseICS(input); len(events) != 0 {
			t.Fatalf("ParseICS(%q) returned %#v, want empty", input, events)
		}
	}

	ics := "BEGIN:VCALENDAR\nBEGIN:VEVENT\nUID:missing-start\nEND:VEVENT\nBEGIN:VEVENT\nDTSTART:20260723T100000Z\nEND:VEVENT\nEND:VCALENDAR"
	if events := ParseICS(ics); len(events) != 0 {
		t.Fatalf("incomplete events returned %#v, want empty", events)
	}
}

func TestUnfoldLinesPreservesFoldingSemantics(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []string
		ok   bool
	}{
		{name: "mixed newline styles and continuations", text: "first\r\n second\r\n\tthird\r\nnext", want: []string{"firstsecondthird", "next"}, ok: true},
		{name: "continuation after empty logical line", text: "first\n\n continuation", want: []string{"first", "continuation"}, ok: true},
		{name: "continuation without a logical line", text: " continuation", ok: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := unfoldLines(test.text)
			if ok != test.ok {
				t.Fatalf("unfoldLines(%q) ok = %v, want %v", test.text, ok, test.ok)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("unfoldLines(%q) = %#v, want %#v", test.text, got, test.want)
			}
		})
	}

	var folded strings.Builder
	folded.WriteString("DESCRIPTION:prefix")
	for range 10_000 {
		folded.WriteString("\n continuation")
	}
	lines, ok := unfoldLines(folded.String())
	if !ok || len(lines) != 1 || len(lines[0]) != len("DESCRIPTION:prefix")+len("continuation")*10_000 {
		t.Fatalf("large folded line = ok:%v lines:%d length:%d", ok, len(lines), len(lines[0]))
	}
}

func TestParseICSRejectsIllegalNestingAndPropertiesOutsideRoot(t *testing.T) {
	validEvent := "BEGIN:VEVENT\nUID:nesting@example.com\nDTSTART:20260723T100000Z\nEND:VEVENT\n"
	tests := []struct {
		name string
		ics  string
		want int
	}{
		{name: "valid direct event", ics: "BEGIN:VCALENDAR\n" + validEvent + "END:VCALENDAR\n", want: 1},
		{name: "nested calendar", ics: "BEGIN:VCALENDAR\nBEGIN:VCALENDAR\nEND:VCALENDAR\nEND:VCALENDAR\n"},
		{name: "nested event", ics: "BEGIN:VCALENDAR\nBEGIN:VEVENT\nBEGIN:VEVENT\nEND:VEVENT\nEND:VEVENT\nEND:VCALENDAR\n"},
		{name: "event under another component", ics: "BEGIN:VCALENDAR\nBEGIN:VTIMEZONE\nBEGIN:VEVENT\nEND:VEVENT\nEND:VTIMEZONE\nEND:VCALENDAR\n"},
		{name: "property before root", ics: "VERSION:2.0\nBEGIN:VCALENDAR\nEND:VCALENDAR\n"},
		{name: "property after root", ics: "BEGIN:VCALENDAR\nEND:VCALENDAR\nVERSION:2.0\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := len(ParseICS(test.ics)); got != test.want {
				t.Fatalf("ParseICS returned %d events, want %d", got, test.want)
			}
		})
	}
}

func TestDecodeTextRejectsUnknownEscapes(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
		ok    bool
	}{
		{name: "plain text", value: "plain", want: "plain", ok: true},
		{name: "newline", value: `first\nsecond`, want: "first\nsecond", ok: true},
		{name: "escaped punctuation", value: `a\\b\,c\;d`, want: "a\\b,c;d", ok: true},
		{name: "unknown escape", value: `meeting\q`, ok: false},
		{name: "dangling escape", value: `meeting\`, ok: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := decodeText(test.value)
			if ok != test.ok || got != test.want {
				t.Fatalf("decodeText(%q) = (%q, %v), want (%q, %v)", test.value, got, ok, test.want, test.ok)
			}
		})
	}
}

func TestParseICSRejectsMalformedTextEscapes(t *testing.T) {
	eventICS := func(uid, extra string) string {
		return "BEGIN:VCALENDAR\nBEGIN:VEVENT\nUID:" + uid + "\nDTSTART:20260723T100000Z\n" + extra + "END:VEVENT\nEND:VCALENDAR\n"
	}
	tests := []struct {
		name string
		ics  string
		want int
	}{
		{name: "unknown UID escape", ics: eventICS(`meeting\q`, ""), want: 0},
		{name: "unknown summary escape", ics: eventICS("meeting@example.com", "SUMMARY:bad\\q\n"), want: 0},
		{name: "valid escapes", ics: eventICS("meeting@example.com", `SUMMARY:a\,b\;c\\d\ne`+"\n"), want: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			events := ParseICS(test.ics)
			if len(events) != test.want {
				t.Fatalf("ParseICS returned %d events, want %d", len(events), test.want)
			}
			if test.name == "valid escapes" && *events[0].Summary != "a,b;c\\d\ne" {
				t.Fatalf("decoded summary = %q", *events[0].Summary)
			}
		})
	}
}

func TestParseICSCapsEventsPerCalendar(t *testing.T) {
	tests := []int{maxEventsPerCalendar, maxEventsPerCalendar + 1}
	for _, eventCount := range tests {
		t.Run(strconv.Itoa(eventCount), func(t *testing.T) {
			var ics strings.Builder
			ics.WriteString("BEGIN:VCALENDAR\n")
			for index := range eventCount {
				ics.WriteString("BEGIN:VEVENT\nUID:event-")
				ics.WriteString(strconv.Itoa(index))
				ics.WriteString("\nDTSTART:20260723T100000Z\nEND:VEVENT\n")
			}
			ics.WriteString("END:VCALENDAR\n")
			events := ParseICS(ics.String())
			want := eventCount
			if eventCount > maxEventsPerCalendar {
				want = 0
			}
			if len(events) != want {
				t.Fatalf("ParseICS returned %d events, want %d", len(events), want)
			}
		})
	}
}

func TestIsCalendarPart(t *testing.T) {
	tests := []struct {
		contentType string
		filename    string
		want        bool
	}{
		{"text/calendar; method=REQUEST; charset=utf-8", "", true},
		{"APPLICATION/ICS", "invite.bin", true},
		{"application/octet-stream", "Invite.ICS", true},
		{"text/plain", "invite.txt", false},
	}
	for _, test := range tests {
		if got := IsCalendarPart(test.contentType, test.filename); got != test.want {
			t.Errorf("IsCalendarPart(%q, %q) = %v, want %v", test.contentType, test.filename, got, test.want)
		}
	}
}

func stringPointer(value string) *string {
	return &value
}
