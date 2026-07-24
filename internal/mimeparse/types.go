package mimeparse

import "github.com/emersion/go-message"

type Limits struct {
	MaxRawBytes      int
	MaxDepth         int
	MaxParts         int
	MaxHeaderFields  int
	MaxHeaderBytes   int
	MaxPhysicalLines int
}

var InspectionLimits = Limits{
	MaxRawBytes:      25 << 20,
	MaxDepth:         64,
	MaxParts:         4096,
	MaxHeaderFields:  4096,
	MaxHeaderBytes:   1 << 20,
	MaxPhysicalLines: 200000,
}

type ByteRange struct {
	Start int
	End   int
}

type HeaderField struct {
	Name                string
	RawField            ByteRange
	RawValue            ByteRange
	FirstLine, LastLine int
}

type PhysicalLine struct {
	Raw        ByteRange
	Terminator string
}

type Node struct {
	Path                 string
	Header               message.Header
	HeaderFields         []HeaderField
	RawHeader            ByteRange
	RawBody              ByteRange
	BoundaryDelimiters   []ByteRange
	MediaType            string
	MediaParams          map[string]string
	TransferEncoding     string
	Disposition          string
	DispositionParams    map[string]string
	DecodedBody          []byte
	DecodeError          error
	MalformedContentType bool
	MalformedDisposition bool
	BoundaryClosed       bool
	Children             []*Node
}

type AttachmentCandidate struct {
	Node            *Node
	ExposeContentID bool
}

type Presentation struct {
	Text        *Node
	HTML        *Node
	Attachments []AttachmentCandidate
	Supported   bool
}

type Document struct {
	Raw               []byte
	Lines             []PhysicalLine
	Root              *Node
	Presentation      Presentation
	SemanticError     error
	ParsedThroughPath *string
	Truncated         bool
	TruncationCauses  []string
}
