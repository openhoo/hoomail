package inspect

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"io"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/net/html"
)

// Exact Mailpit caniemail-data.json clone at b859b0d986d66bbf1d7d9533317faf867afa1ed1 (MIT).
//
//go:embed caniemail-data.json
var canIEmailData []byte

const (
	maxCompatibilityWarnings    = 128
	maxCompatibilityClients     = 256
	maxCompatibilityPlatforms   = 128
	maxCompatibilityOccurrences = MaxHTMLNodes
)

type CompatibilityScore struct {
	Supported   float64 `json:"supported"`
	Partial     float64 `json:"partial"`
	Unsupported float64 `json:"unsupported"`
}
type CompatibilityClient struct {
	Name     string  `json:"name"`
	Family   string  `json:"family"`
	Platform string  `json:"platform"`
	Version  string  `json:"version"`
	Support  string  `json:"support"`
	Note     *string `json:"note"`
}
type CompatibilityWarning struct {
	Slug        string                `json:"slug"`
	Title       string                `json:"title"`
	Category    string                `json:"category"`
	Description string                `json:"description"`
	URL         string                `json:"url"`
	Occurrences int                   `json:"occurrences"`
	Score       CompatibilityScore    `json:"score"`
	Clients     []CompatibilityClient `json:"clients"`
}
type CompatibilityPlatform struct {
	Family   string `json:"family"`
	Platform string `json:"platform"`
	Label    string `json:"label"`
}
type HTMLCompatibility struct {
	DataVersion       string                  `json:"dataVersion"`
	DataUpdated       string                  `json:"dataUpdated"`
	Nodes             int                     `json:"nodes"`
	Tests             int                     `json:"tests"`
	Score             CompatibilityScore      `json:"score"`
	Platforms         []CompatibilityPlatform `json:"platforms"`
	Warnings          []CompatibilityWarning  `json:"warnings"`
	WarningsTruncated bool                    `json:"warningsTruncated,omitempty"`
	ClientsTruncated  bool                    `json:"clientsTruncated,omitempty"`
	Truncated         bool                    `json:"truncated,omitempty"`
}

type caniEmailFile struct {
	APIVersion     string `json:"api_version"`
	LastUpdateDate string `json:"last_update_date"`
	NiceNames      struct {
		Family   map[string]string `json:"family"`
		Platform map[string]string `json:"platform"`
	} `json:"nicenames"`
	Data []caniEmailFeature `json:"data"`
}
type caniEmailFeature struct {
	Slug        string                                           `json:"slug"`
	Title       string                                           `json:"title"`
	Description string                                           `json:"description"`
	URL         string                                           `json:"url"`
	Category    string                                           `json:"category"`
	Stats       map[string]map[string]map[string]json.RawMessage `json:"stats"`
	Notes       map[string]string                                `json:"notes_by_num"`
}

var canIEmailDataset = func() caniEmailFile {
	var file caniEmailFile
	if err := json.Unmarshal(canIEmailData, &file); err != nil {
		panic(err)
	}
	return file
}()

var caniEmailFeatures = func() map[string]caniEmailFeature {
	out := make(map[string]caniEmailFeature, len(canIEmailDataset.Data))
	for _, feature := range canIEmailDataset.Data {
		out[feature.Slug] = feature
	}
	return out
}()
var compatibilityCSSRegex = func() map[string]*regexp.Regexp {
	out := make(map[string]*regexp.Regexp)
	for k, v := range cssInlineRegexTests {
		out[k] = v
	}
	for k, v := range cssRegexpTests {
		out[k] = v
	}
	for k, v := range cssRegexpUnitTests {
		out[k] = v
	}
	return out
}()

func analyzeHTMLCompatibility(raw []byte) *HTMLCompatibility {
	if len(raw) == 0 {
		return nil
	}
	fullDocument := regexp.MustCompile(`(?i)</body\s*>`).Match(raw)
	truncated := false
	counts := map[string]int{}
	nodes := 0
	inStyle := false
	var styles strings.Builder
	t := html.NewTokenizer(bytes.NewReader(raw))
	t.SetMaxBuf(MaxHTMLTokenBytes)
	for {
		typ := t.Next()
		if typ == html.ErrorToken {
			if err := t.Err(); err != nil && err != io.EOF {
				truncated = true
			}
			break
		}
		tok := t.Token()
		switch typ {
		case html.StartTagToken, html.SelfClosingTagToken:
			name := strings.ToLower(tok.Data)
			if !fullDocument || (name != "html" && name != "head" && name != "meta" && name != "body") {
				nodes++
				if nodes > MaxHTMLNodes {
					nodes = MaxHTMLNodes
					goto done
				}
			}
			attrs := htmlAttrs(tok.Attr)
			for slug := range htmlTests {
				if slug == "html-body" && !fullDocument {
					continue
				}
				if compatibilityHTMLMatch(slug, htmlTests[slug], name, attrs) {
					counts[slug]++
				}
			}
			if name == "script" && !strings.EqualFold(attrs["type"], "application/ld+json") && !strings.EqualFold(attrs["type"], "application/json") {
				counts["html-script"]++
			}
			if name == "img" {
				src := strings.ToLower(strings.TrimSpace(attrs["src"]))
				for slug, re := range imageRegexpTests {
					if re.MatchString(src) {
						counts[slug]++
					}
				}
			}
			if style := attrs["style"]; style != "" {
				for slug, re := range cssInlineRegexTests {
					if re.MatchString(style) {
						counts[slug]++
					}
				}
				for slug, re := range cssRegexpUnitTests {
					counts[slug] += len(re.FindAllString(style, maxCompatibilityOccurrences))
				}
			}
			for slug, selector := range styleInlineAttributes {
				attr := strings.Trim(selector, "[]")
				if _, ok := attrs[attr]; ok {
					counts[slug]++
				}
			}
			if name == "style" && typ == html.StartTagToken {
				inStyle = true
			}
		case html.TextToken:
			if inStyle && styles.Len() < MaxHTMLTokenBytes {
				remaining := MaxHTMLTokenBytes - styles.Len()
				text := tok.Data
				if len(text) > remaining {
					text = text[:remaining]
				}
				styles.WriteString(text)
			}
		case html.EndTagToken:
			if strings.EqualFold(tok.Data, "style") {
				inStyle = false
			}
		}
	}

done:
	css := styles.String()
	for _, match := range regexp.MustCompile(`(?s)\{([^{}]*)\}`).FindAllStringSubmatch(css, maxCompatibilityOccurrences) {
		declarations := match[1]
		for slug, re := range cssInlineRegexTests {
			if re.MatchString(declarations) {
				counts[slug]++
			}
		}
		for slug, re := range cssRegexpUnitTests {
			counts[slug] += len(re.FindAllString(declarations, maxCompatibilityOccurrences))
		}
	}
	for slug, re := range cssRegexpTests {
		counts[slug] += len(re.FindAllString(css, maxCompatibilityOccurrences))
	}

	tests := 1 + len(htmlTests) + len(imageRegexpTests) + len(cssInlineRegexTests) + len(styleInlineAttributes) + len(cssRegexpUnitTests) + len(cssRegexpTests)
	slugs := make([]string, 0, len(counts))
	for slug, count := range counts {
		if count > maxCompatibilityOccurrences {
			count = maxCompatibilityOccurrences
		}
		counts[slug] = count
		if count > 0 {
			if _, ok := caniEmailFeatures[slug]; ok {
				slugs = append(slugs, slug)
			}
		}
	}
	sort.Strings(slugs)

	warningsTruncated := len(slugs) > maxCompatibilityWarnings
	warnings := make([]CompatibilityWarning, 0, min(len(slugs), maxCompatibilityWarnings))
	platforms := map[string]CompatibilityPlatform{}
	clientsTruncated := false
	partial, unsupported := 0.0, 0.0
	for index, slug := range slugs {
		feature := caniEmailFeatures[slug]
		warning := CompatibilityWarning{Slug: slug, Title: feature.Title, Category: feature.Category, Description: feature.Description, URL: feature.URL, Occurrences: counts[slug], Clients: []CompatibilityClient{}}
		for family, byPlatform := range feature.Stats {
			for platform, versions := range byPlatform {
				for version, rawSupport := range versions {
					support, note := compatibilitySupport(rawSupport, feature.Notes)
					familyLabel := canIEmailDataset.NiceNames.Family[family]
					if familyLabel == "" {
						familyLabel = family
					}
					platformLabel := canIEmailDataset.NiceNames.Platform[platform]
					if platformLabel == "" {
						platformLabel = platform
					}
					warning.Clients = append(warning.Clients, CompatibilityClient{Name: familyLabel + " " + platformLabel + " (" + version + ")", Family: family, Platform: platform, Version: version, Support: support, Note: note})
					switch support {
					case "yes":
						warning.Score.Supported++
					case "partial":
						warning.Score.Partial++
					case "no":
						warning.Score.Unsupported++
					}
				}
			}
		}
		sort.Slice(warning.Clients, func(i, j int) bool {
			if warning.Clients[i].Family != warning.Clients[j].Family {
				return warning.Clients[i].Family < warning.Clients[j].Family
			}
			if warning.Clients[i].Platform != warning.Clients[j].Platform {
				return warning.Clients[i].Platform < warning.Clients[j].Platform
			}
			return warning.Clients[i].Version < warning.Clients[j].Version
		})
		total := warning.Score.Supported + warning.Score.Partial + warning.Score.Unsupported
		if total > 0 {
			warning.Score.Supported = warning.Score.Supported / total * 100
			warning.Score.Partial = warning.Score.Partial / total * 100
			warning.Score.Unsupported = warning.Score.Unsupported / total * 100
		}
		if nodes > 0 {
			weight := float64(warning.Occurrences) / float64(nodes)
			partial = max(partial, warning.Score.Partial*weight)
			unsupported = max(unsupported, warning.Score.Unsupported*weight)
		}
		if index >= maxCompatibilityWarnings {
			continue
		}
		if len(warning.Clients) > maxCompatibilityClients {
			warning.Clients = warning.Clients[:maxCompatibilityClients]
			clientsTruncated = true
		}
		for _, client := range warning.Clients {
			platformLabel := canIEmailDataset.NiceNames.Platform[client.Platform]
			if platformLabel == "" {
				platformLabel = client.Platform
			}
			platforms[client.Family+"\x00"+client.Platform] = CompatibilityPlatform{Family: client.Family, Platform: client.Platform, Label: platformLabel}
		}
		warnings = append(warnings, warning)
	}

	score := compatibilityTotalScore(partial, unsupported)
	platformList := make([]CompatibilityPlatform, 0, len(platforms))
	if !clientsTruncated {
		for _, platform := range platforms {
			platformList = append(platformList, platform)
		}
		sort.Slice(platformList, func(i, j int) bool {
			if platformList[i].Family != platformList[j].Family {
				return platformList[i].Family < platformList[j].Family
			}
			return platformList[i].Platform < platformList[j].Platform
		})
		if len(platformList) > maxCompatibilityPlatforms {
			platformList = []CompatibilityPlatform{}
			clientsTruncated = true
		}
	}
	return &HTMLCompatibility{
		DataVersion: canIEmailDataset.APIVersion, DataUpdated: canIEmailDataset.LastUpdateDate,
		Nodes: nodes, Tests: tests, Score: score, Platforms: platformList, Warnings: warnings,
		WarningsTruncated: warningsTruncated, ClientsTruncated: clientsTruncated, Truncated: truncated,
	}
}

func compatibilitySupport(raw json.RawMessage, notes map[string]string) (string, *string) {
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return "partial", nil
	}
	switch value {
	case "y":
		return "yes", nil
	case "n":
		return "no", nil
	}
	var noteTexts []string
	for _, token := range strings.Fields(value) {
		if !strings.HasPrefix(token, "#") {
			continue
		}
		if text := notes[strings.TrimPrefix(token, "#")]; text != "" {
			noteTexts = append(noteTexts, text)
		}
	}
	if len(noteTexts) > 0 {
		combined := strings.Join(noteTexts, "\n")
		return "partial", &combined
	}
	return "partial", nil
}

func compatibilityTotalScore(partial, unsupported float64) CompatibilityScore {
	if partial < 0 {
		partial = 0
	}
	if unsupported < 0 {
		unsupported = 0
	}
	if partial > 100 {
		partial = 100
	}
	if unsupported > 100-partial {
		unsupported = 100 - partial
	}
	return CompatibilityScore{Supported: 100 - partial - unsupported, Partial: partial, Unsupported: unsupported}
}
func compatibilityHTMLMatch(slug string, _ string, name string, attrs map[string]string) bool {
	switch slug {
	case "html-body":
		return name == "body"
	case "html-object":
		return name == "object" || name == "embed" || name == "image" || name == "pdf"
	case "html-link":
		return name == "link"
	case "html-srcset":
		_, ok := attrs["srcset"]
		return ok
	case "html-required":
		_, ok := attrs["required"]
		return ok
	case "html-image-maps":
		return name == "map" || (name == "img" && attrs["usemap"] != "")
	case "html-anchor-links":
		return name == "a" && strings.HasPrefix(attrs["href"], "#")
	case "html-input-checkbox", "html-input-radio", "html-input-text":
		return name == "input" && strings.EqualFold(attrs["type"], strings.TrimPrefix(slug, "html-input-"))
	case "html-input-hidden":
		return name == "input" && strings.EqualFold(attrs["type"], "hidden")
	case "html-input-submit":
		return name == "input" && strings.EqualFold(attrs["type"], "submit")
	case "html-button-reset":
		return name == "button" && strings.EqualFold(attrs["type"], "reset")
	case "html-button-submit":
		return name == "button" && strings.EqualFold(attrs["type"], "submit")
	}
	for _, x := range strings.Split(htmlTests[slug], ",") {
		if strings.TrimSpace(x) == name {
			return true
		}
	}
	if strings.HasPrefix(htmlTests[slug], "[") {
		k := strings.Trim(htmlTests[slug], "[]")
		_, ok := attrs[k]
		return ok
	}
	return false
}
