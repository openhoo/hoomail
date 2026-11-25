package inspect

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"io"
	"sort"
	"strings"

	"golang.org/x/net/html"
)

// Can I Email API export (MIT): https://www.caniemail.com/api/data.json
// Snapshot: 2026-09-16; SHA-256: 7328f873fae3bb24455d10bf3005464b2af12cbbcc5b54da51683d4911235628.
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

	truncationCause string
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
var compatibilityRules = compatibilityRuleIDs()

func analyzeHTMLCompatibility(raw []byte) *HTMLCompatibility {
	if len(raw) == 0 {
		return nil
	}
	elements := 0
	truncated := false
	counts := map[string]int{}
	nodes := 0
	inStyle := false
	styleBytes := 0
	truncationCause := ""
	stylesTruncated := false
	t := html.NewTokenizer(bytes.NewReader(raw))
	t.SetMaxBuf(MaxHTMLTokenBytes)
	for {
		typ := t.Next()
		if typ == html.ErrorToken {
			if err := t.Err(); err != nil && err != io.EOF {
				truncated = true
				truncationCause = "HTML token bytes"
			}
			break
		}
		tok := t.Token()
		switch typ {
		case html.StartTagToken, html.SelfClosingTagToken:
			name := strings.ToLower(tok.Data)
			elements++
			if elements > MaxHTMLNodes {
				truncated = true
				truncationCause = "HTML nodes"
				goto done
			}
			if name != "html" && name != "head" && name != "meta" && name != "body" {
				nodes++
			}
			attrs := htmlAttrs(tok.Attr)
			inspectCompatibilityTag(name, attrs, counts)
			if style := attrs["style"]; style != "" {
				inspectCompatibilityCSS(style, true, counts)
			}
			if name == "style" && typ == html.StartTagToken {
				kind := strings.TrimSpace(attrs["type"])
				inStyle = kind == "" || strings.EqualFold(kind, "text/css")
			}
		case html.TextToken:
			if inStyle {
				remaining := MaxHTMLTokenBytes - styleBytes
				source := tok.Data
				if len(source) > remaining {
					source = source[:remaining]
					stylesTruncated = true
				}
				if len(source) > 0 {
					inspectCompatibilityCSS(source, false, counts)
					styleBytes += len(source)
				}
			}
		case html.EndTagToken:
			if strings.EqualFold(tok.Data, "style") {
				inStyle = false
			}
		}
	}

done:
	slugs := make([]string, 0, len(counts))
	for slug, count := range counts {
		if count > maxCompatibilityOccurrences {
			count = maxCompatibilityOccurrences
			truncated = true
			if truncationCause == "" {
				truncationCause = "compatibility occurrences"
			}
		}
		counts[slug] = count
		if count > 0 {
			if _, ok := caniEmailFeatures[slug]; ok || slug == "html-script" {
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
		if slug == "html-script" {
			// Mailpit's one hand-authored rule; the dataset has no script feature.
			warning.Title = "<script> element"
			warning.Category = "html"
			warning.Description = "Executable scripts are generally blocked by email clients."
			warning.URL = "https://mailpit.axllent.org/docs/usage/html-check/"
			warning.Score.Unsupported = 100
		}
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
		if elements > 0 {
			weight := min(1.0, float64(warning.Occurrences)/float64(max(1, nodes)))
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
	isTruncated := truncated || stylesTruncated
	cause := truncationCause
	if cause == "" && stylesTruncated {
		cause = "style bytes"
	}
	return &HTMLCompatibility{
		DataVersion: canIEmailDataset.APIVersion, DataUpdated: canIEmailDataset.LastUpdateDate,
		Nodes: nodes, Tests: len(compatibilityRules), Score: score, Platforms: platformList, Warnings: warnings,
		WarningsTruncated: warningsTruncated, ClientsTruncated: clientsTruncated, Truncated: isTruncated,
		truncationCause: cause,
	}
}

func compatibilitySupport(raw json.RawMessage, notes map[string]string) (string, *string) {
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return "unknown", nil
	}
	tokens := strings.Fields(value)
	if len(tokens) == 0 {
		return "unknown", nil
	}
	support := "unknown"
	switch tokens[0] {
	case "y":
		support = "yes"
	case "a":
		support = "partial"
	case "n":
		support = "no"
	}
	var noteTexts []string
	for _, token := range tokens[1:] {
		if !strings.HasPrefix(token, "#") {
			continue
		}
		if text := notes[strings.TrimPrefix(token, "#")]; text != "" {
			noteTexts = append(noteTexts, text)
		}
	}
	if len(noteTexts) > 0 {
		combined := strings.Join(noteTexts, "\n")
		return support, &combined
	}
	return support, nil
}

func compatibilityTotalScore(partial, unsupported float64) CompatibilityScore {
	if partial < 0 {
		partial = 0
	}
	if unsupported < 0 {
		unsupported = 0
	}
	unsupported = min(100, unsupported)
	partial = min(100-unsupported, partial)
	return CompatibilityScore{Supported: 100 - partial - unsupported, Partial: partial, Unsupported: unsupported}
}
