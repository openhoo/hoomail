package inspect

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/quotedprintable"
	"net/mail"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	messagemail "github.com/emersion/go-message/mail"
	"github.com/openhoo/hoomail/internal/mimeparse"
)

var (
	parseMIME       = mimeparse.Parse
	analyzeHTMLPass = analyzeHTML
	fieldNameRE     = regexp.MustCompile(`^[!-9;-~]+$`)
	msgIDRE         = regexp.MustCompile(`^<[^<>\s@]+@(?:[^<>\s]+|\[[^\]\r\n]+\])>$`)
	tokenRE         = regexp.MustCompile(`^[!#$%&'*+\-.^_` + "`" + `|~0-9A-Za-z]+$`)
	dkimBase64RE    = regexp.MustCompile(`^[A-Za-z0-9+/]*={0,2}$`)
	textResourceRE  = regexp.MustCompile(`(?i)https?://[^\s<>"')\]]+`)
)

var categoryOrder = map[string]int{
	"analysis": 0, "message": 1, "mime": 2, "authentication": 3,
	"unsubscribe": 4, "content": 5, "privacy": 6, "compatibility": 7,
}

var truncationOrder = []string{
	"raw bytes", "MIME depth", "MIME parts", "header fields", "header bytes",
	"physical lines", "legacy body bytes", "HTML nodes", "HTML token bytes",
	"resource values", "resources", "findings", "evidence", "report bytes",
}

var unavailableOrder = []string{"message", "mime", "authentication", "unsubscribe", "content", "privacy", "compatibility"}

type analyzer struct {
	input       Input
	doc         mimeparse.Document
	findings    []Finding
	resources   []Resource
	resourceMap map[string]int
	causes      map[string]struct{}
	unavailable map[string]struct{}
	rawPresent  bool
	wholeRaw    bool
	semanticOK  bool
	html        []byte
	text        []byte
	htmlPath    *string
	textPath    *string
	fallback    bool
	facts       *HTMLFacts
}

type headerOccurrence struct {
	name       string
	value      string
	line       int
	occurrence int
	field      mimeparse.HeaderField
}

func Analyze(input Input) (Report, error) {
	doc, err := parseMIME(input.Raw, mimeparse.InspectionLimits)
	if err != nil {
		return Report{}, err
	}
	a := analyzer{
		input: input, doc: doc, resourceMap: make(map[string]int), causes: make(map[string]struct{}),
		unavailable: make(map[string]struct{}), rawPresent: len(input.Raw) != 0,
	}
	for _, cause := range doc.TruncationCauses {
		a.addCause(normalizeCause(cause))
	}
	if doc.Truncated && len(doc.TruncationCauses) == 0 {
		a.addCause("MIME parts")
	}
	a.wholeRaw = a.rawPresent && !doc.Truncated
	a.semanticOK = a.rawPresent && doc.Root != nil && doc.SemanticError == nil && !hasCause(a.causes, "raw bytes")
	a.selectBodies()
	a.runAnalysisRules()
	if a.rawPresent && doc.Root != nil && !hasCause(a.causes, "raw bytes") {
		if doc.Truncated {
			a.addUnavailable("mime")
		}
		a.runMessageRules()
		a.runMIMERules()
		a.runAuthenticationRules()
		a.runUnsubscribeRules()
	}
	a.analyzeContent()
	a.runContentRules()
	a.runPrivacyRules()
	a.runCompatibilityRules()
	a.addAttachmentResources()

	report := a.finishReport()
	return report, nil
}

func (a *analyzer) selectBodies() {
	if a.semanticOK && a.doc.Presentation.Supported {
		if node := a.doc.Presentation.HTML; node != nil && node.DecodeError == nil {
			a.html, a.htmlPath = node.DecodedBody, stringPointer(node.Path)
		}
		if node := a.doc.Presentation.Text; node != nil && node.DecodeError == nil {
			a.text, a.textPath = node.DecodedBody, stringPointer(node.Path)
		}
		return
	}
	a.fallback = true
	a.html = boundedLegacyBody(a.input.LegacyHTML, a)
	a.text = boundedLegacyBody(a.input.LegacyText, a)
}

func boundedLegacyBody(value *string, a *analyzer) []byte {
	if value == nil {
		return nil
	}
	limit := len(*value)
	if mimeparse.InspectionLimits.MaxRawBytes > 0 && limit > mimeparse.InspectionLimits.MaxRawBytes {
		limit = mimeparse.InspectionLimits.MaxRawBytes
		for limit > 0 && !utf8.RuneStart((*value)[limit]) {
			limit--
		}
		a.addCause("legacy body bytes")
	}
	return []byte((*value)[:limit])
}

func (a *analyzer) runAnalysisRules() {
	if !a.rawPresent {
		a.addUnavailable("message", "mime", "authentication", "unsubscribe")
		a.addFinding(finding("analysis.raw-unavailable", "analysis", "observed", "none", "evidence", "all", "Raw message unavailable", "Raw headers, MIME structure, authentication evidence, and unsubscribe syntax cannot be evaluated; stored selected bodies are legacy fallbacks.", nil, nil))
		a.addFinding(finding("analysis.parse", "analysis", "not-evaluated", "none", "evidence", "all", "MIME parsing", "Semantic MIME parsing was skipped because raw message bytes are unavailable.", nil, nil))
		return
	}
	if hasCause(a.causes, "raw bytes") {
		a.addUnavailable("message", "mime", "authentication", "unsubscribe")
		a.addFinding(finding("analysis.parse", "analysis", "not-evaluated", "none", "evidence", "all", "MIME parsing", "Semantic MIME parsing was deliberately skipped because the raw message exceeds the offline inspection byte limit.", nil, nil))
		return
	}
	if a.doc.SemanticError != nil {
		a.addUnavailable("mime")
		value := evidenceValue(a.doc.SemanticError.Error())
		a.addFinding(finding("analysis.parse", "analysis", "fail", "error", "standard", "all", "MIME parsing", "Semantic MIME parsing stopped at a sender-supplied syntax or decoding defect; completed raw evidence remains available.", []Evidence{{Source: "mime-part", Path: a.doc.ParsedThroughPath, Value: &value}}, rfc("RFC 2045", "https://www.rfc-editor.org/rfc/rfc2045")))
		return
	}
	a.addFinding(finding("analysis.parse", "analysis", "observed", "none", "evidence", "all", "MIME parsing", "Semantic MIME parsing completed. This is static offline analysis and does not establish transport or delivery conformance.", nil, nil))
}

func (a *analyzer) runMessageRules() {
	root := a.doc.Root
	top := a.headerOccurrences(root)
	complete := a.wholeRaw && a.doc.SemanticError == nil
	a.ruleLineEndings(complete)
	a.ruleHeaderSyntax(root, "message.header-syntax", "Message header syntax", complete, false)
	a.ruleLineLength(complete)
	a.ruleDate(top, complete)
	fromCount, fromGroups, fromValid := a.ruleFrom(top)
	a.ruleSender(top, fromCount, fromGroups, fromValid)
	a.ruleMessageID(top)
	a.ruleSingletons(top)
	for _, name := range []string{"Reply-To", "To", "Cc", "Bcc"} {
		a.ruleAddressField(top, name)
	}
	for _, name := range []string{"In-Reply-To", "References"} {
		a.ruleThreading(top, name)
	}
	a.ruleInternationalizedHeaders()
}

func (a *analyzer) ruleLineEndings(complete bool) {
	var evidence []Evidence
	for index, line := range a.doc.Lines {
		if line.Terminator == "CR" || line.Terminator == "LF" {
			lineNumber := index + 1
			value := line.Terminator
			evidence = append(evidence, Evidence{Source: "raw-line", Line: &lineNumber, Value: &value})
		}
	}
	if len(evidence) != 0 {
		a.addFinding(finding("message.line-endings", "message", "fail", "error", "standard", "all", "Line endings", "Bare CR or LF line endings were found; Internet Message Format requires CRLF line endings.", evidence, rfc("RFC 5322 §2.3", "https://www.rfc-editor.org/rfc/rfc5322#section-2.3")))
	} else if complete {
		a.addFinding(finding("message.line-endings", "message", "pass", "none", "standard", "all", "Line endings", "All indexed physical lines use CRLF termination or the final EOF boundary.", nil, rfc("RFC 5322 §2.3", "https://www.rfc-editor.org/rfc/rfc5322#section-2.3")))
	}
}

func (a *analyzer) ruleHeaderSyntax(node *mimeparse.Node, id, label string, complete, nested bool) {
	defect, line, value := inspectHeaderBlock(a.doc.Raw, node)
	category := "message"
	source := "raw-header"
	path := (*string)(nil)
	if nested {
		category, path = "mime", stringPointer(node.Path)
	}
	if defect != "" {
		evidence := Evidence{Source: source, Path: path}
		if line > 0 {
			evidence.Line = &line
		}
		if value != "" {
			v := evidenceValue(value)
			evidence.Value = &v
		}
		a.addFinding(finding(id, category, "fail", "error", "standard", "all", label, defect, []Evidence{evidence}, rfc("RFC 5322 §2.2", "https://www.rfc-editor.org/rfc/rfc5322#section-2.2")))
	} else if complete {
		a.addFinding(finding(id, category, "pass", "none", "standard", "all", label, "The completed header block has valid field-line structure.", nil, rfc("RFC 5322 §2.2", "https://www.rfc-editor.org/rfc/rfc5322#section-2.2")))
	}
}

func inspectHeaderBlock(raw []byte, node *mimeparse.Node) (string, int, string) {
	if node == nil || !validRange(node.RawHeader, len(raw)) {
		return "", 0, ""
	}
	block := raw[node.RawHeader.Start:node.RawHeader.End]
	lineNo := 0
	start := 0
	seenField := false
	for start < len(block) {
		lineNo++
		end := start
		for end < len(block) && block[end] != '\r' && block[end] != '\n' {
			end++
		}
		line := block[start:end]
		if len(line) == 0 {
			return "", 0, ""
		}
		if line[0] == ' ' || line[0] == '\t' {
			if !seenField {
				return "An orphan header continuation line was found.", lineNo, string(line)
			}
		} else {
			colon := bytes.IndexByte(line, ':')
			if colon < 0 {
				return "A header field line is missing its colon separator.", lineNo, string(line)
			}
			name := string(line[:colon])
			if name == "" || !fieldNameRE.MatchString(name) || !isASCII(name) {
				return "A header field name is empty or contains invalid characters.", lineNo, name
			}
			seenField = true
		}
		for _, b := range line {
			if b < 32 && b != '\t' {
				return "A prohibited control byte was found in a header field.", lineNo, string(line)
			}
		}
		if end >= len(block) {
			break
		}
		if block[end] == '\r' && end+1 < len(block) && block[end+1] == '\n' {
			start = end + 2
		} else {
			start = end + 1
		}
	}
	return "", 0, ""
}

func (a *analyzer) ruleLineLength(complete bool) {
	maxOctets, maxChars := 0, 0
	longLine, longCharsLine := 0, 0
	for index, line := range a.doc.Lines {
		if !validRange(line.Raw, len(a.doc.Raw)) {
			continue
		}
		value := a.doc.Raw[line.Raw.Start:line.Raw.End]
		if len(value) > maxOctets {
			maxOctets, longLine = len(value), index+1
		}
		if utf8.Valid(value) {
			count := utf8.RuneCount(value)
			if count > maxChars {
				maxChars, longCharsLine = count, index+1
			}
		}
	}
	if maxOctets > 998 {
		value := fmt.Sprintf("%d octets", maxOctets)
		a.addFinding(finding("message.line-length", "message", "fail", "error", "standard", "all", "Line length", "At least one physical line exceeds the 998-octet maximum.", []Evidence{{Source: "raw-line", Line: &longLine, Value: &value}}, rfc("RFC 5322 §2.1.1", "https://www.rfc-editor.org/rfc/rfc5322#section-2.1.1")))
	} else if maxOctets > 78 || maxChars > 78 {
		line := longLine
		value := fmt.Sprintf("maximum %d octets; %d Unicode characters", maxOctets, maxChars)
		if maxChars > 78 {
			line = longCharsLine
		}
		a.addFinding(finding("message.line-length", "message", "pass", "warning", "recommendation", "all", "Line length", "At least one physical line exceeds the recommended 78-character line length.", []Evidence{{Source: "raw-line", Line: &line, Value: &value}}, rfc("RFC 5322 §2.1.1", "https://www.rfc-editor.org/rfc/rfc5322#section-2.1.1")))
	} else if complete {
		value := fmt.Sprintf("maximum %d octets", maxOctets)
		a.addFinding(finding("message.line-length", "message", "pass", "none", "standard", "all", "Line length", "All indexed physical lines are within the standard maximum and recommendation.", []Evidence{{Source: "raw-line", Value: &value}}, rfc("RFC 5322 §2.1.1", "https://www.rfc-editor.org/rfc/rfc5322#section-2.1.1")))
	}
}

func (a *analyzer) ruleDate(fields []headerOccurrence, complete bool) {
	values := fieldsNamed(fields, "Date")
	if len(values) != 1 {
		a.addFinding(finding("message.date", "message", "fail", "error", "standard", "all", "Date", "Exactly one Date field is required.", occurrencesEvidence(values, "Date", nil), rfc("RFC 5322 §3.6", "https://www.rfc-editor.org/rfc/rfc5322#section-3.6")))
		return
	}
	h := messagemail.Header{Header: a.doc.Root.Header}
	if _, err := h.Date(); err != nil {
		a.addFinding(finding("message.date", "message", "fail", "error", "standard", "all", "Date", "The Date field is not a complete valid date-time.", occurrencesEvidence(values, "Date", nil), rfc("RFC 5322 §3.3", "https://www.rfc-editor.org/rfc/rfc5322#section-3.3")))
	} else if complete {
		a.addFinding(finding("message.date", "message", "pass", "none", "standard", "all", "Date", "Exactly one syntactically valid Date field is present.", nil, rfc("RFC 5322 §3.6", "https://www.rfc-editor.org/rfc/rfc5322#section-3.6")))
	}
}

func (a *analyzer) ruleFrom(fields []headerOccurrence) (int, bool, bool) {
	values := fieldsNamed(fields, "From")
	if len(values) != 1 {
		a.addFinding(finding("message.from", "message", "fail", "error", "standard", "all", "From", "Exactly one From field is required.", occurrencesEvidence(values, "From", nil), rfc("RFC 5322 §3.6.2", "https://www.rfc-editor.org/rfc/rfc5322#section-3.6.2")))
		return 0, false, false
	}
	count, groups, err := parseAddressField(values[0].value, false)
	if err != nil || count == 0 && !groups {
		a.addFinding(finding("message.from", "message", "fail", "error", "standard", "all", "From", "The From field is not a complete valid originator address list.", occurrencesEvidence(values, "From", nil), rfc("RFC 6854", "https://www.rfc-editor.org/rfc/rfc6854")))
		return count, groups, false
	}
	severity, basis, detail := "none", "standard", "The From field contains a valid mailbox list."
	if groups {
		severity, basis, detail = "warning", "recommendation", "The From field uses group syntax; RFC 6854 recommends limiting this form for interoperability."
	}
	a.addFinding(finding("message.from", "message", "pass", severity, basis, "all", "From", detail, nil, rfc("RFC 6854", "https://www.rfc-editor.org/rfc/rfc6854")))
	return count, groups, true
}

func (a *analyzer) ruleSender(fields []headerOccurrence, fromCount int, fromGroups, fromValid bool) {
	values := fieldsNamed(fields, "Sender")
	required := fromValid && fromCount > 1
	if len(values) == 0 && !required {
		return
	}
	if len(values) != 1 {
		detail := "Sender is required when From identifies multiple mailboxes and must occur exactly once."
		a.addFinding(finding("message.sender", "message", "fail", "error", "standard", "all", "Sender", detail, occurrencesEvidence(values, "Sender", nil), rfc("RFC 5322 §3.6.2", "https://www.rfc-editor.org/rfc/rfc5322#section-3.6.2")))
		return
	}
	count, groups, err := parseAddressField(values[0].value, false)
	if err != nil || count != 1 {
		a.addFinding(finding("message.sender", "message", "fail", "error", "standard", "all", "Sender", "Sender must contain exactly one valid mailbox.", occurrencesEvidence(values, "Sender", nil), rfc("RFC 5322 §3.6.2", "https://www.rfc-editor.org/rfc/rfc5322#section-3.6.2")))
		return
	}
	if groups {
		a.addFinding(finding("message.sender", "message", "pass", "warning", "recommendation", "all", "Sender", "Sender uses a single-mailbox group form; direct mailbox form is more interoperable.", nil, rfc("RFC 6854", "https://www.rfc-editor.org/rfc/rfc6854")))
	} else if !required && fromCount == 1 && !fromGroups {
		a.addFinding(finding("message.sender", "message", "pass", "warning", "recommendation", "all", "Sender", "Sender is syntactically valid but unnecessary for a single-mailbox From field.", nil, rfc("RFC 5322 §3.6.2", "https://www.rfc-editor.org/rfc/rfc5322#section-3.6.2")))
	} else {
		a.addFinding(finding("message.sender", "message", "pass", "none", "standard", "all", "Sender", "The required Sender field contains exactly one valid mailbox.", nil, rfc("RFC 5322 §3.6.2", "https://www.rfc-editor.org/rfc/rfc5322#section-3.6.2")))
	}
}

func (a *analyzer) ruleMessageID(fields []headerOccurrence) {
	values := fieldsNamed(fields, "Message-ID")
	if len(values) == 0 {
		a.addFinding(finding("message.message-id", "message", "pass", "warning", "recommendation", "all", "Message-ID", "No Message-ID field is present; including one improves threading and interoperability.", nil, rfc("RFC 5322 §3.6.4", "https://www.rfc-editor.org/rfc/rfc5322#section-3.6.4")))
	} else if len(values) != 1 || !validMessageID(values[0].value) {
		a.addFinding(finding("message.message-id", "message", "fail", "error", "standard", "all", "Message-ID", "Message-ID, when present, must occur once with one complete msg-id value.", occurrencesEvidence(values, "Message-ID", nil), rfc("RFC 5322 §3.6.4", "https://www.rfc-editor.org/rfc/rfc5322#section-3.6.4")))
	} else {
		a.addFinding(finding("message.message-id", "message", "pass", "none", "standard", "all", "Message-ID", "One syntactically valid Message-ID is present.", nil, rfc("RFC 5322 §3.6.4", "https://www.rfc-editor.org/rfc/rfc5322#section-3.6.4")))
	}
}

func (a *analyzer) ruleSingletons(fields []headerOccurrence) {
	var duplicates []Evidence
	for _, name := range []string{"Reply-To", "To", "Cc", "Bcc", "In-Reply-To", "References", "Subject"} {
		values := fieldsNamed(fields, name)
		if len(values) > 1 {
			duplicates = append(duplicates, occurrencesEvidence(values, name, nil)...)
		}
	}
	if len(duplicates) != 0 {
		a.addFinding(finding("message.singletons", "message", "fail", "error", "standard", "all", "Singleton fields", "One or more fields that may occur at most once are duplicated.", duplicates, rfc("RFC 5322 §3.6", "https://www.rfc-editor.org/rfc/rfc5322#section-3.6")))
	} else {
		a.addFinding(finding("message.singletons", "message", "pass", "none", "standard", "all", "Singleton fields", "No duplicate singleton destination, threading, or subject fields were found.", nil, rfc("RFC 5322 §3.6", "https://www.rfc-editor.org/rfc/rfc5322#section-3.6")))
	}
}

func (a *analyzer) ruleAddressField(fields []headerOccurrence, name string) {
	values := fieldsNamed(fields, name)
	if len(values) == 0 {
		return
	}
	allowEmpty := strings.EqualFold(name, "Bcc")
	_, _, err := parseAddressField(values[0].value, allowEmpty)
	id := "message.address-fields." + strings.ToLower(name)
	if len(values) > 1 || err != nil {
		a.addFinding(finding(id, "message", "fail", "error", "standard", "all", name+" addresses", "The field does not contain one complete valid address list.", occurrencesEvidence(values, name, nil), rfc("RFC 5322 §3.6.3", "https://www.rfc-editor.org/rfc/rfc5322#section-3.6.3")))
	} else {
		a.addFinding(finding(id, "message", "pass", "none", "standard", "all", name+" addresses", "The address-list syntax is valid.", nil, rfc("RFC 5322 §3.6.3", "https://www.rfc-editor.org/rfc/rfc5322#section-3.6.3")))
	}
}

func (a *analyzer) ruleThreading(fields []headerOccurrence, name string) {
	values := fieldsNamed(fields, name)
	if len(values) == 0 {
		return
	}
	valid := len(values) == 1 && validMessageIDList(values[0].value)
	id := "message.threading." + strings.ToLower(name)
	if !valid {
		a.addFinding(finding(id, "message", "fail", "error", "standard", "all", name, "The field must contain one or more complete msg-id values separated only by comments or folding whitespace.", occurrencesEvidence(values, name, nil), rfc("RFC 5322 §3.6.4", "https://www.rfc-editor.org/rfc/rfc5322#section-3.6.4")))
	} else {
		a.addFinding(finding(id, "message", "pass", "none", "standard", "all", name, "The threading field contains complete msg-id values.", nil, rfc("RFC 5322 §3.6.4", "https://www.rfc-editor.org/rfc/rfc5322#section-3.6.4")))
	}
}

func (a *analyzer) ruleInternationalizedHeaders() {
	var invalid, direct []Evidence
	stack := []*mimeparse.Node{a.doc.Root}
	for len(stack) > 0 {
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for index := len(node.Children) - 1; index >= 0; index-- {
			stack = append(stack, node.Children[index])
		}
		for _, field := range node.HeaderFields {
			if !validRange(field.RawValue, len(a.doc.Raw)) {
				continue
			}
			value := a.doc.Raw[field.RawValue.Start:field.RawValue.End]
			path := node.Path
			fieldName := field.Name
			occurrence := 1
			ev := Evidence{Source: "raw-header", Path: &path, Field: &fieldName, Occurrence: &occurrence}
			if !utf8.Valid(value) {
				invalid = append(invalid, ev)
			} else if !isASCIIBytes(value) {
				direct = append(direct, ev)
			}
		}
	}
	if len(invalid) != 0 {
		a.addFinding(finding("message.internationalized-headers", "message", "fail", "error", "standard", "all", "Internationalized headers", "At least one raw header field body is not valid UTF-8.", invalid, rfc("RFC 6532", "https://www.rfc-editor.org/rfc/rfc6532")))
	} else if len(direct) != 0 {
		a.addFinding(finding("message.internationalized-headers", "message", "observed", "none", "evidence", "all", "Internationalized headers", "RFC 6532 syntax observed; SMTPUTF8 transport authorization not available in stored data.", direct, rfc("RFC 6532", "https://www.rfc-editor.org/rfc/rfc6532")))
	} else if a.wholeRaw {
		a.addFinding(finding("message.internationalized-headers", "message", "pass", "none", "standard", "all", "Internationalized headers", "All indexed header field bodies are ASCII or use encoded-word syntax.", nil, rfc("RFC 6532", "https://www.rfc-editor.org/rfc/rfc6532")))
	}
}

func (a *analyzer) runMIMERules() {
	root := a.doc.Root
	topFields := a.headerOccurrences(root)
	mimeTriggered := len(fieldsNamed(topFields, "Content-Type"))+len(fieldsNamed(topFields, "Content-Transfer-Encoding"))+len(fieldsNamed(topFields, "MIME-Version")) > 0 || strings.HasPrefix(root.MediaType, "multipart/") || strings.HasPrefix(root.MediaType, "message/")
	if mimeTriggered {
		values := fieldsNamed(topFields, "MIME-Version")
		valid := len(values) == 1 && strings.TrimSpace(stripComments(values[0].value)) == "1.0"
		if valid {
			a.addFinding(finding("mime.version", "mime", "pass", "none", "standard", "all", "MIME-Version", "Exactly one MIME-Version: 1.0 field is present.", nil, rfc("RFC 2045 §4", "https://www.rfc-editor.org/rfc/rfc2045#section-4")))
		} else {
			a.addFinding(finding("mime.version", "mime", "fail", "error", "standard", "all", "MIME-Version", "MIME content requires exactly one comment-normalized MIME-Version: 1.0 field.", occurrencesEvidence(values, "MIME-Version", nil), rfc("RFC 2045 §4", "https://www.rfc-editor.org/rfc/rfc2045#section-4")))
		}
	}
	if a.doc.SemanticError != nil || a.doc.Truncated {
		a.addFinding(finding("mime.structure", "mime", "observed", "none", "evidence", "all", "MIME structure", "Only the completed MIME prefix is reported; analysis.parse or analysis.truncated owns the partial condition.", nil, rfc("RFC 2046", "https://www.rfc-editor.org/rfc/rfc2046")))
		return
	} else if detail := structuralDefect(root); detail != "" {
		a.addFinding(finding("mime.structure", "mime", "fail", "error", "standard", "all", "MIME structure", detail, nil, rfc("RFC 2046", "https://www.rfc-editor.org/rfc/rfc2046")))
	} else {
		a.addFinding(finding("mime.structure", "mime", "pass", "none", "standard", "all", "MIME structure", "Multipart boundaries and completed MIME structure are internally consistent.", nil, rfc("RFC 2046", "https://www.rfc-editor.org/rfc/rfc2046")))
	}

	stack := []*mimeparse.Node{root}
	for len(stack) > 0 {
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for index := len(node.Children) - 1; index >= 0; index-- {
			stack = append(stack, node.Children[index])
		}
		a.ruleMIMEContentType(node)
		if node != root {
			a.ruleHeaderSyntax(node, "mime.header-syntax."+node.Path, "MIME part header syntax", a.wholeRaw && !a.doc.Truncated, true)
		}
		a.ruleMIMETransfer(node)
		a.ruleMIMECharset(node)
		a.ruleMIMEDisposition(node)
		a.ruleMIMEContentID(node)
		if strings.EqualFold(node.MediaType, "multipart/related") {
			a.ruleRelated(node)
		}
		if strings.EqualFold(node.MediaType, "multipart/alternative") {
			a.ruleAlternative(node)
		}
	}
}

func structuralDefect(node *mimeparse.Node) string {
	stack := []*mimeparse.Node{node}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if strings.HasPrefix(n.MediaType, "multipart/") {
			if strings.TrimSpace(n.MediaParams["boundary"]) == "" {
				return "A multipart entity is missing a valid boundary parameter."
			}
			if !n.BoundaryClosed {
				return "A multipart entity has no closing boundary delimiter."
			}
		}
		stack = append(stack, n.Children...)
	}
	return ""
}

func (a *analyzer) ruleMIMEContentType(node *mimeparse.Node) {
	fields := a.headerOccurrences(node)
	values := fieldsNamed(fields, "Content-Type")
	bad := len(values) > 1 || node.MalformedContentType || strings.HasPrefix(node.MediaType, "multipart/") && strings.TrimSpace(node.MediaParams["boundary"]) == ""
	id := "mime.content-type." + node.Path
	path := stringPointer(node.Path)
	if bad {
		a.addFinding(finding(id, "mime", "fail", "error", "standard", "all", "Content-Type "+node.Path, "The effective media type has malformed syntax, a duplicate field, or a multipart boundary defect.", occurrencesEvidence(values, "Content-Type", path), rfc("RFC 2045 §5", "https://www.rfc-editor.org/rfc/rfc2045#section-5")))
	} else {
		a.addFinding(finding(id, "mime", "pass", "none", "standard", "all", "Content-Type "+node.Path, "The explicit Content-Type or applicable RFC default is syntactically valid.", []Evidence{{Source: "mime-part", Path: path, Value: stringPointer(node.MediaType)}}, rfc("RFC 2045 §5", "https://www.rfc-editor.org/rfc/rfc2045#section-5")))
	}
}

func (a *analyzer) ruleMIMETransfer(node *mimeparse.Node) {
	fields := a.headerOccurrences(node)
	values := fieldsNamed(fields, "Content-Transfer-Encoding")
	id := "mime.transfer-encoding." + node.Path
	path := stringPointer(node.Path)
	encoding := strings.ToLower(strings.TrimSpace(node.TransferEncoding))
	if encoding == "" {
		encoding = "7bit"
	}
	if len(values) > 1 || !tokenRE.MatchString(encoding) {
		a.addFinding(finding(id, "mime", "fail", "error", "standard", "all", "Transfer encoding "+node.Path, "Content-Transfer-Encoding is duplicated or is not a valid token.", occurrencesEvidence(values, "Content-Transfer-Encoding", path), rfc("RFC 2045 §6", "https://www.rfc-editor.org/rfc/rfc2045#section-6")))
		return
	}
	if len(node.Children) != 0 && encoding != "7bit" && encoding != "8bit" && encoding != "binary" {
		a.addFinding(finding(id, "mime", "fail", "error", "standard", "all", "Transfer encoding "+node.Path, "Composite MIME entities may only use 7bit, 8bit, or binary transfer encoding.", occurrencesEvidence(values, "Content-Transfer-Encoding", path), rfc("RFC 2045 §6.4", "https://www.rfc-editor.org/rfc/rfc2045#section-6.4")))
		return
	}
	body, bodyKnown := rawBody(a.doc.Raw, node)
	if encoding == "base64" && bodyKnown {
		if invalidBase64(body) || encodedLineOver(body, 76) {
			a.addFinding(finding(id, "mime", "fail", "error", "standard", "all", "Transfer encoding "+node.Path, "The base64 payload is malformed or has an encoded line longer than 76 characters.", []Evidence{{Source: "mime-part", Path: path}}, rfc("RFC 2045 §6.8", "https://www.rfc-editor.org/rfc/rfc2045#section-6.8")))
			return
		}
	}
	if encoding == "quoted-printable" && bodyKnown {
		if invalidQuotedPrintable(body) || encodedLineOver(body, 76) {
			a.addFinding(finding(id, "mime", "fail", "error", "standard", "all", "Transfer encoding "+node.Path, "The quoted-printable payload is malformed or has an encoded line longer than 76 characters.", []Evidence{{Source: "mime-part", Path: path}}, rfc("RFC 2045 §6.7", "https://www.rfc-editor.org/rfc/rfc2045#section-6.7")))
			return
		}
	}
	if (encoding == "7bit" || encoding == "8bit") && bodyKnown {
		contradiction := transferDomainContradiction(body, encoding)
		if contradiction != "" {
			a.addFinding(finding(id, "mime", "observed", "none", "evidence", "all", "Transfer encoding "+node.Path, contradiction+" Hoomail does not retain BODY or 8BITMIME transport metadata.", []Evidence{{Source: "mime-part", Path: path}}, rfc("RFC 2045 §6", "https://www.rfc-editor.org/rfc/rfc2045#section-6")))
			return
		}
	}
	if encoding != "7bit" && encoding != "8bit" && encoding != "binary" && encoding != "base64" && encoding != "quoted-printable" {
		a.addFinding(finding(id, "mime", "observed", "none", "evidence", "all", "Transfer encoding "+node.Path, "A syntactically valid but unsupported transfer encoding was observed; content remains opaque.", []Evidence{{Source: "mime-part", Path: path, Value: &encoding}}, rfc("RFC 2045 §6", "https://www.rfc-editor.org/rfc/rfc2045#section-6")))
		return
	}
	a.addFinding(finding(id, "mime", "pass", "none", "standard", "all", "Transfer encoding "+node.Path, "The transfer-encoding declaration and available raw payload checks are syntactically valid.", nil, rfc("RFC 2045 §6", "https://www.rfc-editor.org/rfc/rfc2045#section-6")))
}

func (a *analyzer) ruleMIMECharset(node *mimeparse.Node) {
	if !strings.HasPrefix(strings.ToLower(node.MediaType), "text/") {
		return
	}
	id := "mime.charset." + node.Path
	path := stringPointer(node.Path)
	charset := strings.TrimSpace(node.MediaParams["charset"])
	if charset == "" && node.DecodeError == nil && !isASCIIBytes(node.DecodedBody) {
		a.addFinding(finding(id, "mime", "fail", "error", "standard", "all", "Charset "+node.Path, "Non-ASCII decoded text relies on the US-ASCII default charset.", []Evidence{{Source: "mime-part", Path: path}}, rfc("RFC 2045 §5.2", "https://www.rfc-editor.org/rfc/rfc2045#section-5.2")))
	} else if node.DecodeError != nil {
		detail := "The declared charset or text decoder failed; recipients may not display the part interoperably."
		value := evidenceValue(node.DecodeError.Error())
		a.addFinding(finding(id, "mime", "pass", "warning", "recommendation", "all", "Charset "+node.Path, detail, []Evidence{{Source: "mime-part", Path: path, Value: &value}}, rfc("RFC 2045 §5", "https://www.rfc-editor.org/rfc/rfc2045#section-5")))
	} else {
		a.addFinding(finding(id, "mime", "pass", "none", "standard", "all", "Charset "+node.Path, "Text charset conversion completed or the US-ASCII default is safe for the decoded content.", nil, rfc("RFC 2045 §5", "https://www.rfc-editor.org/rfc/rfc2045#section-5")))
	}
}

func (a *analyzer) ruleMIMEDisposition(node *mimeparse.Node) {
	fields := a.headerOccurrences(node)
	values := fieldsNamed(fields, "Content-Disposition")
	if len(values) == 0 {
		return
	}
	id := "mime.disposition." + node.Path
	path := stringPointer(node.Path)
	if len(values) > 1 || node.MalformedDisposition {
		a.addFinding(finding(id, "mime", "fail", "error", "standard", "all", "Disposition "+node.Path, "Content-Disposition is duplicated or malformed.", occurrencesEvidence(values, "Content-Disposition", path), rfc("RFC 2183", "https://www.rfc-editor.org/rfc/rfc2183")))
		return
	}
	filename := node.DispositionParams["filename"]
	if dangerousFilename(filename) {
		a.addFinding(finding(id, "mime", "pass", "warning", "recommendation", "all", "Disposition "+node.Path, "The filename contains dangerous path or terminal components and should be sanitized by recipients.", occurrencesEvidence(values, "Content-Disposition", path), rfc("RFC 2183 §2.3", "https://www.rfc-editor.org/rfc/rfc2183#section-2.3")))
		return
	}
	disposition := strings.ToLower(strings.TrimSpace(node.Disposition))
	if disposition != "inline" && disposition != "attachment" {
		a.addFinding(finding(id, "mime", "observed", "none", "evidence", "all", "Disposition "+node.Path, "An unrecognized disposition token was observed; recipients are expected to treat it as attachment.", []Evidence{{Source: "mime-part", Path: path, Value: &disposition}}, rfc("RFC 2183 §2.8", "https://www.rfc-editor.org/rfc/rfc2183#section-2.8")))
		return
	}
	a.addFinding(finding(id, "mime", "pass", "none", "standard", "all", "Disposition "+node.Path, "Content-Disposition syntax and any filename are safe for static presentation.", nil, rfc("RFC 2183", "https://www.rfc-editor.org/rfc/rfc2183")))
}

func (a *analyzer) ruleMIMEContentID(node *mimeparse.Node) {
	values := fieldsNamed(a.headerOccurrences(node), "Content-ID")
	if len(values) == 0 {
		return
	}
	id := "mime.content-id." + node.Path
	path := stringPointer(node.Path)
	if len(values) > 1 || !validMessageID(values[0].value) {
		a.addFinding(finding(id, "mime", "fail", "error", "standard", "all", "Content-ID "+node.Path, "Content-ID is duplicated or is not a complete msg-id.", occurrencesEvidence(values, "Content-ID", path), rfc("RFC 2045 §7", "https://www.rfc-editor.org/rfc/rfc2045#section-7")))
	} else {
		a.addFinding(finding(id, "mime", "pass", "none", "standard", "all", "Content-ID "+node.Path, "The Content-ID field contains one complete msg-id.", nil, rfc("RFC 2045 §7", "https://www.rfc-editor.org/rfc/rfc2045#section-7")))
	}
}

func (a *analyzer) ruleRelated(node *mimeparse.Node) {
	id := "mime.related." + node.Path
	path := stringPointer(node.Path)
	typeParam := strings.TrimSpace(node.MediaParams["type"])
	if len(node.Children) == 0 || typeParam == "" {
		a.addFinding(finding(id, "mime", "fail", "error", "standard", "all", "Related structure "+node.Path, "multipart/related requires children and a valid type parameter.", []Evidence{{Source: "mime-part", Path: path}}, rfc("RFC 2387", "https://www.rfc-editor.org/rfc/rfc2387")))
		return
	}
	root := node.Children[0]
	start := normalizeContentID(node.MediaParams["start"])
	if start != "" {
		matches := 0
		for _, child := range node.Children {
			if normalizeContentID(child.Header.Get("Content-ID")) == start {
				root = child
				matches++
			}
		}
		if matches != 1 {
			a.addFinding(finding(id, "mime", "fail", "error", "standard", "all", "Related structure "+node.Path, "The start parameter must resolve to exactly one direct child.", []Evidence{{Source: "mime-part", Path: path}}, rfc("RFC 2387 §3", "https://www.rfc-editor.org/rfc/rfc2387#section-3")))
			return
		}
	}
	if !strings.EqualFold(typeParam, root.MediaType) {
		a.addFinding(finding(id, "mime", "fail", "error", "standard", "all", "Related structure "+node.Path, "The multipart/related type parameter does not match the selected root media type.", []Evidence{{Source: "mime-part", Path: path}}, rfc("RFC 2387 §3", "https://www.rfc-editor.org/rfc/rfc2387#section-3")))
		return
	}
	a.addFinding(finding(id, "mime", "pass", "none", "standard", "all", "Related structure "+node.Path, "The related root, type, and optional start reference are internally consistent.", nil, rfc("RFC 2387", "https://www.rfc-editor.org/rfc/rfc2387")))
}

func (a *analyzer) ruleAlternative(node *mimeparse.Node) {
	id := "mime.alternative." + node.Path
	htmlIndex, textIndex := -1, -1
	for index, child := range node.Children {
		candidate := mimeparse.SelectPresentation(child)
		if !candidate.Supported {
			continue
		}
		if candidate.HTML != nil {
			htmlIndex = index
		}
		if candidate.Text != nil && candidate.HTML == nil {
			textIndex = index
		}
	}
	if htmlIndex >= 0 && textIndex < 0 {
		a.addFinding(finding(id, "mime", "pass", "warning", "recommendation", "all", "Alternative ordering "+node.Path, "HTML is available without a plain-text alternative; including both improves interoperability and accessibility.", nil, rfc("RFC 2046 §5.1.4", "https://www.rfc-editor.org/rfc/rfc2046#section-5.1.4")))
	} else if htmlIndex >= 0 && textIndex > htmlIndex {
		a.addFinding(finding(id, "mime", "pass", "warning", "recommendation", "all", "Alternative ordering "+node.Path, "A later supported plain-text child makes plain text preferred over the earlier HTML child.", nil, rfc("RFC 2046 §5.1.4", "https://www.rfc-editor.org/rfc/rfc2046#section-5.1.4")))
	} else {
		a.addFinding(finding(id, "mime", "pass", "none", "recommendation", "all", "Alternative ordering "+node.Path, "Supported alternatives are ordered from plain text toward richer HTML presentation.", nil, rfc("RFC 2046 §5.1.4", "https://www.rfc-editor.org/rfc/rfc2046#section-5.1.4")))
	}
}

func (a *analyzer) runAuthenticationRules() {
	fields := a.headerOccurrences(a.doc.Root)
	for index, item := range fieldsNamed(fields, "Authentication-Results") {
		id := fmt.Sprintf("authentication.results.%d", index+1)
		authserv, ok := parseAuthenticationResults(item.value)
		ev := occurrencesEvidence([]headerOccurrence{item}, "Authentication-Results", nil)
		if !ok {
			a.addFinding(finding(id, "authentication", "fail", "error", "standard", "all", "Authentication-Results", "The Authentication-Results field has malformed static syntax.", ev, rfc("RFC 8601", "https://www.rfc-editor.org/rfc/rfc8601")))
		} else {
			a.addFinding(finding(id, "authentication", "observed", "none", "evidence", "all", "Authentication-Results", "Observed assertion from "+authserv+"; trust not established.", ev, rfc("RFC 8601 §5", "https://www.rfc-editor.org/rfc/rfc8601#section-5")))
		}
	}
	dkimClaims := make([]map[string]string, 0)
	for index, item := range fieldsNamed(fields, "DKIM-Signature") {
		id := fmt.Sprintf("authentication.dkim.%d", index+1)
		tags, err := parseTagList(item.value)
		ev := occurrencesEvidence([]headerOccurrence{item}, "DKIM-Signature", nil)
		if err == nil {
			err = validateDKIM(tags, false)
		}
		if err != nil {
			value := evidenceValue(err.Error())
			ev = append(ev, Evidence{Source: "raw-header", Field: stringPointer("DKIM-Signature"), Value: &value})
			a.addFinding(finding(id, "authentication", "fail", "error", "standard", "all", "DKIM signature", "The DKIM-Signature field is structurally malformed. Signature and body hash were not cryptographically verified.", ev, rfc("RFC 6376", "https://www.rfc-editor.org/rfc/rfc6376")))
			continue
		}
		dkimClaims = append(dkimClaims, tags)
		algorithm := strings.ToLower(tags["a"])
		if algorithm == "rsa-sha1" || tags["l"] != "" {
			a.addFinding(finding(id, "authentication", "pass", "warning", "recommendation", "all", "DKIM signature", "The signature uses obsolete rsa-sha1 or the risky l= body-length tag. Signature and body hash were not cryptographically verified.", ev, rfc("RFC 8301", "https://www.rfc-editor.org/rfc/rfc8301")))
		} else if algorithm != "rsa-sha256" && algorithm != "ed25519-sha256" {
			a.addFinding(finding(id, "authentication", "observed", "none", "evidence", "all", "DKIM signature", "A syntactically valid unrecognized algorithm was observed. Signature and body hash were not cryptographically verified.", ev, rfc("RFC 6376", "https://www.rfc-editor.org/rfc/rfc6376")))
		} else {
			a.addFinding(finding(id, "authentication", "observed", "none", "evidence", "all", "DKIM signature", "A structurally valid DKIM signature was observed. Signature and body hash were not cryptographically verified.", ev, rfc("RFC 6376", "https://www.rfc-editor.org/rfc/rfc6376")))
		}
	}
	a.runARCRule(fields)
	a.runUnsubscribeDKIMCoverage(fields, dkimClaims)
}

func (a *analyzer) runARCRule(fields []headerOccurrence) {
	aar := fieldsNamed(fields, "ARC-Authentication-Results")
	ams := fieldsNamed(fields, "ARC-Message-Signature")
	seal := fieldsNamed(fields, "ARC-Seal")
	if len(aar)+len(ams)+len(seal) == 0 {
		return
	}
	type arcSet struct {
		aar, ams, seal bool
		cv             string
	}
	sets := map[int]*arcSet{}
	failed := ""
	for _, item := range aar {
		i, ok := parseARCInstanceFromAAR(item.value)
		if !ok {
			failed = "ARC-Authentication-Results has malformed syntax or a missing instance."
			break
		}
		set := sets[i]
		if set == nil {
			set = &arcSet{}
			sets[i] = set
		}
		if set.aar {
			failed = "An ARC instance repeats ARC-Authentication-Results."
			break
		}
		set.aar = true
	}
	if failed == "" {
		for _, item := range ams {
			tags, err := parseTagList(item.value)
			if err == nil {
				err = validateDKIM(tags, true)
			}
			i, e := strconv.Atoi(tags["i"])
			if err != nil || e != nil || i < 1 || i > 50 {
				failed = "ARC-Message-Signature has malformed static syntax."
				break
			}
			set := sets[i]
			if set == nil {
				set = &arcSet{}
				sets[i] = set
			}
			if set.ams {
				failed = "An ARC instance repeats ARC-Message-Signature."
				break
			}
			set.ams = true
		}
	}
	if failed == "" {
		for _, item := range seal {
			tags, err := parseTagList(item.value)
			if err == nil {
				err = validateARCSeal(tags)
			}
			i, e := strconv.Atoi(tags["i"])
			if err != nil || e != nil || i < 1 || i > 50 {
				failed = "ARC-Seal has malformed static syntax."
				break
			}
			set := sets[i]
			if set == nil {
				set = &arcSet{}
				sets[i] = set
			}
			if set.seal {
				failed = "An ARC instance repeats ARC-Seal."
				break
			}
			set.seal = true
			set.cv = strings.ToLower(tags["cv"])
		}
	}
	if failed == "" {
		for i := 1; i <= len(sets); i++ {
			set := sets[i]
			if set == nil || !set.aar || !set.ams || !set.seal {
				failed = "ARC instances have a gap or an incomplete set."
				break
			}
			if i == 1 && set.cv != "none" || i > 1 && set.cv != "pass" {
				failed = "ARC-Seal cv values do not follow the static none/pass sequence."
				break
			}
		}
	}
	if failed != "" {
		a.addFinding(finding("authentication.arc", "authentication", "fail", "error", "standard", "all", "ARC chain", failed+" Chain signatures and custody were not cryptographically validated.", nil, rfc("RFC 8617", "https://www.rfc-editor.org/rfc/rfc8617")))
	} else {
		a.addFinding(finding("authentication.arc", "authentication", "observed", "none", "evidence", "all", "ARC chain", "ARC set structure observed; chain signatures and custody were not cryptographically validated.", nil, rfc("RFC 8617", "https://www.rfc-editor.org/rfc/rfc8617")))
	}
}

func (a *analyzer) runUnsubscribeRules() {
	fields := a.headerOccurrences(a.doc.Root)
	list := fieldsNamed(fields, "List-Unsubscribe")
	post := fieldsNamed(fields, "List-Unsubscribe-Post")
	uris, listOK := parseListUnsubscribe(list)
	if len(list) == 0 {
		a.addFinding(finding("unsubscribe.list", "unsubscribe", "not-evaluated", "none", "evidence", "unknown", "List-Unsubscribe", "No List-Unsubscribe field is present; stored bytes do not establish whether this transactional or mailing-list guidance applies.", nil, rfc("RFC 2369 §4", "https://www.rfc-editor.org/rfc/rfc2369#section-4")))
	} else if !listOK {
		a.addFinding(finding("unsubscribe.list", "unsubscribe", "fail", "error", "standard", "mailing-list", "List-Unsubscribe", "List-Unsubscribe is duplicated or has invalid angle-bracket, comma, or URI syntax.", occurrencesEvidence(list, "List-Unsubscribe", nil), rfc("RFC 2369 §4", "https://www.rfc-editor.org/rfc/rfc2369#section-4")))
	} else {
		a.addFinding(finding("unsubscribe.list", "unsubscribe", "pass", "none", "standard", "mailing-list", "List-Unsubscribe", "One syntactically valid List-Unsubscribe field is present.", occurrencesEvidence(list, "List-Unsubscribe", nil), rfc("RFC 2369 §4", "https://www.rfc-editor.org/rfc/rfc2369#section-4")))
	}
	if len(post) != 0 {
		valid := len(post) == 1 && strings.TrimSpace(post[0].value) == "List-Unsubscribe=One-Click" && listOK
		httpsCount := 0
		for _, u := range uris {
			if strings.EqualFold(u.Scheme, "https") {
				httpsCount++
			}
		}
		valid = valid && httpsCount == 1
		if valid {
			a.addFinding(finding("unsubscribe.one-click-pairing", "unsubscribe", "pass", "none", "standard", "one-click-claim", "One-click unsubscribe pairing", "The static RFC 8058 header pair is valid. DKIM validity, endpoint behavior, recipient binding, and operational eligibility are unverified.", append(occurrencesEvidence(list, "List-Unsubscribe", nil), occurrencesEvidence(post, "List-Unsubscribe-Post", nil)...), rfc("RFC 8058", "https://www.rfc-editor.org/rfc/rfc8058")))
		} else {
			a.addFinding(finding("unsubscribe.one-click-pairing", "unsubscribe", "fail", "error", "standard", "one-click-claim", "One-click unsubscribe pairing", "The one-click claim requires exactly one post field with List-Unsubscribe=One-Click and exactly one HTTPS unsubscribe URI. Endpoint behavior is unverified.", append(occurrencesEvidence(list, "List-Unsubscribe", nil), occurrencesEvidence(post, "List-Unsubscribe-Post", nil)...), rfc("RFC 8058", "https://www.rfc-editor.org/rfc/rfc8058")))
		}
	}
	if len(list)+len(post) > 0 {
		a.addFinding(finding("unsubscribe.bulk-guidance", "unsubscribe", "observed", "none", "evidence", "bulk-marketing", "Bulk-sender guidance", "Gmail and Yahoo one-click requirements are conditional provider guidance; Hoomail cannot determine sender volume or message class from stored bytes.", nil, &Reference{Label: "Google Email sender guidelines", URL: "https://support.google.com/a/answer/81126"}))
	}
}

func (a *analyzer) runUnsubscribeDKIMCoverage(fields []headerOccurrence, claims []map[string]string) {
	if len(fieldsNamed(fields, "List-Unsubscribe-Post")) == 0 {
		return
	}
	var evidence []Evidence
	for index, tags := range claims {
		headers := strings.Split(strings.ToLower(tags["h"]), ":")
		if containsFold(headers, "list-unsubscribe") && containsFold(headers, "list-unsubscribe-post") {
			n := index + 1
			value := "DKIM h= claims both unsubscribe fields"
			evidence = append(evidence, Evidence{Source: "raw-header", Field: stringPointer("DKIM-Signature"), Occurrence: &n, Value: &value})
		}
	}
	if len(evidence) > 0 {
		a.addFinding(finding("unsubscribe.dkim-coverage", "unsubscribe", "observed", "none", "evidence", "one-click-claim", "Claimed DKIM coverage", "Structurally valid DKIM h= lists claim both one-click fields; cryptographic coverage was not verified.", evidence, rfc("RFC 8058 §3.1", "https://www.rfc-editor.org/rfc/rfc8058#section-3.1")))
	} else {
		a.addFinding(finding("unsubscribe.dkim-coverage", "unsubscribe", "pass", "warning", "recommendation", "one-click-claim", "Claimed DKIM coverage", "No structurally valid DKIM h= list claims both one-click fields; DKIM validity itself is not verified.", nil, rfc("RFC 8058 §3.1", "https://www.rfc-editor.org/rfc/rfc8058#section-3.1")))
	}
}

func (a *analyzer) analyzeContent() {
	if a.html != nil {
		facts, causes := analyzeHTMLPass(a.html, a.htmlPath)
		a.facts = &facts
		for _, cause := range causes {
			a.addCause(cause)
		}
		for _, occurrence := range facts.resources {
			a.addResource(occurrence.kind, occurrence.path, occurrence.url, occurrence.text)
		}
		return
	}
	if a.text != nil {
		for _, matchRange := range textResourceRE.FindAllIndex(a.text, -1) {
			if matchRange[1]-matchRange[0] > 2048 {
				a.addCause("resource values")
				continue
			}
			match := string(a.text[matchRange[0]:matchRange[1]])
			u, err := url.Parse(match)
			if err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" {
				a.addResource("link", a.textPath, match, "")
			}
		}
	}
}

func (a *analyzer) runContentRules() {
	if a.fallback {
		empty := len(a.html) == 0 && len(a.text) == 0
		detail := "Stored fallback bodies were inspected, but MIME presentation selection could not be reconstructed; fallback content is non-empty."
		if empty {
			detail = "Stored fallback bodies were inspected, but MIME presentation selection could not be reconstructed; fallback content is empty."
		}
		a.addFinding(finding("content.body", "content", "not-evaluated", "none", "evidence", "all", "Message body", detail, nil, nil))
		a.addFinding(finding("content.alternative", "content", "not-evaluated", "none", "evidence", "all", "Body alternatives", "Stored fallback bodies cannot prove MIME alternative selection or ordering.", nil, nil))
	} else {
		hasHTML := len(a.html) != 0
		hasText := len(a.text) != 0
		if !hasHTML && !hasText {
			a.addFinding(finding("content.body", "content", "pass", "warning", "recommendation", "all", "Message body", "The authoritative selected text and HTML bodies are both empty.", nil, rfc("RFC 2046", "https://www.rfc-editor.org/rfc/rfc2046")))
		} else {
			a.addFinding(finding("content.body", "content", "pass", "none", "recommendation", "all", "Message body", "At least one authoritative selected body is non-empty.", nil, nil))
		}
		if hasHTML && !hasText {
			a.addFinding(finding("content.alternative", "content", "pass", "warning", "recommendation", "all", "Body alternatives", "Authoritative HTML is selected without a plain-text fallback.", nil, rfc("RFC 2046 §5.1.4", "https://www.rfc-editor.org/rfc/rfc2046#section-5.1.4")))
		} else if hasHTML && hasText {
			a.addFinding(finding("content.alternative", "content", "pass", "none", "recommendation", "all", "Body alternatives", "Authoritative HTML and plain-text representations are available.", nil, rfc("RFC 2046 §5.1.4", "https://www.rfc-editor.org/rfc/rfc2046#section-5.1.4")))
		} else if hasText {
			a.addFinding(finding("content.alternative", "content", "observed", "none", "evidence", "all", "Body alternatives", "The authoritative selected presentation is plain text only.", nil, nil))
		}
	}
	if a.facts == nil {
		return
	}
	fallbackLabel := ""
	if a.fallback {
		fallbackLabel = " in the stored fallback HTML"
	}
	if a.facts.imageCount > 0 {
		if a.facts.missingAlt > 0 {
			detail := fmt.Sprintf("%d image occurrence(s)%s omit the alt attribute; empty alt is treated as present.", a.facts.missingAlt, fallbackLabel)
			a.addFinding(finding("content.image-alt", "content", "pass", "advisory", "heuristic", "html", "Image alt attributes", detail, a.facts.missingAltEvidence, nil))
		} else if !a.facts.truncated {
			a.addFinding(finding("content.image-alt", "content", "pass", "none", "heuristic", "html", "Image alt attributes", "Every inspected image declares an alt attribute.", nil, nil))
		}
	}
	if a.facts.anchorCount > 0 {
		if a.facts.unnamedLinks > 0 {
			detail := fmt.Sprintf("%d anchor occurrence(s)%s have no conservative static accessible name.", a.facts.unnamedLinks, fallbackLabel)
			a.addFinding(finding("content.link-name", "content", "pass", "advisory", "heuristic", "html", "Link names", detail, a.facts.unnamedEvidence, nil))
		} else if !a.facts.truncated {
			a.addFinding(finding("content.link-name", "content", "pass", "none", "heuristic", "html", "Link names", "Every inspected anchor has a conservative static accessible name.", nil, nil))
		}
	}
	if a.facts.explicitHTMLRoot {
		if a.facts.lang == "" || a.facts.langInvalid {
			a.addFinding(finding("content.document-language", "content", "pass", "advisory", "heuristic", "html", "Document language", "The explicit source html root is missing a valid lang value.", nil, nil))
		} else if !a.facts.truncated {
			a.addFinding(finding("content.document-language", "content", "pass", "none", "heuristic", "html", "Document language", "The explicit source html root declares a parseable language tag.", nil, nil))
		}
	}
}

func (a *analyzer) runPrivacyRules() {
	if a.facts == nil {
		return
	}
	if a.facts.externalImages > 0 {
		a.addFinding(finding("privacy.external-images", "privacy", "pass", "advisory", "heuristic", "html", "External images", fmt.Sprintf("%d external image occurrence(s) were found. Inspection is offline and did not fetch them.", a.facts.externalImages), a.facts.externalEvidence, nil))
	} else if !a.facts.truncated {
		a.addFinding(finding("privacy.external-images", "privacy", "pass", "none", "heuristic", "html", "External images", "No external HTTP(S) image source was found. Inspection made no network requests.", nil, nil))
	}
	if a.facts.trackingPixels > 0 {
		a.addFinding(finding("privacy.tracking-pixels", "privacy", "pass", "advisory", "heuristic", "html", "Tracking pixels", fmt.Sprintf("%d hidden or at-most-one-pixel image candidate(s) were found; false positives and negatives are possible.", a.facts.trackingPixels), a.facts.trackingEvidence, nil))
	} else if !a.facts.truncated {
		a.addFinding(finding("privacy.tracking-pixels", "privacy", "pass", "none", "heuristic", "html", "Tracking pixels", "No hidden or at-most-one-pixel image candidate was found; this heuristic is not exhaustive.", nil, nil))
	}
	if a.facts.insecure > 0 {
		a.addFinding(finding("privacy.insecure-links", "privacy", "pass", "advisory", "heuristic", "html", "Insecure resources", fmt.Sprintf("%d HTTP resource occurrence(s) use plaintext transport syntax.", a.facts.insecure), a.facts.insecureEvidence, nil))
	} else if !a.facts.truncated {
		a.addFinding(finding("privacy.insecure-links", "privacy", "pass", "none", "heuristic", "html", "Insecure resources", "No inspected link or external image uses an http:// destination.", nil, nil))
	}
}

func (a *analyzer) runCompatibilityRules() {
	if a.html != nil {
		size := len(a.html)
		value := fmt.Sprintf("%d decoded HTML bytes", size)
		evidence := []Evidence{{Source: "html", Path: a.htmlPath, Value: &value}}
		if size > 102*1024 {
			a.addFinding(finding("compatibility.html-size", "compatibility", "pass", "advisory", "heuristic", "html", "HTML size", fmt.Sprintf("Selected decoded HTML is %d bytes. Commonly observed Gmail clipping risk; provider behavior can change.", size), evidence, nil))
		} else {
			a.addFinding(finding("compatibility.html-size", "compatibility", "observed", "none", "evidence", "html", "HTML size", fmt.Sprintf("Selected decoded HTML is %d bytes, at or below the 102 KiB observation threshold.", size), evidence, nil))
		}
	}
	var detail string
	if a.rawPresent {
		detail = fmt.Sprintf("Raw message bytes: %d; selected decoded HTML bytes: %d; selected decoded text bytes: %d", len(a.input.Raw), len(a.html), len(a.text))
		if node := a.doc.Presentation.HTML; node != nil {
			if body, ok := rawBody(a.doc.Raw, node); ok {
				detail += fmt.Sprintf("; encoded selected HTML part bytes: %d", len(body))
			}
		}
		if node := a.doc.Presentation.Text; node != nil {
			if body, ok := rawBody(a.doc.Raw, node); ok {
				detail += fmt.Sprintf("; encoded selected text part bytes: %d", len(body))
			}
		}
	} else {
		detail = fmt.Sprintf("Persisted fallback size: %d; stored fallback HTML bytes: %d; stored fallback text bytes: %d. These are not raw or encoded MIME byte counts.", a.input.StoredSize, len(a.html), len(a.text))
	}
	a.addFinding(finding("compatibility.body-sizes", "compatibility", "observed", "none", "evidence", "all", "Body sizes", detail, nil, nil))
}

func (a *analyzer) addAttachmentResources() {
	attachments := a.doc.Presentation.Attachments
	if a.doc.Root != nil && !a.doc.Presentation.Supported {
		attachments = collectLeafAttachments(a.doc.Root, attachments)
	}
	for _, candidate := range attachments {
		node := candidate.Node
		if node == nil {
			continue
		}
		filename := node.DispositionParams["filename"]
		if filename == "" {
			filename = node.MediaParams["name"]
		}
		a.addResource("attachment", stringPointer(node.Path), "", filename)
	}
}

func collectLeafAttachments(root *mimeparse.Node, out []mimeparse.AttachmentCandidate) []mimeparse.AttachmentCandidate {
	stack := []*mimeparse.Node{root}
	for len(stack) > 0 {
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if len(node.Children) == 0 {
			out = append(out, mimeparse.AttachmentCandidate{Node: node})
			continue
		}
		for index := len(node.Children) - 1; index >= 0; index-- {
			stack = append(stack, node.Children[index])
		}
	}
	return out
}

func (a *analyzer) finishReport() Report {
	if len(a.findings) >= MaxFindings {
		a.findings = a.findings[:MaxFindings-1]
		a.addCause("findings")
	}
	if len(a.causes) > 0 {
		a.ensureTruncationFinding()
	}
	sort.SliceStable(a.findings, func(i, j int) bool { return findingLess(a.findings[i], a.findings[j]) })
	report := Report{Analysis: Analysis{Version: AnalysisVersion, State: "complete", ParsedThroughPath: a.doc.ParsedThroughPath, UnavailableRuleFamilies: a.unavailableFamilies(), Truncated: len(a.causes) > 0}, Findings: a.findings, Resources: a.resources, MIMETree: buildMIMETree(a.doc.Root, a.doc.Raw)}
	if !a.rawPresent || a.doc.SemanticError != nil || len(a.causes) > 0 {
		report.Analysis.State = "partial"
	}
	if report.Analysis.State == "complete" {
		report.Analysis.ParsedThroughPath = nil
	}
	if !a.rawPresent || a.doc.Root == nil || hasCause(a.causes, "raw bytes") {
		report.MIMETree = nil
	}
	report.Summary = summarize(report.Findings)
	if encoded, _ := json.Marshal(report); len(encoded) > MaxReportBytes {
		a.addCause("report bytes")
		a.ensureTruncationFinding()
		report.Findings = a.findings
		sort.SliceStable(report.Findings, func(i, j int) bool { return findingLess(report.Findings[i], report.Findings[j]) })
		report.Analysis.State = "partial"
		report.Analysis.Truncated = true
		report.Resources = a.resources
		report.Analysis.UnavailableRuleFamilies = a.unavailableFamilies()
		report.Summary = summarize(report.Findings)
		trimReport(&report)
	}
	return report
}

func (a *analyzer) ensureTruncationFinding() {
	detail := "Analysis limits reached: " + strings.Join(a.orderedCauses(), ", ") + ". Only the deterministic completed prefix is reported."
	for i := range a.findings {
		if a.findings[i].ID == "analysis.truncated" {
			a.findings[i].Detail = detail
			return
		}
	}
	marker := finding("analysis.truncated", "analysis", "observed", "none", "evidence", "all", "Analysis truncated", detail, nil, nil)
	if len(a.findings) >= MaxFindings {
		for i := len(a.findings) - 1; i >= 0; i-- {
			if a.findings[i].ID != "analysis.truncated" {
				a.findings = append(a.findings[:i], a.findings[i+1:]...)
				break
			}
		}
	}
	a.findings = append(a.findings, marker)
}

func (a *analyzer) addFinding(item Finding) {
	if item.Evidence == nil {
		item.Evidence = []Evidence{}
	}
	if len(item.Evidence) > MaxEvidencePerFinding {
		item.Evidence = item.Evidence[:MaxEvidencePerFinding]
		item.EvidenceTruncated = true
		a.addCause("evidence")
	}
	for i := range item.Evidence {
		if item.Evidence[i].Value != nil {
			v := evidenceValue(*item.Evidence[i].Value)
			item.Evidence[i].Value = &v
		}
	}
	a.findings = append(a.findings, item)
}

func (a *analyzer) addResource(kind string, path *string, rawURL, text string) {
	if len(rawURL) > 2048 || len(text) > 2048 {
		a.addCause("resource values")
		return
	}
	key := kind + "\x00" + pointerValue(path) + "\x00" + rawURL
	if index, ok := a.resourceMap[key]; ok {
		a.resources[index].OccurrenceCount++
		if a.resources[index].Text == "" && text != "" {
			a.resources[index].Text = text
		}
		return
	}
	if len(a.resources) >= MaxResources {
		a.addCause("resources")
		return
	}
	a.resourceMap[key] = len(a.resources)
	a.resources = append(a.resources, Resource{Kind: kind, Path: path, URL: rawURL, Text: text, OccurrenceCount: 1})
}

func (a *analyzer) addCause(cause string) {
	if cause != "" {
		a.causes[cause] = struct{}{}
	}
}
func (a *analyzer) addUnavailable(values ...string) {
	for _, v := range values {
		a.unavailable[v] = struct{}{}
	}
}
func (a *analyzer) unavailableFamilies() []string {
	out := make([]string, 0)
	for _, v := range unavailableOrder {
		if _, ok := a.unavailable[v]; ok {
			out = append(out, v)
		}
	}
	return out
}
func (a *analyzer) orderedCauses() []string {
	out := make([]string, 0, len(a.causes))
	for _, v := range truncationOrder {
		if _, ok := a.causes[v]; ok {
			out = append(out, v)
		}
	}
	return out
}

func (a *analyzer) headerOccurrences(node *mimeparse.Node) []headerOccurrence {
	if node == nil {
		return nil
	}
	out := make([]headerOccurrence, 0, len(node.HeaderFields))
	counts := map[string]int{}
	for _, field := range node.HeaderFields {
		name := field.Name
		counts[strings.ToLower(name)]++
		value := ""
		if validRange(field.RawValue, len(a.doc.Raw)) {
			value = unfoldHeaderValue(string(a.doc.Raw[field.RawValue.Start:field.RawValue.End]))
		}
		out = append(out, headerOccurrence{name: name, value: value, line: field.FirstLine, occurrence: counts[strings.ToLower(name)], field: field})
	}
	return out
}

func finding(id, category, outcome, severity, basis, applicability, label, detail string, evidence []Evidence, reference *Reference) Finding {
	return Finding{ID: id, Category: category, Outcome: outcome, Severity: severity, Basis: basis, Applicability: applicability, Label: label, Detail: detail, Evidence: evidence, Reference: reference}
}
func rfc(label, url string) *Reference   { return &Reference{Label: label, URL: url} }
func stringPointer(value string) *string { return &value }
func pointerValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
func hasCause(values map[string]struct{}, key string) bool { _, ok := values[key]; return ok }
func validRange(r mimeparse.ByteRange, length int) bool {
	return r.Start >= 0 && r.End >= r.Start && r.End <= length
}
func rawBody(raw []byte, node *mimeparse.Node) ([]byte, bool) {
	if node == nil || !validRange(node.RawBody, len(raw)) {
		return nil, false
	}
	return raw[node.RawBody.Start:node.RawBody.End], true
}
func normalizeCause(value string) string {
	lower := strings.ToLower(strings.TrimSpace(value))
	for _, known := range truncationOrder {
		if strings.ToLower(known) == lower {
			return known
		}
	}
	switch lower {
	case "depth", "maxdepth":
		return "MIME depth"
	case "parts", "maxparts":
		return "MIME parts"
	case "headers", "fields":
		return "header fields"
	case "lines":
		return "physical lines"
	}
	return value
}
func evidenceValue(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	return truncateUTF8(value, 200)
}
func truncateUTF8(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	end := limit
	for end > 0 && !utf8.RuneStart(value[end]) {
		end--
	}
	return value[:end]
}
func isASCII(value string) bool { return isASCIIBytes([]byte(value)) }
func isASCIIBytes(value []byte) bool {
	for _, b := range value {
		if b >= 0x80 {
			return false
		}
	}
	return true
}
func unfoldHeaderValue(value string) string {
	value = strings.ReplaceAll(value, "\r\n\t", " ")
	value = strings.ReplaceAll(value, "\r\n ", " ")
	value = strings.ReplaceAll(value, "\n\t", " ")
	value = strings.ReplaceAll(value, "\n ", " ")
	return strings.TrimSpace(value)
}
func fieldsNamed(fields []headerOccurrence, name string) []headerOccurrence {
	out := make([]headerOccurrence, 0)
	for _, field := range fields {
		if strings.EqualFold(field.name, name) {
			out = append(out, field)
		}
	}
	return out
}
func occurrencesEvidence(fields []headerOccurrence, name string, path *string) []Evidence {
	out := make([]Evidence, 0, len(fields))
	for _, item := range fields {
		fieldName := name
		value := evidenceValue(item.value)
		occurrence := item.occurrence
		line := item.line
		out = append(out, Evidence{Source: "raw-header", Path: path, Field: &fieldName, Occurrence: &occurrence, Line: &line, Value: &value})
	}
	return out
}
func stripComments(value string) string {
	out, depth, escaped := strings.Builder{}, 0, false
	for _, r := range value {
		if escaped {
			if depth == 0 {
				out.WriteRune(r)
			}
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if r == '(' {
			depth++
			continue
		}
		if r == ')' && depth > 0 {
			depth--
			continue
		}
		if depth == 0 {
			out.WriteRune(r)
		}
	}
	return out.String()
}
func validMessageID(value string) bool {
	return msgIDRE.MatchString(strings.TrimSpace(stripComments(value)))
}
func validMessageIDList(value string) bool {
	s := strings.TrimSpace(stripComments(value))
	count := 0
	for s != "" {
		start := strings.IndexByte(s, '<')
		if start < 0 || strings.TrimSpace(s[:start]) != "" {
			return false
		}
		end := strings.IndexByte(s[start:], '>')
		if end < 0 {
			return false
		}
		end += start
		token := s[start : end+1]
		if !validMessageID(token) {
			return false
		}
		count++
		s = strings.TrimSpace(s[end+1:])
	}
	return count > 0
}

func parseAddressField(value string, allowEmpty bool) (int, bool, error) {
	value = strings.TrimSpace(value)
	if value == "" && allowEmpty {
		return 0, false, nil
	}
	segments, groups, err := splitAddressGroups(value)
	if err != nil {
		return 0, groups, err
	}
	count := 0
	for _, segment := range segments {
		segment = strings.TrimSpace(segment)
		if segment == "" {
			continue
		}
		addresses, err := mail.ParseAddressList(segment)
		if err != nil {
			return count, groups, err
		}
		count += len(addresses)
	}
	if count == 0 && !groups {
		return 0, groups, errors.New("empty address list")
	}
	return count, groups, nil
}
func splitAddressGroups(value string) ([]string, bool, error) {
	var out []string
	start, groupStart := -1, -1
	quote, escape, angle, comment := false, false, 0, 0
	groups := false
	for i, r := range value {
		if escape {
			escape = false
			continue
		}
		if r == '\\' && (quote || comment > 0) {
			escape = true
			continue
		}
		if comment > 0 {
			if r == '(' {
				comment++
			}
			if r == ')' {
				comment--
			}
			continue
		}
		if r == '(' && !quote {
			comment = 1
			continue
		}
		if r == '"' && angle == 0 {
			quote = !quote
			continue
		}
		if quote {
			continue
		}
		switch r {
		case '<':
			angle++
		case '>':
			angle--
			if angle < 0 {
				return nil, groups, errors.New("unexpected angle")
			}
		case ':':
			if angle == 0 {
				if groupStart >= 0 {
					return nil, groups, errors.New("nested group")
				}
				groups = true
				groupStart = i + 1
				if start < 0 {
					prefix := strings.TrimSpace(value[:i])
					if prefix == "" {
						return nil, groups, errors.New("empty group name")
					}
				}
			}
		case ';':
			if angle == 0 && groupStart >= 0 {
				out = append(out, value[groupStart:i])
				groupStart = -1
				start = i + 1
			}
		case ',':
			if angle == 0 && groupStart < 0 {
				if start < 0 {
					start = 0
				}
				out = append(out, value[start:i])
				start = i + 1
			}
		}
	}
	if quote || comment != 0 || angle != 0 || groupStart >= 0 {
		return nil, groups, errors.New("unterminated address syntax")
	}
	if start < 0 {
		start = 0
	}
	if start < len(value) {
		out = append(out, value[start:])
	}
	return out, groups, nil
}

func invalidBase64(body []byte) bool {
	clean := make([]byte, 0, len(body))
	for _, b := range body {
		if b == '\r' || b == '\n' || b == ' ' || b == '\t' {
			continue
		}
		clean = append(clean, b)
	}
	if !dkimBase64RE.Match(clean) || len(clean)%4 != 0 {
		return true
	}
	_, err := base64.StdEncoding.DecodeString(string(clean))
	return err != nil
}
func invalidQuotedPrintable(body []byte) bool {
	for i := 0; i < len(body); i++ {
		if body[i] != '=' {
			continue
		}
		if i+2 >= len(body) {
			return true
		}
		if body[i+1] == '\r' && body[i+2] == '\n' {
			i += 2
			continue
		}
		if !isHex(body[i+1]) || !isHex(body[i+2]) {
			return true
		}
		i += 2
	}
	_, err := io.ReadAll(quotedprintable.NewReader(bytes.NewReader(body)))
	return err != nil
}
func isHex(b byte) bool { return b >= '0' && b <= '9' || b >= 'A' && b <= 'F' || b >= 'a' && b <= 'f' }
func encodedLineOver(body []byte, max int) bool {
	start := 0
	for start < len(body) {
		end := bytes.IndexByte(body[start:], '\n')
		if end < 0 {
			end = len(body) - start
		}
		line := body[start : start+end]
		line = bytes.TrimSuffix(line, []byte{'\r'})
		if len(line) > max {
			return true
		}
		start += end + 1
	}
	return false
}
func transferDomainContradiction(body []byte, encoding string) string {
	if bytes.IndexByte(body, 0) >= 0 {
		return "The raw body contains NUL octets that contradict the declared transfer domain."
	}
	if encoding == "7bit" && !isASCIIBytes(body) {
		return "The raw body contains high octets that contradict declared 7bit content."
	}
	if bytes.Contains(body, []byte{'\n'}) {
		for i, b := range body {
			if b == '\n' && (i == 0 || body[i-1] != '\r') {
				return "The raw body uses non-CRLF line breaks that contradict the declared transfer domain."
			}
		}
	}
	if encodedLineOver(body, 998) {
		return "The raw body contains a line over 998 octets that contradicts the declared transfer domain."
	}
	return ""
}
func dangerousFilename(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	return strings.ContainsRune(value, 0) || strings.ContainsAny(value, "/\\") || len(value) >= 2 && value[1] == ':' && unicode.IsLetter(rune(value[0])) || lower == "." || lower == ".." || strings.HasSuffix(lower, "/.") || strings.HasSuffix(lower, "/..")
}
func normalizeContentID(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 4 && strings.EqualFold(value[:4], "cid:") {
		value = value[4:]
	}
	return strings.Trim(strings.TrimSpace(value), "<>")
}

func parseAuthenticationResults(value string) (string, bool) {
	parts := splitSemicolon(value)
	if len(parts) < 1 {
		return "", false
	}
	head := strings.Fields(stripComments(parts[0]))
	if len(head) < 1 || !validAuthservID(strings.TrimSuffix(head[0], ".0")) {
		return "", false
	}
	for _, clause := range parts[1:] {
		clause = strings.TrimSpace(clause)
		if clause == "" {
			continue
		}
		fields, ok := splitAuthFields(clause)
		if !ok || len(fields) == 0 {
			return "", false
		}
		pair := strings.SplitN(fields[0], "=", 2)
		if len(pair) != 2 {
			return "", false
		}
		method := strings.SplitN(pair[0], "/", 2)[0]
		if !tokenRE.MatchString(method) || !tokenRE.MatchString(pair[1]) {
			return "", false
		}
		for _, property := range fields[1:] {
			pair = strings.SplitN(property, "=", 2)
			if len(pair) != 2 || strings.TrimSpace(pair[1]) == "" {
				return "", false
			}
			name := strings.SplitN(pair[0], ".", 2)[0]
			if !tokenRE.MatchString(name) {
				return "", false
			}
		}
	}
	return head[0], true
}

func splitAuthFields(value string) ([]string, bool) {
	fields := make([]string, 0, 4)
	start, comment := -1, 0
	quote, escape := false, false
	for index, r := range value {
		if escape {
			escape = false
			continue
		}
		if r == '\\' && (quote || comment > 0) {
			escape = true
			continue
		}
		if !quote {
			if r == '(' {
				comment++
			}
			if r == ')' {
				comment--
				if comment < 0 {
					return nil, false
				}
			}
		}
		if comment == 0 && r == '"' {
			quote = !quote
		}
		if comment == 0 && !quote && unicode.IsSpace(r) {
			if start >= 0 {
				fields = append(fields, value[start:index])
				start = -1
			}
			continue
		}
		if start < 0 && comment == 0 {
			start = index
		}
	}
	if quote || comment != 0 || escape {
		return nil, false
	}
	if start >= 0 {
		fields = append(fields, value[start:])
	}
	return fields, true
}
func validAuthservID(value string) bool { return value != "" && !strings.ContainsAny(value, " \t;()") }
func splitSemicolon(value string) []string {
	var out []string
	start, comment := 0, 0
	quote, escape := false, false
	for i, r := range value {
		if escape {
			escape = false
			continue
		}
		if r == '\\' {
			escape = true
			continue
		}
		if !quote {
			if r == '(' {
				comment++
			}
			if r == ')' && comment > 0 {
				comment--
			}
		}
		if comment == 0 && r == '"' {
			quote = !quote
		}
		if comment == 0 && !quote && r == ';' {
			out = append(out, value[start:i])
			start = i + 1
		}
	}
	out = append(out, value[start:])
	return out
}
func parseTagList(value string) (map[string]string, error) {
	tags := map[string]string{}
	for _, part := range splitSemicolon(value) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		pair := strings.SplitN(part, "=", 2)
		if len(pair) != 2 {
			return nil, errors.New("tag lacks equals")
		}
		name := strings.ToLower(strings.TrimSpace(pair[0]))
		val := strings.TrimSpace(pair[1])
		if !tokenRE.MatchString(name) {
			return nil, errors.New("invalid tag name")
		}
		if _, ok := tags[name]; ok {
			return nil, errors.New("duplicate tag " + name)
		}
		tags[name] = val
	}
	return tags, nil
}
func validateDKIM(tags map[string]string, arc bool) error {
	required := []string{"a", "b", "bh", "d", "h", "s"}
	if !arc {
		required = append(required, "v")
	}
	if arc {
		required = append(required, "i")
	}
	for _, name := range required {
		if strings.TrimSpace(tags[name]) == "" {
			return errors.New("missing " + name)
		}
	}
	if !arc && tags["v"] != "1" {
		return errors.New("v must be 1")
	}
	if !validDKIMBase64(tags["b"]) || !validDKIMBase64(tags["bh"]) {
		return errors.New("invalid base64 tag")
	}
	if tags["h"] == "" || !containsFold(strings.Split(tags["h"], ":"), "from") {
		return errors.New("h omits From")
	}
	if err := validateTimestamp(tags); err != nil {
		return err
	}
	return nil
}
func validDKIMBase64(value string) bool {
	value = strings.Join(strings.Fields(value), "")
	return value != "" && dkimBase64RE.MatchString(value)
}
func validateTimestamp(tags map[string]string) error {
	var t, x int64
	var err error
	if tags["t"] != "" {
		t, err = strconv.ParseInt(tags["t"], 10, 64)
		if err != nil {
			return errors.New("invalid t")
		}
	}
	if tags["x"] != "" {
		x, err = strconv.ParseInt(tags["x"], 10, 64)
		if err != nil {
			return errors.New("invalid x")
		}
		if tags["t"] != "" && x <= t {
			return errors.New("x must exceed t")
		}
	}
	return nil
}
func validateARCSeal(tags map[string]string) error {
	allowed := map[string]bool{"i": true, "a": true, "b": true, "d": true, "s": true, "cv": true, "t": true}
	for name := range tags {
		if !allowed[name] {
			return errors.New("invalid ARC-Seal tag " + name)
		}
	}
	for _, name := range []string{"i", "a", "b", "d", "s", "cv"} {
		if tags[name] == "" {
			return errors.New("missing " + name)
		}
	}
	if !validDKIMBase64(tags["b"]) {
		return errors.New("invalid base64")
	}
	cv := strings.ToLower(tags["cv"])
	if cv != "none" && cv != "pass" && cv != "fail" {
		return errors.New("invalid cv")
	}
	return validateTimestamp(tags)
}
func parseARCInstanceFromAAR(value string) (int, bool) {
	parts := strings.SplitN(strings.TrimSpace(value), ";", 2)
	if len(parts) != 2 {
		return 0, false
	}
	head := strings.Fields(parts[0])
	if len(head) < 2 {
		return 0, false
	}
	instance := 0
	authserv := ""
	for _, part := range head {
		if strings.HasPrefix(strings.ToLower(part), "i=") {
			instance, _ = strconv.Atoi(strings.TrimPrefix(strings.ToLower(part), "i="))
		} else {
			authserv = part
		}
	}
	_, ok := parseAuthenticationResults(authserv + ";" + parts[1])
	return instance, ok && instance >= 1 && instance <= 50
}
func containsFold(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), target) {
			return true
		}
	}
	return false
}
func parseListUnsubscribe(fields []headerOccurrence) ([]*url.URL, bool) {
	if len(fields) != 1 {
		return nil, false
	}
	value := strings.TrimSpace(fields[0].value)
	if value == "" {
		return nil, false
	}
	parts, ok := splitAngleList(value)
	if !ok {
		return nil, false
	}
	ures := make([]*url.URL, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if len(part) < 3 || part[0] != '<' || part[len(part)-1] != '>' {
			return nil, false
		}
		inside := part[1 : len(part)-1]
		u, err := url.Parse(inside)
		if err != nil || u.Scheme == "" || strings.ContainsAny(inside, "<>\r\n") {
			return nil, false
		}
		scheme := strings.ToLower(u.Scheme)
		if scheme == "http" || scheme == "https" {
			if u.Host == "" || !strings.HasPrefix(strings.ToLower(inside), scheme+"://") {
				return nil, false
			}
		} else if scheme == "mailto" && u.Opaque == "" && u.Path == "" {
			return nil, false
		}
		ures = append(ures, u)
	}
	return ures, true
}
func splitAngleList(value string) ([]string, bool) {
	parts := make([]string, 0, 2)
	start, depth := 0, 0
	for index, r := range value {
		switch r {
		case '<':
			depth++
			if depth > 1 {
				return nil, false
			}
		case '>':
			depth--
			if depth < 0 {
				return nil, false
			}
		case ',':
			if depth == 0 {
				parts = append(parts, value[start:index])
				start = index + 1
			}
		}
	}
	if depth != 0 {
		return nil, false
	}
	parts = append(parts, value[start:])
	return parts, true
}

func buildMIMETree(root *mimeparse.Node, raw []byte) *MimeNode {
	if root == nil {
		return nil
	}
	var convert func(*mimeparse.Node) MimeNode
	convert = func(node *mimeparse.Node) MimeNode {
		item := MimeNode{Path: node.Path, ContentType: node.MediaType, Children: make([]MimeNode, 0, len(node.Children))}
		if value := strings.TrimSpace(node.MediaParams["charset"]); value != "" {
			item.Charset = &value
		}
		if value := strings.TrimSpace(node.TransferEncoding); value != "" {
			value = strings.ToLower(value)
			item.Encoding = &value
		}
		if value := strings.TrimSpace(node.Disposition); value != "" {
			value = strings.ToLower(value)
			item.Disposition = &value
		}
		filename := node.DispositionParams["filename"]
		if filename == "" {
			filename = node.MediaParams["name"]
		}
		if filename != "" {
			item.Filename = &filename
		}
		if value := normalizeContentID(node.Header.Get("Content-ID")); value != "" {
			item.ContentID = &value
		}
		if body, ok := rawBody(raw, node); ok {
			size := len(body)
			item.RawSize = &size
		}
		if len(node.Children) == 0 && node.DecodeError == nil {
			size := len(node.DecodedBody)
			item.DecodedSize = &size
		}
		for _, child := range node.Children {
			item.Children = append(item.Children, convert(child))
		}
		return item
	}
	item := convert(root)
	return &item
}
func summarize(findings []Finding) Summary {
	var s Summary
	for _, f := range findings {
		switch {
		case f.Outcome == "fail":
			s.Fail++
		case f.Outcome == "pass" && f.Severity == "warning":
			s.Warning++
		case f.Outcome == "pass" && f.Severity == "advisory":
			s.Advisory++
		case f.Outcome == "observed":
			s.Observed++
		case f.Outcome == "not-evaluated":
			s.NotEvaluated++
		case f.Outcome == "pass" && f.Severity == "none":
			s.Pass++
		}
	}
	return s
}
func findingLess(a, b Finding) bool {
	if categoryOrder[a.Category] != categoryOrder[b.Category] {
		return categoryOrder[a.Category] < categoryOrder[b.Category]
	}
	rank := func(f Finding) int {
		switch {
		case f.Outcome == "fail" && f.Severity == "error":
			return 0
		case f.Outcome == "pass" && f.Severity == "warning":
			return 1
		case f.Outcome == "pass" && f.Severity == "advisory":
			return 2
		case f.Outcome == "not-evaluated":
			return 3
		case f.Outcome == "observed":
			return 4
		default:
			return 5
		}
	}
	ra, rb := rank(a), rank(b)
	if ra != rb {
		return ra < rb
	}
	return a.ID < b.ID
}
func trimReport(report *Report) {
	measure := func() int { encoded, _ := json.Marshal(report); return len(encoded) }
	if measure() <= MaxReportBytes {
		return
	}
	report.Resources = []Resource{}
	if measure() <= MaxReportBytes {
		report.Summary = summarize(report.Findings)
		return
	}
	for index := range report.Findings {
		if len(report.Findings[index].Evidence) > 0 {
			report.Findings[index].Evidence = []Evidence{}
			report.Findings[index].EvidenceTruncated = true
		}
	}
	for measure() > MaxReportBytes && len(report.Findings) > 1 {
		remove := len(report.Findings) - 1
		if report.Findings[remove].ID == "analysis.truncated" {
			remove--
		}
		if remove < 0 || report.Findings[remove].ID == "analysis.truncated" {
			break
		}
		report.Findings = append(report.Findings[:remove], report.Findings[remove+1:]...)
	}
	report.Summary = summarize(report.Findings)
}
