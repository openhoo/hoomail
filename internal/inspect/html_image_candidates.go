package inspect

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

const imageWhitespace = "\t\n\f\r "

// Follow HTML's srcset splitting and descriptor rules. URLs can themselves
// contain commas (notably data:); only tokenize descriptors after whitespace.
// https://html.spec.whatwg.org/multipage/images.html#parse-a-srcset-attribute
func walkImageCandidates(list string, visit func(string)) {
	for {
		list = strings.TrimLeft(list, imageWhitespace+",")
		if list == "" {
			return
		}
		i := strings.IndexAny(list, imageWhitespace)
		if i < 0 {
			i = len(list)
		}
		src := list[:i]
		list = list[i:]
		if strings.HasSuffix(src, ",") {
			visit(strings.TrimRight(src, ","))
			continue
		}
		// Inside a parenthesized descriptor, even a comma is part of
		// the descriptor. Unknown descriptors invalidate only this candidate.
		end := 0
		paren := false
		for ; end < len(list); end++ {
			switch list[end] {
			case '(':
				paren = true
			case ')':
				paren = false
			case ',':
				if !paren {
					goto candidate
				}
			}
		}
	candidate:
		if validImageDescriptors(list[:end]) {
			visit(src)
		}
		list = list[end:]
	}
}

var imageDensityRE = regexp.MustCompile(`^-?(?:[0-9]+(?:\.[0-9]+)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$`)

func validImageDescriptors(value string) bool {
	width, height, density := false, false, false
	for value != "" {
		value = strings.TrimLeft(value, imageWhitespace)
		if value == "" {
			break
		}
		end := strings.IndexAny(value, imageWhitespace)
		if end < 0 {
			end = len(value)
		}
		d := value[:end]
		value = value[end:]
		if len(d) < 2 {
			return false
		}
		amount := d[:len(d)-1]
		switch d[len(d)-1] {
		case 'w', 'h':
			if density || strings.Trim(amount, "0123456789") != "" || strings.Trim(amount, "0") == "" {
				return false
			}
			if d[len(d)-1] == 'w' {
				if width {
					return false
				}
				width = true
			} else {
				if height {
					return false
				}
				height = true
			}
		case 'x':
			if width || height || density || !imageDensityRE.MatchString(amount) {
				return false
			}
			n, err := strconv.ParseFloat(amount, 64)
			if err != nil || n < 0 || math.IsInf(n, 0) {
				return false
			}
			density = true
		default:
			return false
		}
	}
	return !height || width
}
