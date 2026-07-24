package mimeparse

import (
	"bytes"
	"strings"

	"github.com/emersion/go-message"
)

type attachmentCollectionMode uint8

const (
	attachmentCollectionGeneric attachmentCollectionMode = iota
	attachmentCollectionSelectedRelated
)

func SelectPresentation(node *Node) Presentation {
	if node == nil {
		return Presentation{}
	}
	return selectPresentation(node)
}

func selectPresentation(node *Node) Presentation {
	if len(node.Children) == 0 {
		return selectLeaf(node)
	}
	if isEncapsulatedMessage(node.MediaType) {
		return Presentation{}
	}
	switch node.MediaType {
	case "multipart/alternative":
		return selectAlternative(node)
	case "multipart/related":
		return selectRelated(node)
	default:
		return selectMixed(node)
	}
}

func selectLeaf(node *Node) Presentation {
	if node.MalformedContentType || messageDecodeUnsupported(node.DecodeError) {
		return Presentation{}
	}
	filename := Filename(node)
	isAttachment := strings.EqualFold(node.Disposition, "attachment") || filename != ""
	if !isAttachment && node.MediaType == "text/plain" {
		return Presentation{Text: node, Supported: true}
	}
	if !isAttachment && node.MediaType == "text/html" {
		return Presentation{HTML: node, Supported: true}
	}
	return Presentation{}
}

func selectAlternative(node *Node) Presentation {
	var selected Presentation
	selectedIndex := -1
	var nearestPlainText *Node
	var selectedFallback *Node
	for index, child := range node.Children {
		candidate := selectPresentation(child)
		if !candidate.Supported {
			continue
		}
		selected = candidate
		selectedIndex = index
		selectedFallback = nearestPlainText
		if candidate.Text != nil && candidate.HTML == nil {
			nearestPlainText = candidate.Text
		}
	}
	if selected.HTML != nil && selected.Text == nil {
		selected.Text = selectedFallback
	}
	for index, child := range node.Children {
		if index != selectedIndex {
			selected.Attachments = appendCalendarAttachments(selected.Attachments, child)
		}
	}
	return selected
}

func selectMixed(node *Node) Presentation {
	var selected Presentation
	var attachments []AttachmentCandidate
	for _, child := range node.Children {
		candidate := selectPresentation(child)
		if !selected.Supported && candidate.Supported {
			selected = candidate
			continue
		}
		attachments = appendAttachments(attachments, child, attachmentCollectionGeneric)
	}
	selected.Attachments = append(selected.Attachments, attachments...)
	return selected
}

func selectRelated(node *Node) Presentation {
	if len(node.Children) == 0 {
		return Presentation{}
	}
	rootIndex := 0
	if start := NormalizeContentID(node.MediaParams["start"]); start != "" {
		rootIndex = -1
		for index, child := range node.Children {
			if NormalizeContentID(child.Header.Get("Content-Id")) == start {
				rootIndex = index
				break
			}
		}
		if rootIndex < 0 {
			return Presentation{}
		}
	}
	selected := selectPresentation(node.Children[rootIndex])
	if !selected.Supported {
		return Presentation{}
	}
	for index, child := range node.Children {
		if index != rootIndex {
			selected.Attachments = appendAttachments(selected.Attachments, child, attachmentCollectionSelectedRelated)
		}
	}
	return selected
}

func appendAttachments(attachments []AttachmentCandidate, node *Node, mode attachmentCollectionMode) []AttachmentCandidate {
	if isEncapsulatedMessage(node.MediaType) {
		return append(attachments, AttachmentCandidate{Node: node, ExposeContentID: mode == attachmentCollectionSelectedRelated})
	}
	if len(node.Children) > 0 {
		for _, child := range node.Children {
			attachments = appendAttachments(attachments, child, mode)
		}
		return attachments
	}
	return append(attachments, AttachmentCandidate{Node: node, ExposeContentID: mode == attachmentCollectionSelectedRelated})
}

func appendCalendarAttachments(attachments []AttachmentCandidate, node *Node) []AttachmentCandidate {
	if len(node.Children) > 0 {
		for _, child := range node.Children {
			attachments = appendCalendarAttachments(attachments, child)
		}
		return attachments
	}
	if node.MalformedContentType || messageDecodeUnsupported(node.DecodeError) || !isCalendarPart(node.MediaType, Filename(node)) {
		return attachments
	}
	candidate := AttachmentCandidate{Node: node}
	for _, existing := range attachments {
		if sameCalendarAttachment(existing.Node, node) {
			return attachments
		}
	}
	return append(attachments, candidate)
}

func sameCalendarAttachment(left, right *Node) bool {
	return Filename(left) == Filename(right) && left.MediaType == right.MediaType && bytes.Equal(left.DecodedBody, right.DecodedBody)
}

func messageDecodeUnsupported(err error) bool {
	return message.IsUnknownEncoding(err)
}

func isCalendarPart(contentType, filename string) bool {
	contentType = strings.ToLower(contentType)
	return strings.Contains(contentType, "text/calendar") || strings.Contains(contentType, "application/ics") || strings.HasSuffix(strings.ToLower(filename), ".ics")
}

func Filename(node *Node) string {
	if node.DispositionParams["filename"] != "" {
		return node.DispositionParams["filename"]
	}
	return node.MediaParams["name"]
}

func NormalizeContentID(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 4 && strings.EqualFold(value[:4], "cid:") {
		value = value[4:]
	}
	return strings.Trim(strings.TrimSpace(value), "<>")
}
