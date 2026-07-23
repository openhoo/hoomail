package calendar

import (
	"strconv"
	"strings"
	"testing"
)

var benchmarkParsedCalendarEvents []ParsedCalendarEvent

func BenchmarkParseICS(b *testing.B) {
	fixtures := []struct {
		name string
		ics  string
	}{
		{name: "RealisticRequest", ics: realisticRequestICSBenchmarkFixture},
		{name: "MultiEvent100", ics: generatedCalendarBenchmarkFixture(100)},
	}

	for _, fixture := range fixtures {
		b.Run(fixture.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(fixture.ics)))
			b.ResetTimer()
			for b.Loop() {
				benchmarkParsedCalendarEvents = ParseICS(fixture.ics)
			}
		})
	}
}

const realisticRequestICSBenchmarkFixture = "BEGIN:VCALENDAR\r\n" +
	"PRODID:-//Hoomail//Benchmark//EN\r\n" +
	"VERSION:2.0\r\n" +
	"METHOD:REQUEST\r\n" +
	"BEGIN:VEVENT\r\n" +
	"UID:planning-2026@example.test\r\n" +
	"SEQUENCE:4\r\n" +
	"DTSTAMP:20260723T080000Z\r\n" +
	"DTSTART;TZID=Europe/Berlin:20260724T100000\r\n" +
	"DTEND;TZID=Europe/Berlin:20260724T113000\r\n" +
	"SUMMARY:Quarterly planning\\, phase 2\r\n" +
	"DESCRIPTION:Review milestones\\nand assign owners\r\n" +
	"LOCATION:Owl Room\r\n" +
	"STATUS:CONFIRMED\r\n" +
	"ORGANIZER;CN=The Hoomail Owl:mailto:owl@example.test\r\n" +
	"ATTENDEE;CN=Alice;ROLE=REQ-PARTICIPANT;PARTSTAT=NEEDS-ACTION:mailto:alice@example.test\r\n" +
	"ATTENDEE;CN=Bob;ROLE=OPT-PARTICIPANT;PARTSTAT=ACCEPTED:mailto:bob@example.test\r\n" +
	"END:VEVENT\r\n" +
	"END:VCALENDAR\r\n"

func generatedCalendarBenchmarkFixture(eventCount int) string {
	var fixture strings.Builder
	fixture.Grow(100 + eventCount*380)
	fixture.WriteString("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nMETHOD:PUBLISH\r\n")
	for event := range eventCount {
		index := strconv.Itoa(event)
		day := 1 + event%28
		date := "202608" + twoDigitBenchmarkNumber(day)
		fixture.WriteString("BEGIN:VEVENT\r\nUID:generated-")
		fixture.WriteString(index)
		fixture.WriteString("@example.test\r\nSEQUENCE:")
		fixture.WriteString(strconv.Itoa(event % 6))
		fixture.WriteString("\r\nDTSTART:")
		fixture.WriteString(date)
		fixture.WriteString("T090000Z\r\nDTEND:")
		fixture.WriteString(date)
		fixture.WriteString("T100000Z\r\nSUMMARY:Generated planning event ")
		fixture.WriteString(index)
		fixture.WriteString("\r\nDESCRIPTION:Review benchmark milestone ")
		fixture.WriteString(index)
		fixture.WriteString("\\nand assign an owner\r\nLOCATION:Room ")
		fixture.WriteString(strconv.Itoa(event % 12))
		fixture.WriteString("\r\nORGANIZER;CN=Hoomail Owl:mailto:owl@example.test\r\n")
		fixture.WriteString("ATTENDEE;CN=Participant;ROLE=REQ-PARTICIPANT;PARTSTAT=ACCEPTED:mailto:participant@example.test\r\n")
		fixture.WriteString("END:VEVENT\r\n")
	}
	fixture.WriteString("END:VCALENDAR\r\n")
	return fixture.String()
}

func twoDigitBenchmarkNumber(value int) string {
	if value < 10 {
		return "0" + strconv.Itoa(value)
	}
	return strconv.Itoa(value)
}
