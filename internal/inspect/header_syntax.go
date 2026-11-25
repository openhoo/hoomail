package inspect

import (
	"errors"
	"net/mail"
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	dateSyntax = regexp.MustCompile(`(?i)^(?:(Mon|Tue|Wed|Thu|Fri|Sat|Sun) *, *)?([0-9]{1,2} [A-Z]{3} [0-9]{2,} [0-9]{2}:[0-9]{2})(:[0-9]{2})? ([+-][0-9]{4}|[A-Z]{1,3})$`)
	// RFC 5322 §§3.2.3, 3.6.4, including internationalized atoms (RFC 6532).
	idAtom    = "[!#$%&'*+/=?^_`{|}~0-9A-Za-z\\-\\x{80}-\\x{10FFFF}]+"
	idDotAtom = idAtom + "(?:\\." + idAtom + ")*"
	idToken   = regexp.MustCompile("^<(?:" + idDotAtom + `|"(?:[^"\\\r\n]|\\[ -~])*")@(?:` + idDotAtom + `|\[[\x21-\x5a\x5e-\x7e\x{80}-\x{10ffff}]*\])>`)
)

// skipCFWS consumes leading folding whitespace and balanced, optionally nested
// comments. Quoted pairs inside a comment do not affect nesting.
func skipCFWS(s string) (string, bool) {
	for {
		s = strings.TrimLeft(s, " \t\r\n")
		if s == "" || s[0] != '(' {
			return s, true
		}
		depth, i := 1, 1
		for ; i < len(s) && depth != 0; i++ {
			switch s[i] {
			case '\\':
				i++
			case '(':
				depth++
			case ')':
				depth--
			}
		}
		if depth != 0 {
			return "", false
		}
		s = s[i:]
	}
}

func validMessageDate(value string) bool {
	// Strip RFC comments at token boundaries before validating the entire field.
	var fields strings.Builder
	for value != "" {
		if value[0] == '(' {
			var ok bool
			value, ok = skipCFWS(value)
			if !ok {
				return false
			}
			fields.WriteByte(' ')
		} else {
			fields.WriteByte(value[0])
			value = value[1:]
		}
	}
	parts := dateSyntax.FindStringSubmatch(strings.Join(strings.Fields(fields.String()), " "))
	if parts == nil {
		return false
	}
	zone := parts[4]
	if len(zone) == 5 && zone[3:] > "59" {
		return false
	}
	// net/mail delegates to time.Parse, which does not represent a leap second.
	seconds := parts[3]
	if seconds == ":60" {
		seconds = ":59"
	}
	parsed, err := mail.ParseDate(parts[2] + seconds + " " + zone)
	return err == nil && parsed.Year() >= 1900 && (parts[1] == "" || strings.EqualFold(parts[1], parsed.Weekday().String()[:3]))
}

func validMessageID(value string) bool     { return countMessageIDs(value) == 1 }
func validMessageIDList(value string) bool { return countMessageIDs(value) > 0 }

func countMessageIDs(value string) int {
	if !utf8.ValidString(value) {
		return -1
	}
	count := 0
	for {
		rest, ok := skipCFWS(value)
		if !ok {
			return -1
		}
		if rest == "" {
			return count
		}
		match := idToken.FindStringIndex(rest)
		if match == nil {
			return -1
		}
		count++
		value = rest[match[1]:]
	}
}

func parseAddressField(value string, allowEmpty bool) (int, bool, error) {
	if rest, ok := skipCFWS(value); ok && rest == "" && allowEmpty {
		return 0, false, nil
	}
	addresses, err := mail.ParseAddressList(value)
	if err != nil {
		return 0, false, err
	}
	// net/mail parses and flattens groups. A semicolon outside a comment,
	// quoted string or domain literal identifies an actual group delimiter.
	groups := addressDelimiter(value, ';')
	if !groups && len(addresses) == 0 {
		return 0, false, errors.New("empty address list")
	}
	return len(addresses), groups, nil
}

func addressDelimiter(s string, delimiter byte) bool {
	quoted, literal, group := false, false, false
	for i := 0; i < len(s); i++ {
		switch {
		case (quoted || literal) && s[i] == '\\':
			i++
		case !literal && s[i] == '"':
			quoted = !quoted
		case quoted:
		case s[i] == '[':
			literal = true
		case s[i] == ']':
			literal = false
		case literal:
		case s[i] == '(':
			rest, ok := skipCFWS(s[i:])
			if !ok {
				return false
			}
			i = len(s) - len(rest) - 1
		case s[i] == ':':
			group = true
		case s[i] == ';':
			if delimiter == ';' {
				return true
			}
			group = false
		case !group && s[i] == delimiter:
			return true
		}
	}
	return false
}

func singleAddress(s string) bool { return !addressDelimiter(s, ',') }

func sameMailbox(a, b string) bool {
	left, e1 := mail.ParseAddress(a)
	right, e2 := mail.ParseAddress(b)
	if e1 != nil || e2 != nil {
		return false
	}
	li, ri := strings.LastIndexByte(left.Address, '@'), strings.LastIndexByte(right.Address, '@')
	return li >= 0 && ri >= 0 && left.Address[:li] == right.Address[:ri] && strings.EqualFold(left.Address[li+1:], right.Address[ri+1:])
}

func hasMIMEFields(fields []headerOccurrence) bool {
	for _, f := range fields {
		switch strings.ToLower(f.name) {
		case "mime-version", "content-type", "content-transfer-encoding", "content-id", "content-description", "content-disposition":
			return true
		}
	}
	return false
}

func validMIMEVersion(s string) bool {
	s, ok := skipCFWS(s)
	// MIME-Version is 1*DIGIT "." 1*DIGIT: comments may surround tokens.
	for _, token := range []string{"1", ".", "0"} {
		if !ok || !strings.HasPrefix(s, token) {
			return false
		}
		s, ok = skipCFWS(s[len(token):])
	}
	return ok && s == ""
}

func validMediaType(s string) bool {
	typ, sub, ok := strings.Cut(s, "/")
	return ok && tokenRE.MatchString(typ) && tokenRE.MatchString(sub)
}

// trimProseURL handles common surrounding prose without removing closing
// parentheses in names or the brackets around an IPv6 address.
func trimProseURL(s string) string {
	extraParens := strings.Count(s, ")") - strings.Count(s, "(")
	extraBrackets := strings.Count(s, "]") - strings.Count(s, "[")
	for len(s) > 0 {
		switch s[len(s)-1] {
		case '.', ',', '!', ';', ':', '\'':
		case ')':
			if extraParens <= 0 {
				return s
			}
			extraParens--
		case ']':
			if extraBrackets <= 0 {
				return s
			}
			extraBrackets--
		default:
			return s
		}
		s = s[:len(s)-1]
	}
	return s
}
