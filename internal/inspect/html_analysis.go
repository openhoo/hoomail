package inspect

import (
	"bytes"
	"context"
	"io"
	"net/url"
	"strconv"
	"strings"

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
	href          string
	ariaLabel     string
	labelledBy    []string
	title         string
	text          strings.Builder
	childImageAlt strings.Builder
}

type htmlElement struct {
	name   string
	id     string
	text   strings.Builder
	anchor *htmlAnchor
	hidden bool
}

type pendingAnchor struct {
	href       string
	ariaLabel  string
	labelledBy []string
	title      string
	text       string
	imageAlt   string
}

func analyzeHTML(ctx context.Context, raw []byte, path *string) (HTMLFacts, []string, error) {
	facts := HTMLFacts{resources: make([]htmlResourceOccurrence, 0)}
	causes := make([]string, 0, 2)
	tokenizer := html.NewTokenizer(bytes.NewReader(raw))
	tokenizer.SetMaxBuf(MaxHTMLTokenBytes)
	stack := make([]htmlElement, 0, 32)
	pending := make([]pendingAnchor, 0)
	idText := make(map[string]string)
	nodes := 0
	openTags := make(map[string][]int)
	activeIDs := make([]int, 0, 16)
	anchorHistory := make([]int, 0, 8)
	nearestAnchor := -1
	resourceIndex := make(map[string]int)
	for {
		typeOfToken := tokenizer.Next()
		if typeOfToken == html.ErrorToken {
			err := tokenizer.Err()
			if err != nil && err != io.EOF {
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
				return facts, causes, nil
			}
			if nodes%1024 == 0 {
				if err := ctx.Err(); err != nil {
					return facts, causes, err
				}
			}
			name := strings.ToLower(token.Data)
			attrs := htmlAttrs(token.Attr)
			hidden := hiddenHTML(attrs) || strings.EqualFold(strings.TrimSpace(attrs["aria-hidden"]), "true") || len(stack) > 0 && stack[len(stack)-1].hidden
			if name == "html" && len(stack) == 0 {
				facts.explicitHTMLRoot = true
				facts.lang = strings.TrimSpace(attrs["lang"])
				if facts.lang != "" {
					_, err := language.Parse(facts.lang)
					facts.langInvalid = err != nil
				}
			}
			var anchor *htmlAnchor
			if name == "a" {
				facts.anchorCount++
				anchor = &htmlAnchor{href: strings.TrimSpace(attrs["href"]), ariaLabel: strings.TrimSpace(attrs["aria-label"]), title: strings.TrimSpace(attrs["title"])}
				anchor.labelledBy = strings.Fields(attrs["aria-labelledby"])
			}
			if name == "link" && containsHTMLToken(attrs["rel"], "stylesheet") {
				if stylesheet, ok := inspectionHTTPURL(attrs["href"]); ok {
					collectHTMLResource(&facts, resourceIndex, htmlResourceOccurrence{kind: "link", path: path, url: stylesheet, text: "stylesheet"})
					if strings.HasPrefix(strings.ToLower(stylesheet), "http://") {
						facts.insecure++
						facts.insecureEvidence = appendBoundedHTMLEvidence(facts.insecureEvidence, path, stylesheet)
					}
				}
			}
			if name == "img" {
				facts.imageCount++
				alt, hasAlt := attrs["alt"]
				if !hasAlt {
					facts.missingAlt++
					facts.missingAltEvidence = appendBoundedHTMLEvidence(facts.missingAltEvidence, path, "img without alt")
				}
				if !hidden && nearestAnchor >= 0 {
					appendBoundedNormalized(&stack[nearestAnchor].anchor.childImageAlt, alt, 512)
				}
				source := strings.TrimSpace(attrs["src"])
				if source != "" {
					kind, external := classifyImageSource(source)
					if kind != "" {
						tracking := external && (hidden || dimensionAtMostOne(attrs["width"]) || dimensionAtMostOne(attrs["height"]) || styleDimensionAtMostOne(attrs["style"], "width") || styleDimensionAtMostOne(attrs["style"], "height"))
						if tracking {
							kind = "tracking-pixel"
							facts.trackingPixels++
							facts.trackingEvidence = appendBoundedHTMLEvidence(facts.trackingEvidence, path, source)
						}
						if external {
							facts.externalImages++
							facts.externalEvidence = appendBoundedHTMLEvidence(facts.externalEvidence, path, source)
						}
						if strings.HasPrefix(strings.ToLower(source), "http://") {
							facts.insecure++
							facts.insecureEvidence = appendBoundedHTMLEvidence(facts.insecureEvidence, path, source)
						}
						collectHTMLResource(&facts, resourceIndex, htmlResourceOccurrence{kind: kind, path: path, url: source, text: normalizeHTMLText(alt)})
					}
				}
			}
			id := strings.TrimSpace(attrs["id"])
			if id == "" {
				id = strings.TrimSpace(attrs["name"])
			}
			if id != "" && len(idText) >= MaxHTMLNodes {
				id = ""
			}
			if typeOfToken == html.StartTagToken && !isVoidElement(name) {
				position := len(stack)
				stack = append(stack, htmlElement{name: name, id: id, anchor: anchor, hidden: hidden})
				openTags[name] = append(openTags[name], position)
				if anchor != nil {
					nearestAnchor = position
					anchorHistory = append(anchorHistory, position)
				}
				if id != "" {
					activeIDs = append(activeIDs, position)
				}
			} else if anchor != nil {
				pending = append(pending, pendingAnchor{href: anchor.href, ariaLabel: anchor.ariaLabel, labelledBy: anchor.labelledBy, title: anchor.title})
			}
		case html.TextToken:
			text := normalizeHTMLText(token.Data)
			if text == "" {
				continue
			}
			alive := activeIDs[:0]
			for _, position := range activeIDs {
				if position >= len(stack) {
					continue
				}
				element := &stack[position]
				appendBoundedNormalized(&element.text, text, 512)
				if element.text.Len() < 512 {
					alive = append(alive, position)
				}
			}
			activeIDs = alive
			if len(stack) == 0 || !stack[len(stack)-1].hidden {
				if nearestAnchor >= 0 {
					appendBoundedNormalized(&stack[nearestAnchor].anchor.text, text, 512)
				}
			}
		case html.EndTagToken:
			name := strings.ToLower(token.Data)
			positions := openTags[name]
			if len(positions) != 0 {
				index := positions[len(positions)-1]
				openTags[name] = positions[:len(positions)-1]
				for candidate := len(stack) - 1; candidate >= index; candidate-- {
					element := stack[candidate]
					if owned := openTags[element.name]; len(owned) != 0 && owned[len(owned)-1] == candidate {
						openTags[element.name] = owned[:len(owned)-1]
					}
					if element.id != "" {
						idText[element.id] = normalizeHTMLText(element.text.String())
					}
					if element.anchor != nil {
						a := element.anchor
						pending = append(pending, pendingAnchor{href: a.href, ariaLabel: a.ariaLabel, labelledBy: a.labelledBy, title: a.title, text: a.text.String(), imageAlt: a.childImageAlt.String()})
					}
				}
				stack = stack[:index]
				for len(anchorHistory) != 0 && anchorHistory[len(anchorHistory)-1] >= index {
					anchorHistory = anchorHistory[:len(anchorHistory)-1]
				}
				if len(anchorHistory) == 0 {
					nearestAnchor = -1
				} else {
					nearestAnchor = anchorHistory[len(anchorHistory)-1]
				}
			}
		}
	}
	for _, element := range stack {
		if element.id != "" {
			idText[element.id] = normalizeHTMLText(element.text.String())
		}
	}
	for _, element := range stack {
		if element.anchor != nil {
			a := element.anchor
			pending = append(pending, pendingAnchor{href: a.href, ariaLabel: a.ariaLabel, labelledBy: a.labelledBy, title: a.title, text: a.text.String(), imageAlt: a.childImageAlt.String()})
		}
	}
	for _, anchor := range pending {
		label := normalizeHTMLText(anchor.ariaLabel)
		if label == "" && len(anchor.labelledBy) != 0 {
			var resolved strings.Builder
			for _, id := range anchor.labelledBy {
				appendBoundedNormalized(&resolved, idText[id], 512)
			}
			label = normalizeHTMLText(resolved.String())
		}
		if label == "" {
			label = normalizeHTMLText(anchor.text)
		}
		if label == "" {
			label = normalizeHTMLText(anchor.imageAlt)
		}
		if label == "" {
			facts.unnamedLinks++
			value := "anchor without conservative static name"
			if anchor.title != "" {
				value += "; title=" + anchor.title
			}
			facts.unnamedEvidence = appendBoundedHTMLEvidence(facts.unnamedEvidence, path, value)
		}
		if safe, ok := inspectionAnchorURL(anchor.href); ok {
			collectHTMLResource(&facts, resourceIndex, htmlResourceOccurrence{kind: "link", path: path, url: safe, text: label})
			if strings.HasPrefix(strings.ToLower(safe), "http://") {
				facts.insecure++
				facts.insecureEvidence = appendBoundedHTMLEvidence(facts.insecureEvidence, path, safe)
			}
		}
	}
	return facts, causes, nil
}

func appendBoundedHTMLEvidence(list []Evidence, path *string, value string) []Evidence {
	if len(list) >= MaxEvidencePerFinding {
		return list
	}
	return append(list, Evidence{Source: "html", Path: path, Value: new(evidenceValue(value))})
}

// collectHTMLResource deduplicates occurrences immediately so repeated tags cannot
// grow unbounded backing arrays before report generation caps them again.
func collectHTMLResource(facts *HTMLFacts, index map[string]int, occurrence htmlResourceOccurrence) {
	if len(occurrence.url) > 2048 || len(occurrence.text) > 2048 {
		facts.resourceValuesDropped = true
		return
	}
	key := occurrence.kind + "\x00" + pointerValue(occurrence.path) + "\x00" + occurrence.url
	if position, ok := index[key]; ok {
		facts.resources[position].count++
		if facts.resources[position].text == "" && occurrence.text != "" {
			facts.resources[position].text = occurrence.text
		}
		return
	}
	if len(facts.resources) >= MaxResources {
		facts.resourcesCapped = true
		return
	}
	index[key] = len(facts.resources)
	occurrence.count = 1
	facts.resources = append(facts.resources, occurrence)
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

func hiddenHTML(attrs map[string]string) bool {
	if _, ok := attrs["hidden"]; ok {
		return true
	}
	style := strings.ToLower(strings.ReplaceAll(attrs["style"], " ", ""))
	return strings.Contains(style, "display:none") || strings.Contains(style, "visibility:hidden") || strings.Contains(style, "opacity:0")
}

func dimensionAtMostOne(value string) bool {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.TrimSuffix(value, "px")
	if value == "" || strings.HasSuffix(value, "%") {
		return false
	}
	dimension, err := strconv.ParseFloat(value, 64)
	return err == nil && dimension <= 1
}

func styleDimensionAtMostOne(style, property string) bool {
	for declaration := range strings.SplitSeq(style, ";") {
		name, value, ok := strings.Cut(declaration, ":")
		if ok && strings.EqualFold(strings.TrimSpace(name), property) && dimensionAtMostOne(strings.TrimSpace(value)) {
			return true
		}
	}
	return false
}

func classifyImageSource(source string) (string, bool) {
	lower := strings.ToLower(source)
	if strings.HasPrefix(lower, "cid:") {
		return "cid", false
	}
	if strings.HasPrefix(lower, "data:") {
		return "data", false
	}
	parsed, err := url.Parse(source)
	if err != nil || parsed.Host == "" || parsed.Scheme != "http" && parsed.Scheme != "https" {
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
	return value, true
}

func inspectionHTTPURL(value string) (string, bool) {
	safe, ok := inspectionAnchorURL(value)
	if !ok {
		return "", false
	}
	parsed, err := url.Parse(safe)
	return safe, err == nil && (strings.EqualFold(parsed.Scheme, "http") || strings.EqualFold(parsed.Scheme, "https"))
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

func appendNormalized(builder *strings.Builder, value string) {
	value = normalizeHTMLText(value)
	if value == "" {
		return
	}
	if builder.Len() != 0 {
		builder.WriteByte(' ')
	}
	builder.WriteString(value)
}

func appendBoundedNormalized(builder *strings.Builder, value string, limit int) {
	if builder.Len() >= limit {
		return
	}
	value = normalizeHTMLText(value)
	if value == "" {
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
