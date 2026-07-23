package smtpserver

import (
	"bytes"
	"errors"
	"io"
	"strings"

	"github.com/emersion/go-message"
	_ "github.com/emersion/go-message/charset"
	messagemail "github.com/emersion/go-message/mail"
	"github.com/openhoo/hoomail/internal/calendar"
	"github.com/openhoo/hoomail/internal/store"
)

type mimeNode struct {
	header               message.Header
	mediaType            string
	mediaParams          map[string]string
	malformedContentType bool
	unknownEncoding      bool
	body                 []byte
	children             []*mimeNode
}

type mimePresentation struct {
	text        *string
	html        *string
	attachments []store.AttachmentInput
	supported   bool
}

type attachmentCollectionMode uint8

const (
	attachmentCollectionGeneric attachmentCollectionMode = iota
	attachmentCollectionSelectedRelated
)

func Parse(raw []byte, envelopeFrom string, envelopeRecipients []string) (store.StoreMessageInput, error) {
	entity, entityErr := message.Read(bytes.NewReader(raw))
	if entityErr != nil && !message.IsUnknownCharset(entityErr) && !message.IsUnknownEncoding(entityErr) {
		return store.StoreMessageInput{}, entityErr
	}
	if entity == nil {
		return store.StoreMessageInput{}, entityErr
	}

	header := messagemail.Header{Header: entity.Header}
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

	root, err := readMIMENode(entity, entityErr)
	if err != nil {
		return store.StoreMessageInput{}, err
	}
	presentation := selectMIMEPresentation(root)
	if presentation.supported {
		input.Text = presentation.text
		input.HTML = presentation.html
		input.Attachments = presentation.attachments
	} else {
		input.Attachments = appendMIMEAttachments(nil, root, attachmentCollectionGeneric)
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

func readMIMENode(entity *message.Entity, entityErr error) (*mimeNode, error) {
	contentTypeHeader := entity.Header.Get("Content-Type")
	mediaType, mediaParams, contentTypeErr := entity.Header.ContentType()
	node := &mimeNode{
		header:               entity.Header,
		mediaType:            strings.ToLower(mediaType),
		mediaParams:          mediaParams,
		malformedContentType: contentTypeHeader != "" && contentTypeErr != nil,
		unknownEncoding:      message.IsUnknownEncoding(entityErr),
	}

	if multipartReader := entity.MultipartReader(); multipartReader != nil && !node.malformedContentType {
		defer multipartReader.Close()
		for {
			part, partErr := multipartReader.NextPart()
			if errors.Is(partErr, io.EOF) {
				break
			}
			if partErr != nil && !message.IsUnknownCharset(partErr) && !message.IsUnknownEncoding(partErr) {
				return nil, partErr
			}
			if part == nil {
				return nil, partErr
			}
			child, err := readMIMENode(part, partErr)
			if err != nil {
				return nil, err
			}
			node.children = append(node.children, child)
		}
		return node, nil
	}

	body, err := io.ReadAll(entity.Body)
	if err != nil {
		return nil, err
	}
	node.body = body
	return node, nil
}

func selectMIMEPresentation(node *mimeNode) mimePresentation {
	if len(node.children) == 0 {
		return selectMIMELeaf(node)
	}

	switch node.mediaType {
	case "multipart/alternative":
		return selectAlternativePresentation(node)
	case "multipart/related":
		return selectRelatedPresentation(node)
	default:
		return selectMixedPresentation(node)
	}
}

func selectMIMELeaf(node *mimeNode) mimePresentation {
	if node.malformedContentType || node.unknownEncoding {
		return mimePresentation{}
	}
	filename := mimeFilename(node.header, node.mediaParams)
	disposition, _, _ := node.header.ContentDisposition()
	isAttachment := strings.EqualFold(disposition, "attachment") || filename != ""
	if !isAttachment && node.mediaType == "text/plain" {
		text := string(node.body)
		return mimePresentation{text: &text, supported: true}
	}
	if !isAttachment && node.mediaType == "text/html" {
		html := string(node.body)
		return mimePresentation{html: &html, supported: true}
	}
	return mimePresentation{}
}

func selectAlternativePresentation(node *mimeNode) mimePresentation {
	var selected mimePresentation
	selectedIndex := -1
	var nearestPlainText *string
	var selectedFallback *string
	for index, child := range node.children {
		candidate := selectMIMEPresentation(child)
		if !candidate.supported {
			continue
		}

		selected = candidate
		selectedIndex = index
		selectedFallback = nearestPlainText
		if candidate.text != nil && candidate.html == nil {
			nearestPlainText = candidate.text
		}
	}
	if selected.html != nil && selected.text == nil {
		selected.text = selectedFallback
	}
	for index, child := range node.children {
		if index != selectedIndex {
			selected.attachments = appendCalendarAttachments(selected.attachments, child)
		}
	}
	return selected
}

func selectMixedPresentation(node *mimeNode) mimePresentation {
	var selected mimePresentation
	var attachments []store.AttachmentInput
	for _, child := range node.children {
		candidate := selectMIMEPresentation(child)
		if !selected.supported && candidate.supported {
			selected = candidate
			continue
		}
		attachments = appendMIMEAttachments(attachments, child, attachmentCollectionGeneric)
	}
	selected.attachments = append(selected.attachments, attachments...)
	return selected
}

func selectRelatedPresentation(node *mimeNode) mimePresentation {
	if len(node.children) == 0 {
		return mimePresentation{}
	}
	rootIndex := 0
	if start := normalizeContentID(node.mediaParams["start"]); start != "" {
		rootIndex = -1
		for index, child := range node.children {
			if normalizeContentID(child.header.Get("Content-Id")) == start {
				rootIndex = index
				break
			}
		}
		if rootIndex < 0 {
			return mimePresentation{}
		}
	}

	selected := selectMIMEPresentation(node.children[rootIndex])
	if !selected.supported {
		return mimePresentation{}
	}
	for index, child := range node.children {
		if index != rootIndex {
			selected.attachments = appendMIMEAttachments(selected.attachments, child, attachmentCollectionSelectedRelated)
		}
	}
	return selected
}

func appendMIMEAttachments(attachments []store.AttachmentInput, node *mimeNode, mode attachmentCollectionMode) []store.AttachmentInput {
	if len(node.children) > 0 {
		for _, child := range node.children {
			attachments = appendMIMEAttachments(attachments, child, mode)
		}
		return attachments
	}

	contentType := node.mediaType
	if node.malformedContentType || node.unknownEncoding || contentType == "" {
		contentType = "application/octet-stream"
	}
	return append(attachments, store.AttachmentInput{
		Filename:    nullable(mimeFilename(node.header, node.mediaParams)),
		ContentType: nullable(contentType),
		ContentID:   collectedContentID(node, mode),
		Content:     append([]byte(nil), node.body...),
	})
}

func collectedContentID(node *mimeNode, mode attachmentCollectionMode) *string {
	if mode != attachmentCollectionSelectedRelated {
		return nil
	}
	return nullable(normalizeContentID(node.header.Get("Content-Id")))
}

func appendCalendarAttachments(attachments []store.AttachmentInput, node *mimeNode) []store.AttachmentInput {
	if len(node.children) > 0 {
		for _, child := range node.children {
			attachments = appendCalendarAttachments(attachments, child)
		}
		return attachments
	}
	if node.malformedContentType || node.unknownEncoding {
		return attachments
	}

	filename := mimeFilename(node.header, node.mediaParams)
	if !calendar.IsCalendarPart(node.mediaType, filename) {
		return attachments
	}
	calendarAttachment := store.AttachmentInput{
		Filename:    nullable(filename),
		ContentType: nullable(node.mediaType),
		ContentID:   collectedContentID(node, attachmentCollectionGeneric),
		Content:     append([]byte(nil), node.body...),
	}
	for _, attachment := range attachments {
		if sameCalendarAttachment(attachment, calendarAttachment) {
			return attachments
		}
	}
	return append(attachments, calendarAttachment)
}

func sameCalendarAttachment(left, right store.AttachmentInput) bool {
	return optionalStringEqual(left.Filename, right.Filename) &&
		optionalStringEqual(left.ContentType, right.ContentType) &&
		bytes.Equal(left.Content, right.Content)
}

func optionalStringEqual(left, right *string) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

func normalizeContentID(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 4 && strings.EqualFold(value[:4], "cid:") {
		value = value[4:]
	}
	return strings.Trim(strings.TrimSpace(value), "<>")
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

func mimeFilename(header message.Header, contentTypeParameters map[string]string) string {
	_, parameters, err := header.ContentDisposition()
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
