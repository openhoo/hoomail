package calendar

import (
	"encoding/json"
	"reflect"
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

func TestParseICSAllDayDefaultsEndAndPublish(t *testing.T) {
	ics := "BEGIN:VCALENDAR\nBEGIN:VEVENT\nUID:holiday@example.com\nDTSTART;VALUE=DATE:20261224\nSUMMARY:Holiday\nEND:VEVENT\nEND:VCALENDAR\n"
	events := ParseICS(ics)
	if len(events) != 1 {
		t.Fatalf("ParseICS returned %d events, want 1", len(events))
	}
	start := time.Date(2026, time.December, 24, 0, 0, 0, 0, time.Local).UnixMilli()
	end := time.Date(2026, time.December, 25, 0, 0, 0, 0, time.Local).UnixMilli()
	if events[0].Method != MethodPublish || !events[0].AllDay || events[0].DTStart != start || events[0].DTEnd == nil || *events[0].DTEnd != end {
		t.Fatalf("all-day event mismatch: %#v", events[0])
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
	location, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, time.January, 15, 9, 0, 0, 0, location).UnixMilli()
	end := time.Date(2026, time.January, 15, 10, 30, 0, 0, location).UnixMilli()
	if events[0].DTStart != start || events[0].DTEnd == nil || *events[0].DTEnd != end || events[0].Summary == nil || *events[0].Summary != "Quarterly planning with a longagenda" {
		t.Fatalf("timezone/duration/folding mismatch: %#v", events[0])
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
