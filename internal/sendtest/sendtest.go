package sendtest

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"html"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"regexp"
	"strings"
	"time"

	"github.com/openhoo/hoomail/internal/httpserver"
)

type Sender struct {
	Address string
	Now     func() time.Time
}

func (sender Sender) SendTest(ctx context.Context, request httpserver.SendTestRequest) error {
	address := sender.Address
	if address == "" {
		address = "127.0.0.1:2525"
	}
	now := time.Now
	if sender.Now != nil {
		now = sender.Now
	}
	message := buildMessage(request, now())
	dialer := net.Dialer{}
	connection, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return err
	}
	client, err := smtp.NewClient(connection, host(address))
	if err != nil {
		connection.Close()
		return err
	}
	defer client.Close()
	if err = client.Mail("owl@hoomail.local"); err != nil {
		return err
	}
	if err = client.Rcpt(request.To); err != nil {
		return err
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err = writer.Write(message); err != nil {
		return err
	}
	if err = writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}

func host(address string) string {
	host, _, err := net.SplitHostPort(address)
	if err == nil {
		return host
	}
	return "localhost"
}

func buildMessage(request httpserver.SendTestRequest, now time.Time) []byte {
	subject := strings.TrimSpace(request.Subject)
	if request.Kind == "plain" {
		if subject == "" {
			subject = "hoomail delivery test"
		}
		return plainMessage(request.To, subject, now)
	}
	if subject == "" {
		subject = "Owl standup meeting"
	}
	return calendarMessage(request, subject, now)
}

func plainMessage(to, subject string, now time.Time) []byte {
	const mixed = "hoomail-mixed-boundary"
	const alternative = "hoomail-alt-boundary"
	text := fmt.Sprintf("Hoot hoot! It works.\r\n\r\nThis test email was delivered through the hoomail SMTP server to %s. The inbox was created automatically, the message was parsed, stored in SQLite, and pushed to the UI in realtime.", to)
	html := sampleHTML(to)
	var body bytes.Buffer
	writeHeaders(&body, to, subject, now)
	fmt.Fprintf(&body, "MIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=%q\r\n\r\n", mixed)
	fmt.Fprintf(&body, "--%s\r\nContent-Type: multipart/alternative; boundary=%q\r\n\r\n", mixed, alternative)
	fmt.Fprintf(&body, "--%s\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n", alternative)
	writeQuotedPrintable(&body, text)
	fmt.Fprintf(&body, "\r\n--%s\r\nContent-Type: text/html; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n", alternative)
	writeQuotedPrintable(&body, html)
	fmt.Fprintf(&body, "\r\n--%s--\r\n", alternative)
	attachment := base64.StdEncoding.EncodeToString([]byte("This is a sample attachment from the hoomail test sender.\nHoot hoot!\n"))
	fmt.Fprintf(&body, "--%s\r\nContent-Type: text/plain; name=\"hoot.txt\"\r\nContent-Disposition: attachment; filename=\"hoot.txt\"\r\nContent-Transfer-Encoding: base64\r\n\r\n%s\r\n--%s--\r\n", mixed, attachment, mixed)
	return body.Bytes()
}

func calendarMessage(request httpserver.SendTestRequest, summary string, now time.Time) []byte {
	kind := request.Kind
	method, sequence, status, prefix := "REQUEST", 0, "CONFIRMED", "Invitation"
	if kind == "update" {
		sequence, prefix = 1, "Updated invitation"
	} else if kind == "cancellation" {
		method, sequence, status, prefix = "CANCEL", 2, "CANCELLED", "Cancelled"
	}
	start := now.UTC().AddDate(0, 0, 1)
	hour := 10
	if kind == "update" {
		hour = 11
	}
	start = time.Date(start.Year(), start.Month(), start.Day(), hour, 0, 0, 0, time.UTC)
	end := start.Add(30 * time.Minute)
	ics := buildICS(request.To, summary, method, status, sequence, now.UTC(), start, end)
	text := fmt.Sprintf("You have been invited to %q. Open this message in hoomail to see the appointment.", summary)
	if kind == "cancellation" {
		text = fmt.Sprintf("The event %q has been cancelled.", summary)
	}
	const boundary = "hoomail-calendar-boundary"
	var body bytes.Buffer
	writeHeaders(&body, request.To, prefix+": "+summary, now)
	fmt.Fprintf(&body, "MIME-Version: 1.0\r\nContent-Type: multipart/alternative; boundary=%q\r\n\r\n", boundary)
	fmt.Fprintf(&body, "--%s\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n", boundary)
	writeQuotedPrintable(&body, text)
	fmt.Fprintf(&body, "\r\n")
	fmt.Fprintf(&body, "--%s\r\nContent-Type: text/calendar; charset=utf-8; method=%s\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n", boundary, method)
	writeQuotedPrintable(&body, ics)
	fmt.Fprintf(&body, "\r\n--%s--\r\n", boundary)
	return body.Bytes()
}

func writeHeaders(body *bytes.Buffer, to, subject string, now time.Time) {
	recipient := (&mail.Address{Address: to}).String()
	encodedSubject := mime.QEncoding.Encode("UTF-8", subject)
	fmt.Fprintf(body, "From: \"The hoomail Owl\" <owl@hoomail.local>\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMessage-ID: <%d@hoomail.local>\r\n", recipient, encodedSubject, now.Format(time.RFC1123Z), now.UnixNano())
}

func writeQuotedPrintable(body *bytes.Buffer, value string) {
	writer := quotedprintable.NewWriter(body)
	_, _ = writer.Write([]byte(value))
	_ = writer.Close()
}

var unsafeUID = regexp.MustCompile(`[^a-zA-Z0-9]`)

func buildICS(to, summary, method, status string, sequence int, stamp, start, end time.Time) string {
	escape := func(value string) string {
		normalized := strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(value)
		replacer := strings.NewReplacer("\\", "\\\\", "\n", "\\n", ";", "\\;", ",", "\\,")
		return replacer.Replace(normalized)
	}
	format := func(value time.Time) string { return value.UTC().Format("20060102T150405Z") }
	uid := "hoomail-demo-" + unsafeUID.ReplaceAllString(to, "-") + "@hoomail.local"
	return strings.Join([]string{"BEGIN:VCALENDAR", "PRODID:-//hoomail//test sender//EN", "VERSION:2.0", "METHOD:" + method, "BEGIN:VEVENT", "UID:" + uid, fmt.Sprintf("SEQUENCE:%d", sequence), "DTSTAMP:" + format(stamp), "DTSTART:" + format(start), "DTEND:" + format(end), "SUMMARY:" + escape(summary), "LOCATION:Owl Tree Conference Room", "DESCRIPTION:A test appointment sent by the hoomail built-in sender.", "STATUS:" + status, "ORGANIZER;CN=The hoomail Owl:mailto:owl@hoomail.local", "ATTENDEE;CN=" + escape(to) + ";ROLE=REQ-PARTICIPANT;PARTSTAT=NEEDS-ACTION;RSVP=TRUE:mailto:" + escape(to), "END:VEVENT", "END:VCALENDAR"}, "\r\n")
}

func sampleHTML(recipient string) string {
	return fmt.Sprintf(`<!DOCTYPE html><html><body style="margin:0;padding:0;background-color:#f4f1ea;font-family:Georgia,serif;"><h1>Hoot hoot! It works.</h1><p>This test email was delivered through the hoomail SMTP server to <strong>%s</strong>. The inbox was created automatically, the message was parsed, stored in SQLite, and pushed to the UI in realtime.</p><a href="https://example.com">A sample button</a></body></html>`, html.EscapeString(recipient))
}
