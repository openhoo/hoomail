package mimeparse

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/emersion/go-message"
	_ "github.com/emersion/go-message/charset"
)

var truncationCauseOrder = []string{"raw bytes", "MIME depth", "MIME parts", "header fields", "header bytes", "physical lines"}

func Parse(raw []byte, limits Limits) (Document, error) {
	document := Document{Raw: raw}
	inspection := limits != (Limits{})
	var indexed *rawIndex
	if inspection {
		indexed = buildRawIndex(raw, limits)
		document.Lines = indexed.lines
		for _, cause := range truncationCauseOrder {
			if indexed.causes[cause] {
				document.Truncated = true
				document.TruncationCauses = append(document.TruncationCauses, cause)
			}
		}
		if indexed.causes["raw bytes"] || len(raw) == 0 {
			return document, nil
		}
		if indexed.root == nil {
			document.SemanticError = errors.New("raw MIME header is incomplete")
			return document, nil
		}
		if document.Truncated {
			document.Root = indexed.root
			document.ParsedThroughPath = lastCompleteIndexedPath(indexed.root, indexed)
			return document, nil
		}
	}
	if len(raw) == 0 {
		return document, nil
	}

	maxHeaderBytes := int64(-1)
	if !inspection {
		maxHeaderBytes = 1 << 20
	}
	entity, entityErr := message.ReadWithOptions(bytes.NewReader(raw), &message.ReadOptions{MaxHeaderBytes: maxHeaderBytes})
	if entityErr != nil && !message.IsUnknownCharset(entityErr) && !message.IsUnknownEncoding(entityErr) {
		document.SemanticError = entityErr
		return document, nil
	}
	if entity == nil {
		document.SemanticError = entityErr
		if document.SemanticError == nil {
			document.SemanticError = errors.New("message parser returned a nil entity")
		}
		return document, nil
	}

	var rawRoot *Node
	if indexed != nil {
		rawRoot = indexed.root
		if rawRoot == nil {
			document.SemanticError = errors.New("raw MIME header is incomplete")
			return document, nil
		}
	}
	root, semanticErr, parsedPath, invariantErr := readSemanticNode(entity, entityErr, "1", rawRoot, indexed)
	if invariantErr != nil {
		return Document{}, invariantErr
	}
	document.Root = root
	document.SemanticError = semanticErr
	if semanticErr != nil && parsedPath != "" {
		document.ParsedThroughPath = stringPointer(parsedPath)
	}
	if semanticErr == nil && !document.Truncated {
		document.ParsedThroughPath = nil
	}
	if root != nil {
		document.Presentation = selectPresentation(root)
	}
	return document, nil
}

func readSemanticNode(entity *message.Entity, entityErr error, path string, rawNode *Node, index *rawIndex) (*Node, error, string, error) {
	if entity == nil {
		return nil, entityErr, "", nil
	}
	mediaType, mediaParams, contentTypeErr := entity.Header.ContentType()
	disposition, dispositionParams, dispositionErr := entity.Header.ContentDisposition()
	node := &Node{
		Path:                 path,
		Header:               entity.Header,
		MediaType:            strings.ToLower(mediaType),
		MediaParams:          mediaParams,
		TransferEncoding:     strings.ToLower(strings.TrimSpace(entity.Header.Get("Content-Transfer-Encoding"))),
		Disposition:          strings.ToLower(disposition),
		DispositionParams:    dispositionParams,
		DecodeError:          entityErr,
		MalformedContentType: entity.Header.Get("Content-Type") != "" && contentTypeErr != nil,
		MalformedDisposition: entity.Header.Get("Content-Disposition") != "" && dispositionErr != nil,
	}
	if rawNode != nil {
		if rawNode.Path != path {
			return nil, nil, "", fmt.Errorf("mimeparse: raw path %q does not match semantic path %q", rawNode.Path, path)
		}
		node.HeaderFields = rawNode.HeaderFields
		node.RawHeader = rawNode.RawHeader
		node.RawBody = rawNode.RawBody
		node.BoundaryDelimiters = rawNode.BoundaryDelimiters
		node.BoundaryClosed = rawNode.BoundaryClosed
		node.TransferEncoding = rawNode.TransferEncoding
		node.Disposition = rawNode.Disposition
		node.DispositionParams = rawNode.DispositionParams
		node.MalformedContentType = rawNode.MalformedContentType
		node.MalformedDisposition = rawNode.MalformedDisposition
		if rawNode.MalformedContentType {
			node.MediaType = rawNode.MediaType
			node.MediaParams = rawNode.MediaParams
		} else if rawNode.MediaType != "" && !strings.EqualFold(rawNode.MediaType, node.MediaType) {
			if rawNode.MediaType == "message/rfc822" && node.MediaType == "text/plain" && entity.Header.Get("Content-Type") == "" {
				node.MediaType = rawNode.MediaType
			} else {
				return nil, nil, "", fmt.Errorf("mimeparse: raw media type %q does not match semantic media type %q at %s", rawNode.MediaType, node.MediaType, path)
			}
		}
	}

	if isEncapsulatedMessage(node.MediaType) && rawNode != nil && len(rawNode.Children) > 0 {
		body, err := io.ReadAll(entity.Body)
		if err != nil {
			node.DecodeError = err
			return node, err, "", nil
		}
		node.DecodedBody = body
		childEntity, childEntityErr := message.ReadWithOptions(bytes.NewReader(body), &message.ReadOptions{MaxHeaderBytes: -1})
		if childEntityErr != nil && !message.IsUnknownCharset(childEntityErr) && !message.IsUnknownEncoding(childEntityErr) {
			return node, childEntityErr, "", nil
		}
		if childEntity == nil {
			if childEntityErr == nil {
				childEntityErr = errors.New("message parser returned a nil encapsulated entity")
			}
			return node, childEntityErr, "", nil
		}
		child, childErr, childComplete, invariantErr := readSemanticNode(childEntity, childEntityErr, path+".1", rawNode.Children[0], index)
		if invariantErr != nil {
			return nil, nil, "", invariantErr
		}
		if child != nil {
			node.Children = append(node.Children, child)
		}
		return node, childErr, childComplete, nil
	}

	multipartReader := entity.MultipartReader()
	if multipartReader == nil || node.MalformedContentType {
		body, err := io.ReadAll(entity.Body)
		if err != nil {
			node.DecodeError = err
			return node, err, "", nil
		}
		node.DecodedBody = body
		return node, nil, path, nil
	}
	lastComplete := ""
	childIndex := 0
	for {
		part, partErr := multipartReader.NextPart()
		if errors.Is(partErr, io.EOF) {
			if rawNode != nil && childIndex != len(rawNode.Children) {
				return nil, nil, "", fmt.Errorf("mimeparse: raw child count %d does not match semantic child count %d at %s", len(rawNode.Children), childIndex, path)
			}
			return node, nil, lastComplete, nil
		}
		if partErr != nil && !message.IsUnknownCharset(partErr) && !message.IsUnknownEncoding(partErr) {
			return node, partErr, lastComplete, nil
		}
		if part == nil {
			return node, partErr, lastComplete, nil
		}
		childIndex++
		var rawChild *Node
		if rawNode != nil {
			if childIndex > len(rawNode.Children) {
				return nil, nil, "", fmt.Errorf("mimeparse: semantic child %d has no raw node at %s", childIndex, path)
			}
			rawChild = rawNode.Children[childIndex-1]
		}
		childPath := path + "." + itoa(childIndex)
		child, childErr, childComplete, invariantErr := readSemanticNode(part, partErr, childPath, rawChild, index)
		if invariantErr != nil {
			return nil, nil, "", invariantErr
		}
		if child != nil {
			node.Children = append(node.Children, child)
		}
		if childComplete != "" {
			lastComplete = childComplete
		}
		if childErr != nil {
			return node, childErr, lastComplete, nil
		}
	}
}

func isEncapsulatedMessage(mediaType string) bool {
	return strings.EqualFold(mediaType, "message/rfc822") || strings.EqualFold(mediaType, "message/global")
}

func isIdentityTransferEncoding(encoding string) bool {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "", "7bit", "8bit", "binary":
		return true
	default:
		return false
	}
}

func lastCompleteIndexedPath(root *Node, index *rawIndex) *string {
	if root == nil {
		return nil
	}
	var last string
	stack := []*Node{root}
	for len(stack) > 0 {
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if index.meta[node].complete {
			last = node.Path
		}
		for child := len(node.Children) - 1; child >= 0; child-- {
			stack = append(stack, node.Children[child])
		}
	}
	if last == "" {
		return nil
	}
	return stringPointer(last)
}

func stringPointer(value string) *string { return &value }
