package inspect

import (
	"bytes"
	"io"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/text/language"
)

type HTMLFacts struct {
	explicitHTMLRoot   bool
	lang               string
	langInvalid        bool
	imageCount         int
	missingAlt         int
	anchorCount        int
	unnamedLinks       int
	externalImages     int
	trackingPixels     int
	insecure           int
	truncated          bool
	resources          []htmlResourceOccurrence
	missingAltEvidence []Evidence
	unnamedEvidence    []Evidence
	externalEvidence   []Evidence
	trackingEvidence   []Evidence
	insecureEvidence   []Evidence
}

type htmlResourceOccurrence struct {
	kind string
	path *string
	url  string
	text string
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

func analyzeHTML(raw []byte, path *string) (HTMLFacts, []string) {
	facts := HTMLFacts{resources: make([]htmlResourceOccurrence, 0)}
	causes := make([]string, 0, 2)
	tokenizer := html.NewTokenizer(bytes.NewReader(raw))
	tokenizer.SetMaxBuf(MaxHTMLTokenBytes)
	stack := make([]htmlElement, 0, 32)
	pending := make([]pendingAnchor, 0)
	idText := make(map[string]string)
	nodes := 0
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
				return facts, causes
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
			if name == "img" {
				facts.imageCount++
				alt, hasAlt := attrs["alt"]
				if !hasAlt {
					facts.missingAlt++
					facts.missingAltEvidence = append(facts.missingAltEvidence, Evidence{Source: "html", Path: path, Value: new("img without alt")})
				}
				if !hidden {
					for index := len(stack) - 1; index >= 0; index-- {
						if stack[index].anchor != nil {
							appendBoundedNormalized(&stack[index].anchor.childImageAlt, alt, 512)
							break
						}
					}
				}
				source := strings.TrimSpace(attrs["src"])
				if source != "" {
					kind, external := classifyImageSource(source)
					if kind != "" {
						tracking := external && (hidden || dimensionAtMostOne(attrs["width"]) || dimensionAtMostOne(attrs["height"]) || styleDimensionAtMostOne(attrs["style"], "width") || styleDimensionAtMostOne(attrs["style"], "height"))
						if tracking {
							kind = "tracking-pixel"
							facts.trackingPixels++
							facts.trackingEvidence = append(facts.trackingEvidence, Evidence{Source: "html", Path: path, Value: new(evidenceValue(source))})
						}
						if external {
							facts.externalImages++
							facts.externalEvidence = append(facts.externalEvidence, Evidence{Source: "html", Path: path, Value: new(evidenceValue(source))})
						}
						if strings.HasPrefix(strings.ToLower(source), "http://") {
							facts.insecure++
							facts.insecureEvidence = append(facts.insecureEvidence, Evidence{Source: "html", Path: path, Value: new(evidenceValue(source))})
						}
						facts.resources = append(facts.resources, htmlResourceOccurrence{kind: kind, path: path, url: source, text: normalizeHTMLText(alt)})
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
				stack = append(stack, htmlElement{name: name, id: id, anchor: anchor, hidden: hidden})
			} else if anchor != nil {
				pending = append(pending, pendingAnchor{href: anchor.href, ariaLabel: anchor.ariaLabel, labelledBy: anchor.labelledBy, title: anchor.title})
			}
		case html.TextToken:
			text := normalizeHTMLText(token.Data)
			if text == "" {
				continue
			}
			for index := range stack {
				if stack[index].id != "" {
					appendBoundedNormalized(&stack[index].text, text, 512)
				}
			}
			if len(stack) == 0 || !stack[len(stack)-1].hidden {
				for index := len(stack) - 1; index >= 0; index-- {
					if stack[index].anchor != nil {
						appendBoundedNormalized(&stack[index].anchor.text, text, 512)
						break
					}
				}
			}
		case html.EndTagToken:
			name := strings.ToLower(token.Data)
			index := -1
			for candidate := len(stack) - 1; candidate >= 0; candidate-- {
				if stack[candidate].name == name {
					index = candidate
					break
				}
			}
			if index >= 0 {
				for candidate := len(stack) - 1; candidate >= index; candidate-- {
					if stack[candidate].id != "" {
						idText[stack[candidate].id] = normalizeHTMLText(stack[candidate].text.String())
					}
					if stack[candidate].anchor != nil {
						a := stack[candidate].anchor
						pending = append(pending, pendingAnchor{href: a.href, ariaLabel: a.ariaLabel, labelledBy: a.labelledBy, title: a.title, text: a.text.String(), imageAlt: a.childImageAlt.String()})
					}
				}
				stack = stack[:index]
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
			facts.unnamedEvidence = append(facts.unnamedEvidence, Evidence{Source: "html", Path: path, Value: new(evidenceValue(value))})
		}
		if safe, ok := inspectionAnchorURL(anchor.href); ok {
			facts.resources = append(facts.resources, htmlResourceOccurrence{kind: "link", path: path, url: safe, text: label})
			if strings.HasPrefix(strings.ToLower(safe), "http://") {
				facts.insecure++
				facts.insecureEvidence = append(facts.insecureEvidence, Evidence{Source: "html", Path: path, Value: new(evidenceValue(safe))})
			}
		}
	}
	return facts, causes
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
