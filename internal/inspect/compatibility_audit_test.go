package inspect

import (
	"encoding/json"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The upstream encoding puts the verdict first. Note numbers can qualify any
// verdict; they do not change it to partial. Check every bundled observation.
func TestCompatibilityBundledClientVerdicts(t *testing.T) {
	for _, feature := range canIEmailDataset.Data {
		for _, platforms := range feature.Stats {
			for _, versions := range platforms {
				for version, raw := range versions {
					var source string
					if err := json.Unmarshal(raw, &source); err != nil {
						t.Fatal(err)
					}
					fields := strings.Fields(source)
					want := map[string]string{"y": "yes", "a": "partial", "n": "no", "u": "unknown"}[fields[0]]
					got, note := compatibilitySupport(raw, feature.Notes)
					if want == "" || got != want {
						t.Fatalf("%s %s %s: got %q want %q", feature.Slug, version, raw, got, want)
					}
					for _, id := range fields[1:] {
						if value := feature.Notes[strings.TrimPrefix(id, "#")]; value != "" && (note == nil || !strings.Contains(*note, value)) {
							t.Fatalf("%s %s %s: missing note %q", feature.Slug, version, raw, id)
						}
					}
				}
			}
		}
	}
}

func TestCompatibilityReportedKnownDenominator(t *testing.T) {
	// Independently tallied from the pinned css-font-size matrix: 23 yes,
	// 14 partial, 15 unknown. No client in the matrix recorded a no.
	result := analyzeHTMLCompatibility([]byte(`<p style="font-size:12px">Hello</p>`))
	for _, warning := range result.Warnings {
		if warning.Slug != "css-font-size" {
			continue
		}
		unknown := 0
		for _, client := range warning.Clients {
			if client.Support == "unknown" {
				unknown++
			}
		}
		if unknown != 15 || len(warning.Clients) != 52 || math.Abs(warning.Score.Supported-100.0*23/37) > 1e-9 || math.Abs(warning.Score.Partial-100.0*14/37) > 1e-9 || warning.Score.Unsupported != 0 {
			t.Fatalf("unexpected client tally: unknown=%d clients=%d score=%+v", unknown, len(warning.Clients), warning.Score)
		}
		return
	}
	t.Fatal("font-size feature missing")
}

func TestCompatibilityDetectionFixtures(t *testing.T) {
	for _, tc := range []struct{ name, html, want, absent string }{
		{"stylesheet", `<style>@media screen and (min-width:30em) and (prefers-color-scheme: dark){.card:ONLY-CHILD:hover:not(:focus-visible)::before{ DISPLAY : GRID ! important; grid-template-columns:1fr 1fr; width:calc(100% - .5REM); background:radial-gradient(red,blue) }} @font-face{font-family:"a"; src:url(font.woff)}</style>`,
			"css-at-media css-at-media-prefers-color-scheme css-at-font-face css-pseudo-class-only-child css-pseudo-class-hover css-pseudo-class-not css-pseudo-class-focus-visible css-pseudo-element-before css-display-grid css-important css-grid-template css-unit-rem css-unit-calc css-radial-gradient", "css-pseudo-class-focus css-display-none"},
		{"inline values", `<p style="--card: 2; color:var(--color); padding-inline:2ch; margin-block:2px; block-size:max-content; columns:2; color:oklch(50% .1 20); scroll-snap-type:x mandatory; display:none!important">`,
			"css-variables css-padding-inline-block css-margin-inline-block css-block-inline-size css-intrinsic-size css-column-layout-properties css-modern-color css-scroll-snap css-display-none css-important css-unit-ch", "css-display-grid"},
		{"comments, strings and URL are data", `<style>/* @media(x){.a:hover { display: grid; }} */ p::after{content:"; display:flex; @supports (flex) {} :focus 9rem";background:url('https://example.test/:active/8vh')} </style><p style="--sample: 'display:none'; display:gridlock; bad: display:grid; /* width: 9vmin */">`,
			"css-pseudo-element-after css-background css-display css-variables", "css-display-grid css-display-flex css-display-none css-unit-rem css-unit-vh css-unit-vmin css-at-media css-at-supports css-pseudo-class-active css-pseudo-class-focus css-pseudo-class-hover css-width"},
		{"wrong type stylesheet and separate style blocks", `<style type="text/plain">p:hover{display:flex}</style><style>.a {display:</style><style>grid; }</style>`,
			"html-style", "css-display-grid css-display-flex css-pseudo-class-hover"},
		{"real and decoy images", `<img src="https://example.test/art.SVG?cache=1#x"><img src="hero.webp#top"><img src="https://example.test/asset?id=other.avif"><img src="data:image/svg+xml,%3Csvg%3E"><img src="data:image/png;base64,AAAA">`,
			"image-svg image-webp image-base64", "image-avif"},
		{"responsive formats", `<picture><source srcset="small.avif?rev=1 1x, large.avif 2x, ignored.webp nope"><img src="fallback.jpg" srcset="higher.svg#hero 2x"></picture>`,
			"html-srcset image-svg image-avif image-jpg", "image-webp"},
		{"animated PNG", `<img src="animation.apng"><img src="poster.png">`, "image-apng image-png", ""},
		{"static PNG does not imply animation", `<img src="image.png">`, "image-png", "image-apng"},
		{"fake image data", `<img src="data:image/svg+xml,%3Csvg%3E"><img src="https://example.test/asset?name=image.webp">`, "", "image-base64 image-webp"},
		{"attributes", `<table background="texture.png" height="20" width="300" cellpadding="3"><img srcset="hero.png 1x"><div style="margin:2px"></div></table>`,
			"html-background html-height html-width html-cellpadding html-srcset css-margin", "css-background css-height css-width css-padding"},
		{"attribute scope", `<div srcset="x" required background="x" width="20"></div><image></image><input type="checkbox">`,
			"html-input-checkbox", "html-width html-srcset html-required html-background html-object"},
		{"active and inert scripts", `<script type="application/ld+json">{}</script><script type="text/plain">Example</script><script type="application/json">{}</script>`, "", "html-script"},
		{"updated features", `<meta name="COLOR-SCHEME" content="light dark"><button commandfor="details" command="show-modal">Open</button><aside popover id="menu"></aside><button popovertarget="menu">Menu</button><img src="https://example.test/art.svg?v=3"><div style="inset:0"></div>`,
			"html-meta-color-scheme html-command-attribute html-popover image-svg css-inset", "html-svg"},
		{"feature decoys", `<meta name="theme-color" content="blue"><div commandfor="details" popovertarget="menu"></div><svg></svg><p style="top:0"></p>`,
			"html-svg css-left-right-top-bottom", "html-meta-color-scheme html-command-attribute html-popover image-svg css-inset"},
		{"logical borders", `<p style="border-block-start-color: red; border-inline-width:2px; border-inline:1px solid">`, "css-border-inline-block-longhand css-border-inline-block-individual css-border-inline-block", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := analyzeHTMLCompatibility([]byte(tc.html))
			got := map[string]int{}
			for _, w := range r.Warnings {
				got[w.Slug] = w.Occurrences
			}
			for _, k := range strings.Fields(tc.want) {
				if got[k] < 1 {
					t.Errorf("missing %s; got %v", k, got)
				}
			}
			for _, k := range strings.Fields(tc.absent) {
				if got[k] > 0 {
					t.Errorf("unexpected %s; got %v", k, got)
				}
			}
		})
	}
}

func TestCompatibilityMailpitScriptExample(t *testing.T) {
	r := analyzeHTMLCompatibility([]byte(`<p>Hello</p><script>/* test fixture */</script>`))
	if r.Nodes != 2 || r.Score.Unsupported != 50 || r.Score.Partial != 0 || r.Score.Supported != 50 {
		t.Fatalf("nodes=%d score=%+v", r.Nodes, r.Score)
	}
	for _, w := range r.Warnings {
		if w.Slug == "html-script" && w.Score.Unsupported == 100 && w.Occurrences == 1 && len(w.Clients) == 0 {
			return
		}
	}
	t.Fatal("missing synthetic script warning")
}

func TestCompatibilityRulesHaveEvidence(t *testing.T) {
	for slug := range compatibilityRules {
		if slug == "html-script" {
			continue // the explicit Mailpit exception
		}
		f, ok := caniEmailFeatures[slug]
		if !ok || len(f.Stats) == 0 || f.URL == "" {
			t.Errorf("no reference matrix for %s", slug)
		}
	}
	t.Logf("%d feature checks, %d upstream matrices", len(compatibilityRules), len(canIEmailDataset.Data))
}

// Check against the original authors' HTML rather than fixtures derived from
// Hoomail's rule mappings. Run with HOOMAIL_CANIEMAIL_SOURCE pointing to a
// local hteumeuleu/caniemail checkout matching the embedded data; no fetching.
func TestCompatibilityUpstreamCorpus(t *testing.T) {
	dir := os.Getenv("HOOMAIL_CANIEMAIL_SOURCE")
	if dir == "" {
		t.Skip("set HOOMAIL_CANIEMAIL_SOURCE to the matching Can I Email checkout")
	}
	var matrix struct {
		Data []struct {
			Slug    string
			TestURL string `json:"test_url"`
		}
	}
	if err := json.Unmarshal(canIEmailData, &matrix); err != nil {
		t.Fatal(err)
	}
	results := map[string]map[string]bool{}
	checked := 0
	for _, f := range matrix.Data {
		if !compatibilityRules[f.Slug] {
			continue
		}
		u, err := url.Parse(f.TestURL)
		if err != nil {
			t.Fatal(err)
		}
		page := filepath.Base(u.Path)
		switch f.Slug {
		case "css-flex-wrap":
			// The matrix points to css-box-model.html, which does not use flex-wrap.
			page = "css-flex-wrap.html"
		case "image-jpg":
			// images.html uses a service-specific .jpg&text=... path. The width/height
			// fixture includes an ordinary linked JPEG with an unambiguous extension.
			page = "css-width-height.html"
		case "image-apng", "html-command-attribute":
			// images.html's animated PNG uses .png; detecting motion would need
			// the remote bytes. command/commandfor has no linked test page.
			// Both have explicit positive and negative local fixtures above.
			continue
		default:
			if u.Hostname() != "www.caniemail.com" || u.Path != "/tests/"+page {
				t.Fatalf("%s: unexpected test URL %q", f.Slug, f.TestURL)
			}
		}
		if results[page] == nil {
			source, err := os.ReadFile(filepath.Join(dir, "tests", page))
			if err != nil {
				t.Fatal(err)
			}
			result := analyzeHTMLCompatibility(source)
			if result == nil || result.Truncated || result.WarningsTruncated {
				t.Fatalf("%s: incomplete fixture result", page)
			}
			results[page] = map[string]bool{}
			for _, w := range result.Warnings {
				results[page][w.Slug] = true
			}
		}
		if !results[page][f.Slug] {
			t.Errorf("%s not detected on %s", f.Slug, page)
		}
		checked++
	}
	t.Logf("%d upstream feature examples checked on %d distinct HTML pages", checked, len(results))
}
