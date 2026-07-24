package smtpserver

import (
	"errors"
	"strings"

	"github.com/emersion/go-message"
	messagemail "github.com/emersion/go-message/mail"
	"github.com/openhoo/hoomail/internal/calendar"
	"github.com/openhoo/hoomail/internal/mimeparse"
	"github.com/openhoo/hoomail/internal/store"
)

func Parse(raw []byte, envelopeFrom string, envelopeRecipients []string) (store.StoreMessageInput, error) {
	document, err := mimeparse.Parse(raw, mimeparse.Limits{})
	if err != nil {
		return store.StoreMessageInput{}, err
	}
	if document.SemanticError != nil {
		return store.StoreMessageInput{}, document.SemanticError
	}
	if document.Root == nil {
		return store.StoreMessageInput{}, errors.New("message parser returned a nil entity")
	}

	header := messagemail.Header{Header: document.Root.Header}
	to, err := addresses(header, "To")
	if err != nil {
		return store.StoreMessageInput{}, err
	}
	cc, err := addresses(header, "Cc")
	if err != nil {
		return store.StoreMessageInput{}, err
	}
	from, err := addresses(header, "From")
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

	subject, subjectErr := header.Subject()
	if subjectErr != nil && !message.IsUnknownCharset(subjectErr) {
		return store.StoreMessageInput{}, subjectErr
	}

	input := store.StoreMessageInput{
		FromAddress: fromAddress,
		FromName:    fromName,
		To:          to,
		CC:          cc,
		Subject:     nullable(subject),
		Headers:     headerLines(header),
		Size:        int64(len(raw)),
		Raw:         append([]byte(nil), raw...),
	}
	input.Recipients = normalizedRecipients(envelopeRecipients, append(append([]store.AddressEntry(nil), to...), cc...))

	presentation := document.Presentation
	if presentation.Supported {
		if presentation.Text != nil {
			text := string(presentation.Text.DecodedBody)
			input.Text = &text
		}
		if presentation.HTML != nil {
			html := string(presentation.HTML.DecodedBody)
			input.HTML = &html
		}
		input.Attachments = projectAttachments(presentation.Attachments)
	} else {
		input.Attachments = projectAttachments(collectAllLeaves(document.Root))
	}

	seenCalendar := make(map[string]struct{})
	for _, attachment := range input.Attachments {
		contentType := ""
		if attachment.ContentType != nil {
			contentType = *attachment.ContentType
		}
		filename := ""
		if attachment.Filename != nil {
			filename = *attachment.Filename
		}
		if !calendar.IsCalendarPart(contentType, filename) {
			continue
		}
		for _, event := range calendar.ParseICS(string(attachment.Content)) {
			key := event.DedupKey()
			if _, duplicate := seenCalendar[key]; duplicate {
				continue
			}
			seenCalendar[key] = struct{}{}
			input.ICalEvents = append(input.ICalEvents, event)
		}
	}
	return input, nil
}

func collectAllLeaves(root *mimeparse.Node) []mimeparse.AttachmentCandidate {
	if root == nil {
		return nil
	}
	stack := []*mimeparse.Node{root}
	var leaves []mimeparse.AttachmentCandidate
	for len(stack) > 0 {
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if len(node.Children) == 0 {
			leaves = append(leaves, mimeparse.AttachmentCandidate{Node: node})
			continue
		}
		for index := len(node.Children) - 1; index >= 0; index-- {
			stack = append(stack, node.Children[index])
		}
	}
	return leaves
}

func projectAttachments(candidates []mimeparse.AttachmentCandidate) []store.AttachmentInput {
	attachments := make([]store.AttachmentInput, 0, len(candidates))
	for _, candidate := range candidates {
		node := candidate.Node
		if node == nil {
			continue
		}
		contentType := node.MediaType
		if node.MalformedContentType || message.IsUnknownEncoding(node.DecodeError) || contentType == "" {
			contentType = "application/octet-stream"
		}
		var contentID *string
		if candidate.ExposeContentID {
			contentID = nullable(mimeparse.NormalizeContentID(node.Header.Get("Content-Id")))
		}
		attachments = append(attachments, store.AttachmentInput{
			Filename:    nullable(mimeparse.Filename(node)),
			ContentType: nullable(contentType),
			ContentID:   contentID,
			Content:     append([]byte(nil), node.DecodedBody...),
		})
	}
	return attachments
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

func nullable(value string) *string {
	if value == "" {
		return nil
	}
	return stringPointer(value)
}

func stringPointer(value string) *string { return &value }
