package inspect

import (
	"net/url"
	"path"
	"strings"
)

// The initial feature selection derives from Mailpit internal/htmlcheck at
// b859b0d986d66bbf1d7d9533317faf867afa1ed1 (MIT). Keys below are actual
// HTML/CSS constructs; values are feature IDs in the bundled Can I Email data.
func featureNames(names, prefix string) map[string]string {
	out := map[string]string{}
	for _, name := range strings.Fields(names) {
		out[name] = prefix + name
	}
	return out
}

var cssProperties = func() map[string]string {
	m := featureNames(`accent-color align-items animation aspect-ratio backdrop-filter background-blend-mode background-clip
	background-color background-image background-origin background-position background-repeat background-size background
	border-collapse border-image border-radius border-spacing border box-shadow box-sizing caption-side clear clip-path
	color-scheme column-count cursor direction display empty-cells filter flex-direction flex-wrap float font-kerning font-size-adjust
	font-size font-stretch font-weight font gap grid-template height hyphenate-character hyphenate-limit-chars hyphens inline-size inset
	justify-content letter-spacing line-height list-style-image list-style-position list-style-type list-style margin mask-image
	max-block-size max-height max-inline-size max-width min-block-size min-height min-inline-size min-width mix-blend-mode object-fit
	object-position opacity orphans outline-offset outline overflow-wrap overflow padding position resize shape-margin shape-outside
	tab-size table-layout text-align-last text-align text-decoration-color text-decoration-line text-decoration-skip-ink text-decoration-style
	text-decoration-thickness text-decoration text-emphasis-position text-emphasis text-indent text-justify text-orientation text-overflow
	text-shadow text-transform text-underline-offset text-underline-position text-wrap transform transition user-select vertical-align
	visibility white-space-collapse white-space widows width word-break word-spacing word-wrap writing-mode z-index`, "css-")
	for slug, properties := range map[string]string{
		"css-block-inline-size":              "block-size",
		"css-border-inline-block":            "border-inline border-block",
		"css-border-inline-block-individual": "border-inline-start border-inline-end border-block-start border-block-end border-inline-color border-inline-width border-inline-style border-block-color border-block-width border-block-style",
		"css-border-inline-block-longhand":   "border-inline-start-color border-inline-end-color border-block-start-color border-block-end-color border-inline-start-style border-inline-end-style border-block-start-style border-block-end-style border-inline-start-width border-inline-end-width border-block-start-width border-block-end-width",
		"css-border-radius-logical":          "border-start-start-radius border-start-end-radius border-end-start-radius border-end-end-radius",
		"css-column-layout-properties":       "columns column-width column-fill column-gap column-rule column-rule-color column-rule-style column-rule-width column-span",
		"css-grid-template":                  "grid-template-rows grid-template-columns grid-template-areas",
		"css-left-right-top-bottom":          "left right top bottom",
		"css-margin-inline-block":            "margin-inline margin-block",
		"css-margin-inline-start-end":        "margin-inline-start margin-inline-end",
		"css-margin-block-start-end":         "margin-block-start margin-block-end",
		"css-padding-inline-block":           "padding-inline padding-block",
		"css-padding-inline-start-end":       "padding-inline-start padding-inline-end",
		"css-padding-block-start-end":        "padding-block-start padding-block-end",
		"css-scroll-snap":                    "scroll-snap-type scroll-snap-align scroll-snap-stop",
	} {
		for _, property := range strings.Fields(properties) {
			m[property] = slug
		}
	}
	return m
}()

var cssSizeProperties = map[string]bool{"height": true, "width": true, "min-height": true, "min-width": true, "max-height": true, "max-width": true, "block-size": true, "inline-size": true, "max-block-size": true, "max-inline-size": true, "min-block-size": true, "min-inline-size": true}
var cssAtRules = featureNames("font-face import keyframes media supports", "css-at-")
var cssMediaFeatures = map[string]string{
	"hover": "css-at-media-hover", "any-hover": "css-at-media-hover",
	"orientation": "css-at-media-orientation", "prefers-color-scheme": "css-at-media-prefers-color-scheme",
	"prefers-reduced-motion":     "css-at-media-prefers-reduced-motion",
	"-webkit-device-pixel-ratio": "css-at-media-device-pixel-ratio", "-webkit-min-device-pixel-ratio": "css-at-media-device-pixel-ratio", "-webkit-max-device-pixel-ratio": "css-at-media-device-pixel-ratio",
}
var cssPseudoClasses = featureNames("active checked default first-child first-of-type focus focus-visible focus-within has hover lang last-child last-of-type link not nth-child nth-last-child nth-last-of-type nth-of-type only-child only-of-type target visited", "css-pseudo-class-")
var cssPseudoElements = featureNames("after before first-letter first-line marker placeholder", "css-pseudo-element-")
var cssUnits = featureNames("ch rem vh vmax vmin vw", "css-unit-")
var cssDisplayValues = map[string]string{"flex": "css-display-flex", "inline-flex": "css-display-flex", "grid": "css-display-grid", "inline-grid": "css-display-grid", "none": "css-display-none"}
var cssFunctions = map[string]string{"calc": "css-unit-calc", "rgb": "css-rgb", "rgba": "css-rgba", "var": "css-variables", "lch": "css-modern-color", "oklch": "css-modern-color", "lab": "css-modern-color", "oklab": "css-modern-color", "conic-gradient": "css-conic-gradient", "radial-gradient": "css-radial-gradient", "linear-gradient": "css-linear-gradient", "clamp": "css-function-clamp", "min": "css-function-min", "max": "css-function-max", "light-dark": "css-function-light-dark"}

var htmlTags = func() map[string]string {
	m := featureNames("body object link hr dialog picture svg progress meter audio form base video select textarea style", "html-")
	for _, tag := range strings.Fields("article aside details figcaption figure footer header main mark nav section summary time") {
		m[tag] = "html-semantics"
	}
	return m
}()
var imageExtensions = func() map[string]string {
	m := featureNames("apng avif bmp gif hdr heif ico jpg mp4 png svg tiff webp", "image-")
	m["jpeg"], m["tif"], m["heic"] = "image-jpg", "image-tiff", "image-heif"
	return m
}()

func compatibilityImageFormat(src string) string {
	src = strings.TrimSpace(src)
	if len(src) >= 5 && strings.EqualFold(src[:5], "data:") {
		metadata, _, ok := strings.Cut(src[5:], ",")
		if ok && strings.HasPrefix(strings.ToLower(metadata), "image/") && strings.HasSuffix(strings.ToLower(metadata), ";base64") {
			return "image-base64"
		}
		return ""
	}
	u, err := url.Parse(src)
	if err != nil || u.Opaque != "" {
		return ""
	}
	return imageExtensions[strings.TrimPrefix(strings.ToLower(path.Ext(u.Path)), ".")]
}

func inspectCompatibilityTag(name string, attrs map[string]string, counts map[string]int) {
	if slug := htmlTags[name]; slug != "" {
		counts[slug]++
	}
	has := func(attribute string) bool { _, ok := attrs[attribute]; return ok }
	if has("popover") || ((name == "button" || (name == "input" && strings.EqualFold(attrs["type"], "button"))) && (has("popovertarget") || has("popovertargetaction"))) {
		counts["html-popover"]++
	}
	if name == "button" && (has("command") || has("commandfor")) {
		counts["html-command-attribute"]++
	}
	switch name {
	case "meta":
		if strings.EqualFold(attrs["name"], "color-scheme") {
			counts["html-meta-color-scheme"]++
		}
	case "script":
		if scriptIsExecutable(attrs["type"]) {
			counts["html-script"]++
		}
	case "input":
		switch strings.ToLower(attrs["type"]) {
		case "radio", "checkbox", "hidden", "submit", "reset", "text":
			counts["html-input-"+strings.ToLower(attrs["type"])]++
		case "":
			counts["html-input-text"]++
		}
	case "button":
		switch strings.ToLower(attrs["type"]) {
		case "submit":
			counts["html-button-submit"]++
		case "reset":
			counts["html-button-reset"]++
		}
	case "a":
		if strings.HasPrefix(strings.TrimSpace(attrs["href"]), "#") {
			counts["html-anchor-links"]++
		}
	case "map":
		counts["html-image-maps"]++
	case "img":
		if has("usemap") {
			counts["html-image-maps"]++
		}
	}
	if name == "img" || name == "source" {
		formats := map[string]bool{}
		if name == "img" {
			formats[compatibilityImageFormat(attrs["src"])] = true
		}
		if has("srcset") {
			counts["html-srcset"]++
			walkImageCandidates(attrs["srcset"], func(url string) { formats[compatibilityImageFormat(url)] = true })
		}
		for slug := range formats {
			if slug != "" {
				counts[slug]++
			}
		}
	}
	if (name == "input" || name == "select" || name == "textarea") && has("required") {
		counts["html-required"]++
	}
	if (name == "body" || name == "table" || name == "td" || name == "th") && has("background") {
		counts["html-background"]++
	}
	switch name {
	case "img", "table", "td", "th", "video", "canvas", "iframe", "embed", "object", "input":
		for _, dimension := range []string{"height", "width"} {
			if has(dimension) {
				counts["html-"+dimension]++
			}
		}
	}
	if name == "table" {
		for _, spacing := range []string{"cellspacing", "cellpadding"} {
			if has(spacing) {
				counts["html-"+spacing]++
			}
		}
	}
}

// Script elements can also contain non-executable data such as JSON-LD.
func scriptIsExecutable(kind string) bool {
	kind = strings.TrimSpace(strings.ToLower(kind))
	switch kind {
	case "", "module", "importmap", "speculationrules":
		return true
	}
	kind, _, _ = strings.Cut(kind, ";")
	switch strings.TrimSpace(kind) {
	case "application/ecmascript", "application/javascript", "application/x-ecmascript", "application/x-javascript",
		"text/ecmascript", "text/javascript", "text/javascript1.0", "text/javascript1.1", "text/javascript1.2", "text/javascript1.3", "text/javascript1.4", "text/javascript1.5", "text/jscript", "text/livescript", "text/x-ecmascript", "text/x-javascript":
		return true
	}
	return false
}

func compatibilityRuleIDs() map[string]bool {
	ids := map[string]bool{}
	for _, aliases := range []map[string]string{cssProperties, cssAtRules, cssMediaFeatures, cssPseudoClasses, cssPseudoElements, cssDisplayValues, cssFunctions, cssUnits, htmlTags, imageExtensions} {
		for _, slug := range aliases {
			ids[slug] = true
		}
	}
	for _, slug := range strings.Fields("css-intrinsic-size css-important css-unit-initial html-script html-popover html-command-attribute html-meta-color-scheme html-input-radio html-input-checkbox html-input-hidden html-input-submit html-input-reset html-input-text html-button-submit html-button-reset html-anchor-links html-image-maps html-srcset html-required html-background html-width html-height html-cellspacing html-cellpadding image-base64") {
		ids[slug] = true
	}
	return ids
}
