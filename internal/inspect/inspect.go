package inspect

import (
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"golang.org/x/net/html"
)

var (
	emailHTMLPolicy          = newEmailHTMLPolicy()
	legacyColorPattern       = regexp.MustCompile(`(?i)^(?:#[0-9a-f]{3,8}|[a-z]{1,32})$`)
	legacyDimensionPattern   = regexp.MustCompile(`(?i)^(?:0|[1-9][0-9]{0,4})(?:px|%)?$`)
	legacySpacingPattern     = regexp.MustCompile(`^(?:0|[1-9][0-9]{0,3})$`)
	fontFacePattern          = regexp.MustCompile(`(?i)^[a-z0-9][a-z0-9 ,.'"_-]{0,159}$`)
	cssColorPattern          = regexp.MustCompile(`(?i)^(?:transparent|currentcolor|#[0-9a-f]{3,8}|[a-z]{1,32}|rgba?\([0-9.,% +\-]+\)|hsla?\([0-9.,% +\-]+\))$`)
	cssFontFamilyPattern     = regexp.MustCompile(`(?i)^[a-z0-9][a-z0-9 ,.'"_-]{0,159}$`)
	cssLengthPattern         = regexp.MustCompile(`(?i)^(?:auto|0|(?:[+-]?(?:[0-9]+(?:\.[0-9]+)?|\.[0-9]+))(?:px|pt|pc|in|cm|mm|em|rem|ex|ch|%))$`)
	cssUnsignedLengthPattern = regexp.MustCompile(`(?i)^(?:auto|0|(?:(?:[0-9]+(?:\.[0-9]+)?|\.[0-9]+))(?:px|pt|pc|in|cm|mm|em|rem|ex|ch|%))$`)
)

func SanitizeEmailHTML(rawHTML string, cidMaps ...map[string]int64) string {
	var cidMap map[string]int64
	if len(cidMaps) != 0 {
		cidMap = cidMaps[0]
	}
	rewritten, err := rewriteEmailURLs(rawHTML, cidMap, true)
	if err != nil {
		return ""
	}
	return emailHTMLPolicy.Sanitize(rewritten)
}

// RewriteCIDURLs is retained for callers that only need parsed CID replacement.
// Sanitized rendering should use SanitizeEmailHTML so rewriting precedes the
// final parser allowlist.
func RewriteCIDURLs(rawHTML string, cidMap map[string]int64) string {
	rewritten, err := rewriteEmailURLs(rawHTML, cidMap, false)
	if err != nil {
		return rawHTML
	}
	return rewritten
}

func newEmailHTMLPolicy() *bluemonday.Policy {
	policy := bluemonday.NewPolicy()
	policy.AllowElements(
		"html", "head", "body",
		"address", "article", "aside", "footer", "header", "main", "nav", "section",
		"div", "span", "p", "br", "hr", "wbr",
		"h1", "h2", "h3", "h4", "h5", "h6",
		"blockquote", "pre", "code", "kbd", "samp", "var",
		"b", "strong", "i", "em", "u", "s", "strike", "small", "big", "sub", "sup", "mark",
		"abbr", "bdi", "bdo", "cite", "dfn", "q", "ins", "del", "time",
		"ul", "ol", "li", "dl", "dt", "dd",
		"table", "caption", "colgroup", "col", "thead", "tbody", "tfoot", "tr", "th", "td",
		"figure", "figcaption", "ruby", "rp", "rt",
		"center", "font", "a", "img",
	)
	policy.AllowNoAttrs().OnElements("a")
	policy.AllowAttrs("title", "lang", "dir").Globally()
	policy.AllowAttrs("href").OnElements("a")
	policy.AllowAttrs("target").Matching(regexp.MustCompile(`^_blank$`)).OnElements("a")
	policy.AllowAttrs("rel").Matching(regexp.MustCompile(`^(?:noopener noreferrer|noreferrer noopener)$`)).OnElements("a")
	policy.AllowAttrs("src", "alt", "title").OnElements("img")
	policy.AllowAttrs("width", "height").Matching(legacyDimensionPattern).OnElements("img", "table", "td", "th", "col", "colgroup")
	policy.AllowAttrs("align").Matching(regexp.MustCompile(`(?i)^(?:left|center|right|justify|char)$`)).OnElements("p", "div", "table", "caption", "tr", "td", "th", "img", "h1", "h2", "h3", "h4", "h5", "h6")
	policy.AllowAttrs("valign").Matching(regexp.MustCompile(`(?i)^(?:top|middle|bottom|baseline)$`)).OnElements("tr", "td", "th", "img")
	policy.AllowAttrs("bgcolor").Matching(legacyColorPattern).OnElements("body", "table", "tr", "td", "th")
	policy.AllowAttrs("color").Matching(legacyColorPattern).OnElements("font", "hr")
	policy.AllowAttrs("face").Matching(fontFacePattern).OnElements("font")
	policy.AllowAttrs("size").Matching(regexp.MustCompile(`^(?:[1-7]|[+-][1-7])$`)).OnElements("font")
	policy.AllowAttrs("border", "cellpadding", "cellspacing").Matching(legacySpacingPattern).OnElements("table")
	policy.AllowAttrs("colspan", "rowspan", "span").Matching(regexp.MustCompile(`^(?:[1-9][0-9]{0,2})$`)).OnElements("td", "th", "col", "colgroup")
	policy.AllowAttrs("scope").Matching(regexp.MustCompile(`(?i)^(?:row|col|rowgroup|colgroup)$`)).OnElements("th")
	policy.AllowAttrs("role").Matching(regexp.MustCompile(`(?i)^presentation$`)).OnElements("table")
	policy.AllowAttrs("start", "reversed", "type").OnElements("ol", "ul")
	policy.AllowAttrs("compact", "nowrap").OnElements("table", "tr", "td", "th", "ol", "ul", "dl")
	policy.AllowAttrs("hspace", "vspace", "border").Matching(legacySpacingPattern).OnElements("img")
	policy.RequireParseableURLs(true)
	policy.AllowURLSchemes("http", "https", "mailto")
	policy.AllowURLSchemeWithCustomPolicy("attachment", func(parsed *url.URL) bool {
		return parsed.Opaque != "" && positiveDecimal(parsed.Opaque)
	})
	policy.RewriteSrc(func(parsed *url.URL) {
		if parsed.Scheme == "attachment" && positiveDecimal(parsed.Opaque) {
			parsed.Scheme = ""
			parsed.Path = "/api/attachments/" + parsed.Opaque
			parsed.Opaque = ""
			parsed.RawQuery = ""
			parsed.Fragment = ""
		}
	})
	configureEmailStyles(policy)
	return policy
}

func configureEmailStyles(policy *bluemonday.Policy) {
	properties := []string{
		"color", "background-color",
		"font-family", "font-size", "font-style", "font-weight", "line-height",
		"text-align", "text-decoration", "text-decoration-color", "text-decoration-style", "text-transform",
		"letter-spacing", "word-spacing", "white-space", "vertical-align", "direction",
		"display", "float", "clear", "position", "box-sizing",
		"width", "min-width", "max-width", "height", "min-height", "max-height",
		"margin", "margin-top", "margin-right", "margin-bottom", "margin-left",
		"padding", "padding-top", "padding-right", "padding-bottom", "padding-left",
		"border", "border-top", "border-right", "border-bottom", "border-left",
		"border-width", "border-top-width", "border-right-width", "border-bottom-width", "border-left-width",
		"border-style", "border-top-style", "border-right-style", "border-bottom-style", "border-left-style",
		"border-color", "border-top-color", "border-right-color", "border-bottom-color", "border-left-color",
		"border-radius", "border-collapse", "border-spacing", "table-layout",
		"list-style-type", "overflow-wrap", "word-break",
	}
	for _, property := range properties {
		propertyName := property
		policy.AllowStyles(propertyName).MatchingHandler(func(value string) bool {
			return safeEmailCSSValue(propertyName, value)
		}).Globally()
	}
}

func safeEmailCSSValue(property string, value string) bool {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" || len(value) > 256 || strings.ContainsAny(value, `\\/@;{}<>`) {
		return false
	}
	compact := strings.NewReplacer(" ", "", "\t", "", "\r", "", "\n", "").Replace(value)
	for _, primitive := range []string{"url(", "image-set(", "expression(", "behavior", "binding", "import", "font-face", "src(", "var(", "javascript:", "data:"} {
		if strings.Contains(compact, primitive) {
			return false
		}
	}
	switch property {
	case "color", "background-color", "text-decoration-color", "border-color", "border-top-color", "border-right-color", "border-bottom-color", "border-left-color":
		return cssColorPattern.MatchString(value)
	case "font-family":
		return cssFontFamilyPattern.MatchString(value)
	case "font-size":
		return cssUnsignedLengthPattern.MatchString(value) || containsString([]string{"xx-small", "x-small", "small", "medium", "large", "x-large", "xx-large", "smaller", "larger"}, value)
	case "font-style":
		return containsString([]string{"normal", "italic", "oblique"}, value)
	case "font-weight":
		return containsString([]string{"normal", "bold", "bolder", "lighter", "100", "200", "300", "400", "500", "600", "700", "800", "900"}, value)
	case "line-height":
		if value == "normal" || cssUnsignedLengthPattern.MatchString(value) {
			return true
		}
		_, err := strconv.ParseFloat(value, 64)
		return err == nil && !strings.HasPrefix(value, "-")
	case "text-align":
		return containsString([]string{"left", "right", "center", "justify", "start", "end"}, value)
	case "text-decoration":
		return keywordsOnly(value, "none", "underline", "overline", "line-through")
	case "text-decoration-style":
		return containsString([]string{"solid", "double", "dotted", "dashed", "wavy"}, value)
	case "text-transform":
		return containsString([]string{"none", "capitalize", "uppercase", "lowercase"}, value)
	case "letter-spacing", "word-spacing":
		return value == "normal" || cssLengthPattern.MatchString(value)
	case "white-space":
		return containsString([]string{"normal", "nowrap", "pre", "pre-wrap", "pre-line", "break-spaces"}, value)
	case "vertical-align":
		return cssLengthPattern.MatchString(value) || containsString([]string{"baseline", "sub", "super", "text-top", "text-bottom", "middle", "top", "bottom"}, value)
	case "direction":
		return value == "ltr" || value == "rtl"
	case "display":
		return containsString([]string{"none", "block", "inline", "inline-block", "table", "inline-table", "table-row", "table-cell", "table-header-group", "table-footer-group", "table-row-group", "list-item"}, value)
	case "float", "clear":
		return containsString([]string{"none", "left", "right", "both"}, value)
	case "position":
		return value == "static" || value == "relative"
	case "box-sizing":
		return value == "content-box" || value == "border-box"
	case "width", "min-width", "height", "min-height":
		return cssUnsignedLengthPattern.MatchString(value)
	case "max-width", "max-height":
		return value == "none" || cssUnsignedLengthPattern.MatchString(value)
	case "margin", "margin-top", "margin-right", "margin-bottom", "margin-left":
		return lengthList(value, false, 4)
	case "padding", "padding-top", "padding-right", "padding-bottom", "padding-left", "border-radius", "border-spacing":
		return lengthList(value, true, 4)
	case "border", "border-top", "border-right", "border-bottom", "border-left":
		return safeBorder(value)
	case "border-width", "border-top-width", "border-right-width", "border-bottom-width", "border-left-width":
		return borderWidthList(value)
	case "border-style", "border-top-style", "border-right-style", "border-bottom-style", "border-left-style":
		return keywordsOnly(value, "none", "hidden", "dotted", "dashed", "solid", "double", "groove", "ridge", "inset", "outset")
	case "border-collapse":
		return value == "collapse" || value == "separate"
	case "table-layout":
		return value == "auto" || value == "fixed"
	case "list-style-type":
		return containsString([]string{"none", "disc", "circle", "square", "decimal", "decimal-leading-zero", "lower-roman", "upper-roman", "lower-alpha", "upper-alpha"}, value)
	case "overflow-wrap":
		return containsString([]string{"normal", "break-word", "anywhere"}, value)
	case "word-break":
		return containsString([]string{"normal", "break-all", "keep-all", "break-word"}, value)
	default:
		return false
	}
}

type droppedActiveContainer struct {
	name      string
	openDepth int
}

type activeContentDropState struct {
	containers []droppedActiveContainer
	open       []string
	malformed  bool
}

func rewriteEmailURLs(rawHTML string, cidMap map[string]int64, enforceSafety bool) (string, error) {
	tokenizer := html.NewTokenizer(strings.NewReader(rawHTML))
	var output strings.Builder
	output.Grow(len(rawHTML))
	var emittedOpen []string
	var dropped activeContentDropState
	for {
		tokenType := tokenizer.Next()
		if tokenType == html.ErrorToken {
			if tokenizer.Err() == io.EOF {
				return output.String(), nil
			}
			return "", tokenizer.Err()
		}
		token := tokenizer.Token()
		name := strings.ToLower(token.Data)

		if enforceSafety && dropped.consume(tokenType, name, emittedOpen) {
			if tokenType == html.StartTagToken && isRawTextElement(name) {
				tokenizer.NextIsNotRawText()
			}
			continue
		}

		if tokenType == html.StartTagToken || tokenType == html.SelfClosingTagToken {
			switch name {
			case "a":
				if enforceSafety {
					rewriteAnchorAttrs(&token)
				}
			case "img":
				rewriteImageAttrs(&token, cidMap, enforceSafety)
			}
		}
		output.WriteString(token.String())
		if enforceSafety {
			emittedOpen = updateOpenElements(emittedOpen, tokenType, name)
		}
	}
}

func (state *activeContentDropState) consume(tokenType html.TokenType, name string, emittedOpen []string) bool {
	if len(state.containers) == 0 {
		if tokenType != html.StartTagToken && tokenType != html.SelfClosingTagToken || !isActiveContentElement(name) {
			return false
		}
		state.push(name)
		return true
	}

	switch tokenType {
	case html.StartTagToken, html.SelfClosingTagToken:
		if isActiveContentElement(name) {
			state.malformed = true
			if tokenType == html.StartTagToken && !isVoidElement(name) {
				state.push(name)
			}
			return true
		}
		if state.malformed && len(state.open) == 0 && isActiveRecoveryElement(name) {
			state.reset()
			return false
		}
		if tokenType == html.StartTagToken && !isVoidElement(name) {
			state.open = append(state.open, name)
		}
		return true
	case html.EndTagToken:
		if index := lastElementIndex(state.open, name); index >= 0 {
			state.open = state.open[:index]
			return true
		}
		if index := state.containerIndex(name); index >= 0 {
			openDepth := state.containers[index].openDepth
			state.containers = state.containers[:index]
			state.open = state.open[:openDepth]
			if len(state.containers) == 0 {
				state.reset()
			}
			return true
		}
		if lastElementIndex(emittedOpen, name) >= 0 {
			state.reset()
			return false
		}
		return true
	default:
		return true
	}
}

func (state *activeContentDropState) push(name string) {
	state.containers = append(state.containers, droppedActiveContainer{name: name, openDepth: len(state.open)})
}

func (state *activeContentDropState) containerIndex(name string) int {
	for index := len(state.containers) - 1; index >= 0; index-- {
		if state.containers[index].name == name {
			return index
		}
	}
	return -1
}

func (state *activeContentDropState) reset() {
	state.containers = state.containers[:0]
	state.open = state.open[:0]
	state.malformed = false
}

func updateOpenElements(open []string, tokenType html.TokenType, name string) []string {
	switch tokenType {
	case html.StartTagToken:
		if !isVoidElement(name) {
			return append(open, name)
		}
	case html.EndTagToken:
		if index := lastElementIndex(open, name); index >= 0 {
			return open[:index]
		}
	}
	return open
}

func lastElementIndex(elements []string, name string) int {
	for index := len(elements) - 1; index >= 0; index-- {
		if elements[index] == name {
			return index
		}
	}
	return -1
}

func isActiveContentElement(name string) bool {
	switch name {
	case "applet", "embed", "form", "iframe", "math", "noembed", "noframes", "object", "plaintext", "script", "style", "svg", "template", "textarea", "title", "xmp":
		return true
	default:
		return false
	}
}

func isRawTextElement(name string) bool {
	switch name {
	case "iframe", "noembed", "noframes", "plaintext", "script", "style", "textarea", "title", "xmp":
		return true
	default:
		return false
	}
}

func isActiveRecoveryElement(name string) bool {
	switch name {
	case "address", "article", "aside", "blockquote", "center", "div", "dl", "footer", "h1", "h2", "h3", "h4", "h5", "h6", "header", "hr", "main", "nav", "ol", "p", "pre", "section", "table", "ul":
		return true
	default:
		return false
	}
}

func isVoidElement(name string) bool {
	switch name {
	case "area", "base", "br", "col", "embed", "hr", "img", "input", "link", "meta", "param", "source", "track", "wbr":
		return true
	default:
		return false
	}
}

func rewriteAnchorAttrs(token *html.Token) {
	attrs := make([]html.Attribute, 0, len(token.Attr)+2)
	href := ""
	for _, attr := range token.Attr {
		switch strings.ToLower(attr.Key) {
		case "href":
			if href == "" {
				href = attr.Val
			}
		case "target", "rel":
		default:
			attrs = append(attrs, attr)
		}
	}
	if safeHref, ok := safeAnchorURL(href); ok {
		attrs = append(attrs,
			html.Attribute{Key: "href", Val: safeHref},
			html.Attribute{Key: "target", Val: "_blank"},
			html.Attribute{Key: "rel", Val: "noopener noreferrer"},
		)
	}
	token.Attr = attrs
}

func rewriteImageAttrs(token *html.Token, cidMap map[string]int64, enforceSafety bool) {
	attrs := make([]html.Attribute, 0, len(token.Attr))
	source := ""
	for _, attr := range token.Attr {
		switch strings.ToLower(attr.Key) {
		case "src":
			if source == "" {
				source = strings.TrimSpace(attr.Val)
			}
		case "srcset":
			if !enforceSafety {
				attrs = append(attrs, attr)
			}
		default:
			attrs = append(attrs, attr)
		}
	}
	if attachmentID, ok := cidAttachmentID(source, cidMap); ok {
		value := "/api/attachments/" + strconv.FormatInt(attachmentID, 10)
		if enforceSafety {
			value = "attachment:" + strconv.FormatInt(attachmentID, 10)
		}
		attrs = append(attrs, html.Attribute{Key: "src", Val: value})
	} else if !enforceSafety && source != "" {
		attrs = append(attrs, html.Attribute{Key: "src", Val: source})
	}
	token.Attr = attrs
}

func cidAttachmentID(source string, cidMap map[string]int64) (int64, bool) {
	if len(source) < 4 || !strings.EqualFold(source[:4], "cid:") {
		return 0, false
	}
	decoded, err := url.PathUnescape(source[4:])
	if err != nil {
		return 0, false
	}
	for _, candidate := range []string{normalizeCID(decoded), decoded, normalizeCID(source[4:]), source[4:]} {
		if attachmentID, ok := cidMap[candidate]; ok && attachmentID > 0 {
			return attachmentID, true
		}
	}
	return 0, false
}

func normalizeCID(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 && value[0] == '<' && value[len(value)-1] == '>' {
		return value[1 : len(value)-1]
	}
	return value
}

func safeAnchorURL(rawURL string) (string, bool) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" || strings.ContainsAny(rawURL, "\x00\r\n\t") {
		return "", false
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", false
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
		if parsed.Host == "" {
			return "", false
		}
	case "mailto":
		if parsed.Opaque == "" && parsed.Path == "" {
			return "", false
		}
	default:
		return "", false
	}
	return parsed.String(), true
}

func positiveDecimal(value string) bool {
	number, err := strconv.ParseInt(value, 10, 64)
	return err == nil && number > 0
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func keywordsOnly(value string, allowed ...string) bool {
	parts := strings.Fields(value)
	if len(parts) == 0 || len(parts) > 4 {
		return false
	}
	for _, part := range parts {
		if !containsString(allowed, part) {
			return false
		}
	}
	return true
}

func lengthList(value string, unsigned bool, maximum int) bool {
	parts := strings.Fields(value)
	if len(parts) == 0 || len(parts) > maximum {
		return false
	}
	pattern := cssLengthPattern
	if unsigned {
		pattern = cssUnsignedLengthPattern
	}
	for _, part := range parts {
		if !pattern.MatchString(part) {
			return false
		}
	}
	return true
}

func borderWidthList(value string) bool {
	parts := strings.Fields(value)
	if len(parts) == 0 || len(parts) > 4 {
		return false
	}
	for _, part := range parts {
		if !cssUnsignedLengthPattern.MatchString(part) && !containsString([]string{"thin", "medium", "thick"}, part) {
			return false
		}
	}
	return true
}

func safeBorder(value string) bool {
	parts := strings.Fields(value)
	if len(parts) == 0 || len(parts) > 3 {
		return false
	}
	seenWidth, seenStyle, seenColor := false, false, false
	for _, part := range parts {
		switch {
		case !seenWidth && (cssUnsignedLengthPattern.MatchString(part) || containsString([]string{"thin", "medium", "thick"}, part)):
			seenWidth = true
		case !seenStyle && containsString([]string{"none", "hidden", "dotted", "dashed", "solid", "double", "groove", "ridge", "inset", "outset"}, part):
			seenStyle = true
		case !seenColor && cssColorPattern.MatchString(part):
			seenColor = true
		default:
			return false
		}
	}
	return true
}
