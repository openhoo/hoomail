package smtpserver

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"strings"

	"github.com/emersion/go-message"
	messagemail "github.com/emersion/go-message/mail"
	"github.com/openhoo/hoomail/internal/calendar"
	"github.com/openhoo/hoomail/internal/store"
)

func Parse(raw []byte, envelopeFrom string, envelopeRecipients []string) (store.StoreMessageInput, error) {
	reader, err := messagemail.CreateReader(bytes.NewReader(raw))
	if err != nil && !message.IsUnknownCharset(err) {
		return store.StoreMessageInput{}, err
	}
	defer reader.Close()

	to, err := addresses(reader.Header, "To")
	if err != nil {
		return store.StoreMessageInput{}, err
	}
	cc, err := addresses(reader.Header, "Cc")
	if err != nil {
		return store.StoreMessageInput{}, err
	}
	from, err := addresses(reader.Header, "From")
	if err != nil {
		return store.StoreMessageInput{}, err
	}

	var fromAddress, fromName *string
	if len(from) > 0 {
		fromAddress = stringPointer(from[0].Address)
		fromName = from[0].Name
	} else if strings.TrimSpace(envelopeFrom) != "" {
		fromAddress = stringPointer(envelopeFrom)
	}

	subject, subjectErr := reader.Header.Subject()
	if subjectErr != nil && !message.IsUnknownCharset(subjectErr) {
		return store.StoreMessageInput{}, subjectErr
	}

	input := store.StoreMessageInput{
		FromAddress: fromAddress,
		FromName:    fromName,
		To:          to,
		CC:          cc,
		Subject:     nullable(subject),
		Headers:     headerLines(reader.Header),
		Size:        int64(len(raw)),
		Raw:         append([]byte(nil), raw...),
	}
	input.Recipients = normalizedRecipients(envelopeRecipients, append(append([]store.AddressEntry(nil), to...), cc...))

	seenCalendar := make(map[string]struct{})
	for {
		part, partErr := reader.NextPart()
		if errors.Is(partErr, io.EOF) {
			break
		}
		if partErr != nil && !message.IsUnknownCharset(partErr) {
			return store.StoreMessageInput{}, partErr
		}

		content, readErr := io.ReadAll(part.Body)
		if readErr != nil {
			return store.StoreMessageInput{}, readErr
		}
		contentType, parameters, parseErr := mime.ParseMediaType(part.Header.Get("Content-Type"))
		if parseErr != nil || contentType == "" {
			contentType = "text/plain"
			parameters = nil
		}
		contentType = strings.ToLower(contentType)
		filename := partFilename(part.Header, parameters)
		disposition, _, _ := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
		isAttachment := strings.EqualFold(disposition, "attachment") || filename != "" || (contentType != "text/plain" && contentType != "text/html")

		if !isAttachment && contentType == "text/plain" {
			text := string(content)
			input.Text = &text
			continue
		}
		if !isAttachment && contentType == "text/html" {
			html := string(content)
			input.HTML = &html
			continue
		}

		attachment := store.AttachmentInput{
			Filename:    nullable(filename),
			ContentType: nullable(contentType),
			ContentID:   nullable(strings.Trim(strings.TrimSpace(part.Header.Get("Content-Id")), "<>")),
			Content:     append([]byte(nil), content...),
		}
		input.Attachments = append(input.Attachments, attachment)

		if calendar.IsCalendarPart(contentType, filename) {
			for _, event := range calendar.ParseICS(string(content)) {
				key := event.DedupKey()
				if _, duplicate := seenCalendar[key]; duplicate {
					continue
				}
				seenCalendar[key] = struct{}{}
				input.ICalEvents = append(input.ICalEvents, event)
			}
		}
	}

	return input, nil
}

func addresses(header messagemail.Header, key string) ([]store.AddressEntry, error) {
	parsed, err := header.AddressList(key)
	if err != nil {
		return nil, err
	}
	out := make([]store.AddressEntry, 0, len(parsed))
	for _, address := range parsed {
		if address == nil || address.Address == "" {
			continue
		}
		out = append(out, store.AddressEntry{Address: address.Address, Name: nullable(address.Name)})
	}
	return out, nil
}

func headerLines(header messagemail.Header) map[string]string {
	fields := header.Fields()
	out := make(map[string]string, fields.Len())
	for fields.Next() {
		key := strings.ToLower(fields.Key())
		raw, err := fields.Raw()
		if err == nil {
			out[key] = strings.TrimRight(string(raw), "\r\n")
			continue
		}
		out[key] = fields.Key() + ": " + fields.Value()
	}
	return out
}

func partFilename(header messagemail.PartHeader, contentTypeParameters map[string]string) string {
	if attachment, ok := header.(*messagemail.AttachmentHeader); ok {
		if filename, err := attachment.Filename(); err == nil && filename != "" {
			return filename
		}
	}
	_, parameters, err := mime.ParseMediaType(header.Get("Content-Disposition"))
	if err == nil && parameters["filename"] != "" {
		return parameters["filename"]
	}
	return contentTypeParameters["name"]
}

func nullable(value string) *string {
	if value == "" {
		return nil
	}
	return stringPointer(value)
}

func stringPointer(value string) *string { return &value }
