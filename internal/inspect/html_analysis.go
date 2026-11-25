package inspect

import (
	"bytes"
	"context"
	"io"
	"net/url"
	"strconv"
	"strings"

	"github.com/tdewolff/parse/v2/css"
	"golang.org/x/net/html"
	"golang.org/x/text/language"
)

type HTMLFacts struct {
	explicitHTMLRoot      bool
	lang                  string
	langInvalid           bool
	imageCount            int
	missingAlt            int
	anchorCount           int
	unnamedLinks          int
	externalImages        int
	trackingPixels        int
	insecure              int
	truncated             bool
	resources             []htmlResourceOccurrence
	missingAltEvidence    []Evidence
	unnamedEvidence       []Evidence
	externalEvidence      []Evidence
	trackingEvidence      []Evidence
	insecureEvidence      []Evidence
	resourcesCapped       bool
	resourceValuesDropped bool
}

type htmlResourceOccurrence struct {
	kind  string
	path  *string
	url   string
	text  string
	count int
}

type htmlAnchor struct {
	resource   int
	closed     bool
	exposed    bool
	ariaLabel  string
	labelledBy []string
	title      string
	text       strings.Builder
}

type htmlLabel struct {
	override string
	title    string
	hidden   bool
	text     strings.Builder
}

func (l *htmlLabel) name() string {
	if l.override != "" {
		return l.override
	}
	if l.text.Len() != 0 {
		return l.text.String()
	}
	return l.title
}

type htmlVisibility struct {
	suppressed  bool // display:none, hidden attribute, or a non-rendered subtree
	ariaHidden  bool
	invisible   bool // inherited CSS visibility; a child can override it
	transparent bool // opacity affects pixels, but not accessible names
}

func (v htmlVisibility) inaccessible() bool   { return v.suppressed || v.invisible || v.ariaHidden }
func (v htmlVisibility) visuallyHidden() bool { return v.suppressed || v.invisible || v.transparent }

type htmlElement struct {
	muteAnchorText bool
	name           string
	label          *htmlLabel
	anchor         *htmlAnchor
	visibility     htmlVisibility
}

func analyzeHTML(ctx context.Context, raw []byte, path *string) (HTMLFacts, []string, error) {
	return analyzeHTMLSource(ctx, raw, path, false)
}

func analyzeHTMLSource(ctx context.Context, raw []byte, path *string, clipped bool) (HTMLFacts, []string, error) {
	facts := HTMLFacts{resources: make([]htmlResourceOccurrence, 0), truncated: clipped}
	causes := make([]string, 0, 2)
	tokenizer := html.NewTokenizer(bytes.NewReader(raw))
	tokenizer.SetMaxBuf(MaxHTMLTokenBytes)
	stack := make([]htmlElement, 0, 32)
	anchors := make([]*htmlAnchor, 0)
	labels := make(map[string]*htmlLabel)
	nodes := 0
	templateDepth := 0
	openTags := make(map[string][]int)
	var visibleIDs, hiddenIDs []int
	styles := make(map[string]map[string]string)
	anchorHistory := make([]int, 0, 8)
	resourceIndex := make(map[string]int)

	// Builders are owned by pointers, never copied when the element stack grows.
	appendLabelText := func(ids []int, text string) []int {
		alive := ids[:0]
		for _, position := range ids {
			label := stack[position].label
			appendBoundedText(&label.text, text, 512)
			if label.text.Len() < 512 {
				alive = append(alive, position)
			}
		}
		return alive
	}
	appendText := func(text string, hidden bool) {
		if len(hiddenIDs) == 0 && (hidden || len(visibleIDs) == 0 && len(anchorHistory) == 0) {
			return
		}
		text = normalizeHTMLText(text)
		if text == "" {
			return
		}
		// Hidden descendants contribute only if the referenced label is itself
		// hidden (Accessible Name Computation, Hidden Not Referenced).
		hiddenIDs = appendLabelText(hiddenIDs, text)
		if !hidden {
			visibleIDs = appendLabelText(visibleIDs, text)
			if len(anchorHistory) != 0 && !stack[len(stack)-1].muteAnchorText {
				a := stack[anchorHistory[len(anchorHistory)-1]].anchor
				appendBoundedText(&a.text, text, 512)
			}
		}
	}
	trimPositions := func(ids []int, cut int) []int {
		for len(ids) != 0 && ids[len(ids)-1] >= cut {
			ids = ids[:len(ids)-1]
		}
		return ids
	}

tokenLoop:
	for {
		typeOfToken := tokenizer.Next()
		if typeOfToken == html.ErrorToken {
			if err := tokenizer.Err(); err != nil && err != io.EOF {
				facts.truncated = true
				causes = append(causes, "HTML token bytes")
			}
			break
		}
		token := tokenizer.Token()
		switch typeOfToken {
		case html.StartTagToken, html.SelfClosingTagToken:
			nodes++
			if nodes > MaxHTMLNodes {
				facts.truncated = true
				causes = append(causes, "HTML nodes")
				break tokenLoop
			}
			if nodes%1024 == 0 {
				if err := ctx.Err(); err != nil {
					return facts, causes, err
				}
			}
			name := token.Data
			// Template contents live in a separate inert document fragment.
			if name == "template" {
				templateDepth++
				continue
			}
			if templateDepth != 0 {
				continue
			}
			attrs := htmlAttrs(token.Attr)
			var visibility htmlVisibility
			if len(stack) > 0 {
				visibility = stack[len(stack)-1].visibility
			}
			var style map[string]string
			if rawStyle := attrs["style"]; rawStyle != "" {
				var ok bool
				if style, ok = styles[rawStyle]; !ok {
					style = inspectionStyle(rawStyle)
					// Newsletter templates repeat inline declarations. Bound retained
					// entries and key lengths independently of sender input.
					if len(styles) < 128 && len(rawStyle) <= 1024 {
						styles[rawStyle] = style
					}
				}
			}
			_, hiddenAttr := attrs["hidden"]
			// The hidden attribute is normally implemented by the UA stylesheet.
			// An explicit inline display can make its contents visible again.
			switch style["display"] {
			case "block", "inline", "inline-block", "flex", "inline-flex", "grid", "inline-grid", "table", "table-row", "table-cell", "contents":
				hiddenAttr = false
			}
			visibility.suppressed = visibility.suppressed || hiddenAttr || style["display"] == "none" || name == "script" || name == "style"
			visibility.ariaHidden = visibility.ariaHidden || strings.EqualFold(strings.TrimSpace(attrs["aria-hidden"]), "true")
			switch style["visibility"] {
			case "collapse", "hidden":
				visibility.invisible = true
			case "visible":
				visibility.invisible = false
			}
			if opacity := style["opacity"]; opacity != "" {
				if n, err := strconv.ParseFloat(strings.TrimSuffix(opacity, "%"), 64); err == nil && n <= 0 {
					visibility.transparent = true
				}
			}

			if name == "html" && len(stack) == 0 && !facts.explicitHTMLRoot {
				facts.explicitHTMLRoot = true
				facts.lang = strings.TrimSpace(attrs["lang"])
				if facts.lang != "" {
					_, err := language.Parse(facts.lang)
					facts.langInvalid = err != nil || strings.Contains(facts.lang, "_")
				}
			}
			var anchor *htmlAnchor
			if href, ok := attrs["href"]; name == "a" && ok {
				anchor = &htmlAnchor{resource: -1, exposed: !visibility.inaccessible(), ariaLabel: strings.TrimSpace(attrs["aria-label"]), title: strings.TrimSpace(attrs["title"]), labelledBy: strings.Fields(attrs["aria-labelledby"])}
				if anchor.exposed {
					facts.anchorCount++
				}
				anchors = append(anchors, anchor)
				if safe, ok := inspectionAnchorURL(href); ok {
					anchor.resource = collectHTMLResource(&facts, resourceIndex, htmlResourceOccurrence{kind: "link", path: path, url: safe})
					recordHTTP(&facts, path, safe)
				}
			}
			if name == "link" && containsHTMLToken(attrs["rel"], "stylesheet") {
				if stylesheet, ok := inspectionHTTPURL(attrs["href"]); ok {
					collectHTMLResource(&facts, resourceIndex, htmlResourceOccurrence{kind: "link", path: path, url: stylesheet, text: "stylesheet"})
					recordHTTP(&facts, path, stylesheet)
				}
			}

			var label *htmlLabel
			if id := attrs["id"]; id != "" && labels[id] == nil {
				label = &htmlLabel{override: strings.TrimSpace(attrs["aria-label"]), title: strings.TrimSpace(attrs["title"]), hidden: visibility.inaccessible()}
				labels[id] = label
			}
			if name == "img" {
				facts.imageCount++
				alt, hasAlt := attrs["alt"]
				if !hasAlt {
					facts.missingAlt++
					facts.missingAltEvidence = appendBoundedHTMLEvidence(facts.missingAltEvidence, path, "img without alt")
				}
				imageName := strings.TrimSpace(attrs["aria-label"])
				if imageName == "" {
					imageName = alt
				}
				if imageName == "" {
					imageName = attrs["title"]
				}
				appendText(imageName, visibility.inaccessible())
				if label != nil {
					label.override = imageName
				}
				tiny := visibility.visuallyHidden() || smallImageDimensions(attrs, style)
				recordImage := func(src string) {
					recordImageSource(&facts, resourceIndex, path, src, alt, tiny)
				}
				recordImage(strings.TrimSpace(attrs["src"]))
				walkImageCandidates(attrs["srcset"], recordImage)
			} else if name == "source" && len(stack) > 0 && stack[len(stack)-1].name == "picture" {
				// A source can be selected by viewport/type; record candidates without
				// guessing the client's choice or the dimensions of the fallback img.
				picture := stack[len(stack)-1].visibility
				walkImageCandidates(attrs["srcset"], func(src string) {
					recordImageSource(&facts, resourceIndex, path, src, "", picture.visuallyHidden())
				})
			}
			if typeOfToken == html.StartTagToken && !isVoidElement(name) {
				position := len(stack)
				muteAnchor := anchor == nil && position > 0 && stack[position-1].muteAnchorText
				if childLabel := strings.TrimSpace(attrs["aria-label"]); anchor == nil && childLabel != "" && !muteAnchor && !visibility.inaccessible() && len(anchorHistory) != 0 {
					parentAnchor := stack[anchorHistory[len(anchorHistory)-1]].anchor
					appendBoundedNormalized(&parentAnchor.text, childLabel, 512)
					muteAnchor = true
				}
				stack = append(stack, htmlElement{name: name, label: label, anchor: anchor, visibility: visibility, muteAnchorText: muteAnchor})
				openTags[name] = append(openTags[name], position)
				if anchor != nil {
					anchorHistory = append(anchorHistory, position)
				}
				if label != nil && label.override == "" {
					if label.hidden {
						hiddenIDs = append(hiddenIDs, position)
					} else {
						visibleIDs = append(visibleIDs, position)
					}
				}
			} else if anchor != nil {
				anchor.closed = true
			}
		case html.TextToken:
			if templateDepth != 0 {
				continue
			}
			if len(stack) != 0 && stack[len(stack)-1].name != "script" && stack[len(stack)-1].name != "style" {
				appendText(token.Data, stack[len(stack)-1].visibility.inaccessible())
			}
		case html.EndTagToken:
			if templateDepth != 0 {
				if token.Data == "template" {
					templateDepth--
				}
				continue
			}
			if positions := openTags[token.Data]; len(positions) != 0 {
				index := positions[len(positions)-1]
				for i := len(stack) - 1; i >= index; i-- {
					name := stack[i].name
					if a := stack[i].anchor; a != nil {
						a.closed = true
					}
					openTags[name] = openTags[name][:len(openTags[name])-1]
				}
				stack = stack[:index]
				anchorHistory = trimPositions(anchorHistory, index)
				visibleIDs = trimPositions(visibleIDs, index)
				hiddenIDs = trimPositions(hiddenIDs, index)
			}
		}
	}

	// Reserve the resource when its tag starts, then fill in its first name
	// after forward aria-labelledby references have been seen.
	for _, anchor := range anchors {
		if !anchor.exposed {
			continue
		}
		var resolved strings.Builder
		for _, id := range anchor.labelledBy {
			if l := labels[id]; l != nil {
				appendBoundedNormalized(&resolved, l.name(), 512)
			}
		}
		label := resolved.String()
		if label == "" {
			label = normalizeHTMLText(anchor.ariaLabel)
		}
		if label == "" {
			label = anchor.text.String()
		}
		if label == "" {
			label = normalizeHTMLText(anchor.title)
		}
		if label == "" {
			if !facts.truncated || anchor.closed && len(anchor.labelledBy) == 0 {
				facts.unnamedLinks++
				facts.unnamedEvidence = appendBoundedHTMLEvidence(facts.unnamedEvidence, path, "link without conservative static name")
			}
		} else if anchor.resource >= 0 && facts.resources[anchor.resource].text == "" {
			if len(label) > 2048 {
				facts.resourceValuesDropped = true
			} else {
				facts.resources[anchor.resource].text = label
			}
		}
	}
	return facts, causes, nil
}

func recordImageSource(facts *HTMLFacts, index map[string]int, path *string, src, alt string, tiny bool) {
	kind, external := classifyImageSource(src)
	if kind == "" {
		return
	}
	if external {
		if tiny {
			kind = "tracking-pixel"
			facts.trackingPixels++
			facts.trackingEvidence = appendBoundedHTMLEvidence(facts.trackingEvidence, path, src)
		}
		facts.externalImages++
		facts.externalEvidence = appendBoundedHTMLEvidence(facts.externalEvidence, path, src)
	}
	recordHTTP(facts, path, src)
	collectHTMLResource(facts, index, htmlResourceOccurrence{kind: kind, path: path, url: src, text: normalizeHTMLText(alt)})
}

func recordHTTP(facts *HTMLFacts, path *string, source string) {
	if strings.HasPrefix(strings.ToLower(source), "http://") {
		facts.insecure++
		facts.insecureEvidence = appendBoundedHTMLEvidence(facts.insecureEvidence, path, source)
	}
}

func appendBoundedHTMLEvidence(list []Evidence, path *string, value string) []Evidence {
	if len(list) >= MaxEvidencePerFinding {
		return list
	}
	return append(list, Evidence{Source: "html", Path: path, Value: new(evidenceValue(value))})
}

// collectHTMLResource deduplicates occurrences immediately so repeated tags cannot
// grow unbounded backing arrays before report generation caps them again.
func collectHTMLResource(facts *HTMLFacts, index map[string]int, occurrence htmlResourceOccurrence) int {
	if len(occurrence.url) > 2048 || len(occurrence.text) > 2048 {
		facts.resourceValuesDropped = true
		return -1
	}
	key := occurrence.kind + "\x00" + pointerValue(occurrence.path) + "\x00" + occurrence.url
	if position, ok := index[key]; ok {
		facts.resources[position].count++
		if facts.resources[position].text == "" && occurrence.text != "" {
			facts.resources[position].text = occurrence.text
		}
		return position
	}
	if len(facts.resources) >= MaxResources {
		facts.resourcesCapped = true
		return -1
	}
	index[key] = len(facts.resources)
	occurrence.count = 1
	facts.resources = append(facts.resources, occurrence)
	return len(facts.resources) - 1
}
func htmlAttrs(attributes []html.Attribute) map[string]string {
	out := make(map[string]string, len(attributes))
	for _, attr := range attributes {
		name := strings.ToLower(attr.Key)
		if _, exists := out[name]; !exists {
			out[name] = attr.Val
		}
	}
	return out
}

// Only simple inline CSS can be evaluated without a client, stylesheet or layout.
func inspectionStyle(raw string) map[string]string {
	style := map[string]string{}
	important := map[string]bool{}
	walkCSSDeclarations(raw, func(property string, tokens []css.Token) {
		switch property {
		case "display", "visibility", "opacity", "width", "height":
		default:
			return
		}
		var buffer strings.Builder
		for _, t := range tokens {
			buffer.Write(t.Data)
		}
		value, priority, found := strings.Cut(strings.ToLower(buffer.String()), "!")
		isImportant := found && strings.TrimSpace(priority) == "important"
		if found && !isImportant || important[property] && !isImportant {
			return
		}
		if v := strings.TrimSpace(value); v != "" {
			style[property] = v
			important[property] = isImportant
		}
	})
	return style
}

func smallImageDimensions(attrs, style map[string]string) bool {
	// A banner or separator with one 1px edge still occupies a visible area.
	// With just one dimension, track the explicit evidence; do not guess units.
	dimension := func(property string) (float64, string) {
		v, hasCSS := style[property]
		if !hasCSS {
			v = strings.ToLower(strings.TrimSpace(attrs[property]))
		}
		if v == "" {
			return -1, ""
		}
		raw, px := strings.CutSuffix(v, "px")
		n, err := strconv.ParseFloat(raw, 64)
		if err != nil || n < 0 || (hasCSS && n != 0 && !px) {
			return -1, v
		}
		return n, v
	}
	w, wv := dimension("width")
	h, hv := dimension("height")
	if w == 0 || h == 0 {
		return true
	}
	tinyW, tinyH := w > 0 && w <= 1, h > 0 && h <= 1
	return tinyW && tinyH || tinyW && hv == "" || tinyH && wv == ""
}

func classifyImageSource(source string) (string, bool) {
	if source == "" {
		return "", false
	}
	lower := strings.ToLower(source)
	if strings.HasPrefix(lower, "cid:") {
		return "cid", false
	}
	if strings.HasPrefix(lower, "data:") {
		return "data", false
	}
	parsed, err := url.Parse(source)
	if err != nil || parsed.Hostname() == "" || parsed.Scheme != "" && parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", false
	}
	return "image", true
}

func inspectionAnchorURL(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, "\x00\r\n\t") {
		return "", false
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return "", false
	}
	switch strings.ToLower(parsed.Scheme) {
	case "", "http", "https":
		if parsed.Hostname() == "" {
			return "", false
		}
	case "mailto":
		if parsed.Opaque == "" && parsed.Path == "" {
			return "", false
		}
	default:
		return "", false
	}
	return value, true
}

func inspectionHTTPURL(value string) (string, bool) {
	safe, ok := inspectionAnchorURL(value)
	if !ok {
		return "", false
	}
	parsed, err := url.Parse(safe)
	return safe, err == nil && (parsed.Scheme == "" || strings.EqualFold(parsed.Scheme, "http") || strings.EqualFold(parsed.Scheme, "https"))
}

func containsHTMLToken(value, expected string) bool {
	for _, token := range strings.Fields(value) {
		if strings.EqualFold(token, expected) {
			return true
		}
	}
	return false
}

func normalizeHTMLText(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func appendBoundedNormalized(builder *strings.Builder, value string, limit int) {
	appendBoundedText(builder, normalizeHTMLText(value), limit)
}

func appendBoundedText(builder *strings.Builder, value string, limit int) {
	if builder.Len() >= limit || value == "" {
		return
	}
	remaining := limit - builder.Len()
	if builder.Len() != 0 {
		builder.WriteByte(' ')
		remaining = limit - builder.Len()
	}
	if len(value) > remaining {
		value = truncateUTF8(value, remaining)
	}
	builder.WriteString(value)
}
