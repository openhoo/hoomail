package inspect

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
)

const svgNamespace = "http://www.w3.org/2000/svg"

var (
	svgFragmentURLPattern = regexp.MustCompile(`(?i)^url\(\s*['"]?#[A-Za-z_][A-Za-z0-9_.:-]*['"]?\s*\)$`)
	svgFragmentPattern    = regexp.MustCompile(`^#[A-Za-z_][A-Za-z0-9_.:-]*$`)
	svgElements           = stringSet(
		"svg", "g", "defs", "symbol", "use", "path", "rect", "circle", "ellipse", "line", "polyline", "polygon",
		"text", "tspan", "textPath", "title", "desc", "clipPath", "mask", "pattern", "linearGradient", "radialGradient",
		"stop", "marker", "filter", "feBlend", "feColorMatrix", "feComponentTransfer", "feComposite", "feConvolveMatrix",
		"feDiffuseLighting", "feDisplacementMap", "feDistantLight", "feDropShadow", "feFlood", "feFuncA", "feFuncB",
		"feFuncG", "feFuncR", "feGaussianBlur", "feMerge", "feMergeNode", "feMorphology", "feOffset", "fePointLight",
		"feSpecularLighting", "feSpotLight", "feTile", "feTurbulence",
	)
	svgAttributes = stringSet(
		"id", "class", "lang", "dir", "tabindex", "focusable", "role", "aria-label", "aria-labelledby", "aria-describedby",
		"x", "y", "x1", "y1", "x2", "y2", "cx", "cy", "r", "rx", "ry", "width", "height", "viewBox", "preserveAspectRatio",
		"d", "pathLength", "points", "transform", "gradientTransform", "patternTransform", "markerWidth", "markerHeight",
		"markerUnits", "refX", "refY", "orient", "patternUnits", "patternContentUnits", "gradientUnits", "spreadMethod",
		"offset", "fill", "fill-opacity", "fill-rule", "stroke", "stroke-width", "stroke-opacity", "stroke-linecap",
		"stroke-linejoin", "stroke-miterlimit", "stroke-dasharray", "stroke-dashoffset", "opacity", "color", "color-interpolation",
		"color-interpolation-filters", "color-rendering", "shape-rendering", "text-rendering", "image-rendering", "visibility",
		"display", "overflow", "clip", "clip-path", "clip-rule", "mask", "filter", "marker-start", "marker-mid", "marker-end",
		"font-family", "font-size", "font-style", "font-weight", "text-anchor", "dominant-baseline", "alignment-baseline",
		"baseline-shift", "letter-spacing", "word-spacing", "text-decoration", "paint-order", "vector-effect", "stop-color",
		"stop-opacity", "flood-color", "flood-opacity", "lighting-color", "unicode-bidi", "writing-mode", "direction",
		"href", "result", "in", "in2", "mode", "type", "values", "operator", "k1", "k2", "k3", "k4", "order",
		"kernelMatrix", "divisor", "bias", "targetX", "targetY", "edgeMode", "kernelUnitLength", "preserveAlpha", "scale",
		"xChannelSelector", "yChannelSelector", "stdDeviation", "radius", "baseFrequency", "numOctaves", "seed", "stitchTiles",
		"surfaceScale", "diffuseConstant", "specularConstant", "specularExponent", "limitingConeAngle", "azimuth", "elevation",
		"pointsAtX", "pointsAtY", "pointsAtZ", "z", "dx", "dy", "rotate", "lengthAdjust", "textLength",
	)
)

// SanitizeSVG returns a static-only SVG document. It fails closed on malformed
// XML, non-SVG roots, directives, processing instructions, and documents with no
// root element. Elements and attributes outside the allowlist are discarded.
func SanitizeSVG(raw []byte) ([]byte, error) {
	decoder := xml.NewDecoder(bytes.NewReader(raw))
	decoder.Strict = true
	var output bytes.Buffer
	encoder := xml.NewEncoder(&output)
	depth := 0
	droppedDepth := 0
	seenRoot := false
	closedRoot := false

	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse SVG: %w", err)
		}
		switch value := token.(type) {
		case xml.Directive, xml.ProcInst:
			return nil, errors.New("SVG directives and processing instructions are not allowed")
		case xml.StartElement:
			depth++
			if droppedDepth != 0 {
				continue
			}
			if closedRoot || !safeSVGElement(value.Name, depth, seenRoot) {
				if depth == 1 {
					return nil, errors.New("document root is not an allowed SVG element")
				}
				droppedDepth = depth
				continue
			}
			if depth == 1 {
				seenRoot = true
			}
			value.Attr = sanitizeSVGAttributes(value.Attr)
			if err := encoder.EncodeToken(value); err != nil {
				return nil, fmt.Errorf("encode SVG: %w", err)
			}
		case xml.EndElement:
			if droppedDepth != 0 {
				if depth == droppedDepth {
					droppedDepth = 0
				}
				depth--
				continue
			}
			if depth == 0 {
				return nil, errors.New("unexpected SVG closing element")
			}
			if err := encoder.EncodeToken(value); err != nil {
				return nil, fmt.Errorf("encode SVG: %w", err)
			}
			depth--
			if depth == 0 {
				closedRoot = true
			}
		case xml.CharData:
			if droppedDepth == 0 && depth > 0 {
				if err := encoder.EncodeToken(value); err != nil {
					return nil, fmt.Errorf("encode SVG: %w", err)
				}
			}
		case xml.Comment:
			// Comments are not needed for static rendering and can conceal payloads.
		}
	}
	if !seenRoot || !closedRoot || depth != 0 {
		return nil, errors.New("incomplete SVG document")
	}
	if err := encoder.Flush(); err != nil {
		return nil, fmt.Errorf("encode SVG: %w", err)
	}
	return output.Bytes(), nil
}

func safeSVGElement(name xml.Name, depth int, seenRoot bool) bool {
	if name.Space != "" && name.Space != svgNamespace {
		return false
	}
	if depth == 1 {
		return !seenRoot && name.Local == "svg"
	}
	return svgElements[name.Local]
}

func sanitizeSVGAttributes(attributes []xml.Attr) []xml.Attr {
	clean := make([]xml.Attr, 0, len(attributes))
	for _, attribute := range attributes {
		name := attribute.Name.Local
		lowerName := strings.ToLower(name)
		if strings.HasPrefix(lowerName, "on") || lowerName == "style" || !svgAttributes[name] {
			continue
		}
		if attribute.Name.Space != "" && !(attribute.Name.Space == "http://www.w3.org/1999/xlink" && name == "href") {
			continue
		}
		value := strings.TrimSpace(attribute.Value)
		if value == "" || strings.ContainsAny(value, "\x00\r\n") {
			continue
		}
		if name == "href" && !svgFragmentPattern.MatchString(value) {
			continue
		}
		if strings.Contains(strings.ToLower(value), "url(") && !svgFragmentURLPattern.MatchString(value) {
			continue
		}
		attribute.Value = value
		clean = append(clean, attribute)
	}
	return clean
}

func stringSet(values ...string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[value] = true
	}
	return set
}
