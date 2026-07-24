package mimeparse

import (
	"bytes"
	"mime"
	"strings"
)

type rawIndex struct {
	raw          []byte
	lines        []PhysicalLine
	indexedEnd   int
	complete     bool
	limits       Limits
	headerFields int
	headerBytes  int
	parts        int
	causes       map[string]bool
	meta         map[*Node]rawNodeMeta
	root         *Node
}

type rawNodeMeta struct {
	complete bool
}

func buildRawIndex(raw []byte, limits Limits) *rawIndex {
	index := &rawIndex{raw: raw, limits: limits, causes: make(map[string]bool), meta: make(map[*Node]rawNodeMeta)}
	end := len(raw)
	prefixComplete := true
	if limits.MaxRawBytes > 0 && end > limits.MaxRawBytes {
		end = limits.MaxRawBytes
		prefixComplete = false
		index.causes["raw bytes"] = true
	}
	index.scanLines(end, prefixComplete)
	if !prefixComplete {
		return index
	}
	index.complete = index.indexedEnd == len(raw)
	if len(raw) == 0 || index.indexedEnd == 0 {
		return index
	}
	index.root, _ = index.parseEntity(0, index.indexedEnd, "1", 1, false)
	if index.root != nil {
		index.meta[index.root] = rawNodeMeta{complete: index.complete && index.meta[index.root].complete}
	}
	return index
}

func (index *rawIndex) scanLines(end int, eof bool) {
	position := 0
	for position < end {
		if index.limits.MaxPhysicalLines > 0 && len(index.lines) >= index.limits.MaxPhysicalLines {
			index.causes["physical lines"] = true
			return
		}
		start := position
		for position < end && index.raw[position] != '\r' && index.raw[position] != '\n' {
			position++
		}
		lineEnd := position
		terminator := "EOF"
		if position < end {
			if index.raw[position] == '\r' {
				position++
				terminator = "CR"
				if position < end && index.raw[position] == '\n' {
					position++
					terminator = "CRLF"
				}
			} else {
				position++
				terminator = "LF"
			}
		} else if !eof {
			return
		}
		index.lines = append(index.lines, PhysicalLine{Raw: ByteRange{Start: start, End: lineEnd}, Terminator: terminator})
		index.indexedEnd = position
	}
	if end == 0 {
		index.indexedEnd = 0
	}
}

func (index *rawIndex) parseEntity(start, end int, path string, depth int, digestChild bool) (*Node, bool) {
	if index.limits.MaxDepth > 0 && depth > index.limits.MaxDepth {
		index.causes["MIME depth"] = true
		return nil, false
	}
	if index.limits.MaxParts > 0 && index.parts >= index.limits.MaxParts {
		index.causes["MIME parts"] = true
		return nil, false
	}
	lineStart := index.lineAtOrAfter(start)
	headerEnd := -1
	bodyStart := -1
	for lineNumber := lineStart; lineNumber < len(index.lines); lineNumber++ {
		line := index.lines[lineNumber]
		if line.Raw.Start >= end {
			break
		}
		if line.Raw.End == line.Raw.Start {
			headerEnd = line.Raw.Start
			bodyStart = index.fullLineEnd(lineNumber)
			break
		}
	}
	if headerEnd < 0 || bodyStart > end {
		return nil, false
	}
	blockBytes := bodyStart - start
	if index.limits.MaxHeaderBytes > 0 && index.headerBytes+blockBytes > index.limits.MaxHeaderBytes {
		index.causes["header bytes"] = true
		return nil, false
	}

	fields := index.headerFieldsIn(start, headerEnd)
	if index.limits.MaxHeaderFields > 0 && index.headerFields+len(fields) > index.limits.MaxHeaderFields {
		index.causes["header fields"] = true
		return nil, false
	}
	index.headerBytes += blockBytes
	index.headerFields += len(fields)
	index.parts++

	mediaType := "text/plain"
	mediaParams := map[string]string(nil)
	malformedContentType := false
	if digestChild {
		mediaType = "message/rfc822"
	}
	contentTypeValue, contentTypePresent, dispositionValue, dispositionPresent, transferEncoding := index.entityHeaderValues(fields)
	if contentTypePresent {
		parsedType, parsedParams, err := mime.ParseMediaType(contentTypeValue)
		if err != nil {
			malformedContentType = true
		} else {
			mediaType = strings.ToLower(parsedType)
			mediaParams = parsedParams
		}
	}
	disposition := ""
	dispositionParams := map[string]string(nil)
	malformedDisposition := false
	if dispositionPresent {
		parsedDisposition, parsedParams, err := mime.ParseMediaType(dispositionValue)
		if err != nil {
			malformedDisposition = true
			disposition = strings.ToLower(parsedDisposition)
		} else {
			disposition = strings.ToLower(parsedDisposition)
			dispositionParams = parsedParams
		}
	}
	node := &Node{
		Path:                 path,
		HeaderFields:         fields,
		RawHeader:            ByteRange{Start: start, End: headerEnd},
		RawBody:              ByteRange{Start: bodyStart, End: end},
		MediaType:            mediaType,
		MediaParams:          mediaParams,
		TransferEncoding:     strings.ToLower(strings.TrimSpace(transferEncoding)),
		Disposition:          disposition,
		DispositionParams:    dispositionParams,
		MalformedContentType: malformedContentType,
		MalformedDisposition: malformedDisposition,
	}
	complete := end <= index.indexedEnd

	if strings.HasPrefix(mediaType, "multipart/") {
		boundary := mediaParams["boundary"]
		if boundary != "" {
			delimiters := index.boundaryLines(bodyStart, end, boundary)
			for _, delimiter := range delimiters {
				node.BoundaryDelimiters = append(node.BoundaryDelimiters, ByteRange{Start: index.lines[delimiter.line].Raw.Start, End: index.fullLineEnd(delimiter.line)})
				if delimiter.closing {
					node.BoundaryClosed = true
				}
			}
			childNumber := 0
			for delimiterIndex, delimiter := range delimiters {
				if delimiter.closing {
					break
				}
				childStart := index.fullLineEnd(delimiter.line)
				childEnd := end
				if delimiterIndex+1 < len(delimiters) {
					childEnd = index.lines[delimiters[delimiterIndex+1].line].Raw.Start
					childEnd = trimBoundaryPrefix(index.raw, childStart, childEnd)
				}
				childNumber++
				child, childComplete := index.parseEntity(childStart, childEnd, path+"."+itoa(childNumber), depth+1, mediaType == "multipart/digest")
				if child == nil {
					complete = false
					break
				}
				node.Children = append(node.Children, child)
				if !childComplete {
					complete = false
					break
				}
			}
			if len(delimiters) == 0 || !node.BoundaryClosed {
				complete = false
			}
		}
	}
	if isEncapsulatedMessage(mediaType) && isIdentityTransferEncoding(node.TransferEncoding) {
		child, childComplete := index.parseEntity(bodyStart, end, path+".1", depth+1, false)
		if child == nil {
			complete = false
		} else {
			node.Children = append(node.Children, child)
			if !childComplete {
				complete = false
			}
		}
	}

	index.meta[node] = rawNodeMeta{complete: complete}
	return node, complete
}

type boundaryLine struct {
	line    int
	closing bool
}

func (index *rawIndex) boundaryLines(start, end int, boundary string) []boundaryLine {
	prefix := []byte("--" + boundary)
	var result []boundaryLine
	for lineNumber := index.lineAtOrAfter(start); lineNumber < len(index.lines); lineNumber++ {
		line := index.lines[lineNumber]
		if line.Raw.Start >= end {
			break
		}
		content := index.raw[line.Raw.Start:line.Raw.End]
		if !bytes.HasPrefix(content, prefix) {
			continue
		}
		rest := content[len(prefix):]
		closing := false
		if bytes.HasPrefix(rest, []byte("--")) {
			closing = true
			rest = rest[2:]
		}
		if len(bytes.Trim(rest, " \t")) != 0 {
			continue
		}
		result = append(result, boundaryLine{line: lineNumber, closing: closing})
	}
	return result
}

func (index *rawIndex) headerFieldsIn(start, headerEnd int) []HeaderField {
	var fields []HeaderField
	for lineNumber := index.lineAtOrAfter(start); lineNumber < len(index.lines); lineNumber++ {
		line := index.lines[lineNumber]
		if line.Raw.Start >= headerEnd {
			break
		}
		content := index.raw[line.Raw.Start:line.Raw.End]
		if len(content) > 0 && (content[0] == ' ' || content[0] == '\t') {
			if len(fields) > 0 {
				fields[len(fields)-1].RawField.End = index.fullLineEnd(lineNumber)
				fields[len(fields)-1].RawValue.End = index.fullLineEnd(lineNumber)
				fields[len(fields)-1].LastLine = lineNumber + 1
			}
			continue
		}
		colon := bytes.IndexByte(content, ':')
		if colon < 0 {
			continue
		}
		fields = append(fields, HeaderField{
			Name:      string(content[:colon]),
			RawField:  ByteRange{Start: line.Raw.Start, End: index.fullLineEnd(lineNumber)},
			RawValue:  ByteRange{Start: line.Raw.Start + colon + 1, End: index.fullLineEnd(lineNumber)},
			FirstLine: lineNumber + 1,
			LastLine:  lineNumber + 1,
		})
	}
	return fields
}

func (index *rawIndex) entityHeaderValues(fields []HeaderField) (contentType string, contentTypePresent bool, disposition string, dispositionPresent bool, transferEncoding string) {
	transferEncodingPresent := false
	for _, field := range fields {
		name := strings.TrimSpace(field.Name)
		switch {
		case !contentTypePresent && strings.EqualFold(name, "content-type"):
			contentType = index.unfoldedHeaderValue(field)
			contentTypePresent = true
		case !dispositionPresent && strings.EqualFold(name, "content-disposition"):
			disposition = index.unfoldedHeaderValue(field)
			dispositionPresent = true
		case !transferEncodingPresent && strings.EqualFold(name, "content-transfer-encoding"):
			transferEncoding = index.unfoldedHeaderValue(field)
			transferEncodingPresent = true
		}
		if contentTypePresent && dispositionPresent && transferEncodingPresent {
			break
		}
	}
	return contentType, contentTypePresent, disposition, dispositionPresent, transferEncoding
}

func (index *rawIndex) headerValue(fields []HeaderField, name string) (string, bool) {
	for _, field := range fields {
		if strings.EqualFold(strings.TrimSpace(field.Name), name) {
			return index.unfoldedHeaderValue(field), true
		}
	}
	return "", false
}

func (index *rawIndex) unfoldedHeaderValue(field HeaderField) string {
	raw := bytes.TrimRight(index.raw[field.RawValue.Start:field.RawValue.End], "\r\n")
	if !bytes.ContainsAny(raw, "\r\n") {
		return strings.TrimSpace(string(raw))
	}
	value := make([]byte, 0, len(raw))
	for _, b := range raw {
		if b == '\r' || b == '\n' {
			if len(value) == 0 || value[len(value)-1] != ' ' {
				value = append(value, ' ')
			}
			continue
		}
		value = append(value, b)
	}
	return strings.TrimSpace(string(value))
}

func (index *rawIndex) lineAtOrAfter(offset int) int {
	low, high := 0, len(index.lines)
	for low < high {
		middle := low + (high-low)/2
		if index.lines[middle].Raw.Start < offset {
			low = middle + 1
		} else {
			high = middle
		}
	}
	return low
}

func (index *rawIndex) fullLineEnd(line int) int {
	if line+1 < len(index.lines) {
		return index.lines[line+1].Raw.Start
	}
	return index.indexedEnd
}

func trimBoundaryPrefix(raw []byte, start, end int) int {
	if end >= start+2 && raw[end-2] == '\r' && raw[end-1] == '\n' {
		return end - 2
	}
	if end > start && (raw[end-1] == '\r' || raw[end-1] == '\n') {
		return end - 1
	}
	return end
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var buffer [20]byte
	position := len(buffer)
	for value > 0 {
		position--
		buffer[position] = byte('0' + value%10)
		value /= 10
	}
	return string(buffer[position:])
}
