package inspect

import (
	"regexp"
	"strings"

	"golang.org/x/net/idna"
)

var (
	tagNameRE     = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]*$`)
	signDomainRE  = regexp.MustCompile(`(?i)^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)*$`)
	algorithmRE   = regexp.MustCompile(`(?i)^[a-z][a-z0-9]*-[a-z][a-z0-9]*$`)
	authKeywordRE = regexp.MustCompile(`(?i)^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`)
)

func validSigningDomain(s string, requireDot bool) bool {
	if !isASCII(s) {
		ascii, err := idna.Lookup.ToASCII(s)
		if err != nil {
			return false
		}
		s = ascii
	}
	return (!requireDot || strings.Contains(s, ".")) && signDomainRE.MatchString(s)
}

func digitsOnly(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// Split RFC 8601's structured tokens. Comments count as whitespace, and quoted
// strings (including semicolons inside reasons) remain a single token.
func authTokens(s string) ([]string, bool) {
	var tokens []string
	for s != "" {
		var ok bool
		s, ok = skipCFWS(s)
		if !ok {
			return nil, false
		}
		if s == "" {
			break
		}
		end := 1
		switch s[0] {
		case ';', '=', '/':
		case '"':
			for end < len(s) && s[end] != '"' {
				if s[end] == '\\' {
					end++
				}
				end++
			}
			if end >= len(s) {
				return nil, false
			}
			end++
		default:
			if s[0] == ')' {
				return nil, false
			}
			for end < len(s) && !strings.ContainsRune(" \t\r\n();=/\"", rune(s[end])) {
				end++
			}
		}
		tokens = append(tokens, s[:end])
		s = s[end:]
	}
	return tokens, true
}

func parseAuthenticationResults(s string) (string, bool) {
	tokens, ok := authTokens(s)
	if !ok || len(tokens) < 3 {
		return "", false
	}
	authserv := tokens[0]
	if !authValue(authserv) {
		return "", false
	}
	tokens = tokens[1:]
	if digitsOnly(tokens[0]) {
		tokens = tokens[1:]
	}
	if len(tokens) == 2 && tokens[0] == ";" && strings.EqualFold(tokens[1], "none") {
		return authserv, true
	}
	for len(tokens) > 0 {
		if len(tokens) < 4 || tokens[0] != ";" || !authKeywordRE.MatchString(tokens[1]) {
			return "", false
		}
		tokens = tokens[2:]
		if tokens[0] == "/" {
			if len(tokens) < 4 || !digitsOnly(tokens[1]) {
				return "", false
			}
			tokens = tokens[2:]
		}
		if tokens[0] != "=" || !authKeywordRE.MatchString(tokens[1]) {
			return "", false
		}
		tokens = tokens[2:]
		first := true
		for len(tokens) > 0 && tokens[0] != ";" {
			// Optional CFWS also surrounds the "." in a property specification.
			name := tokens[0]
			tokens = tokens[1:]
			if len(tokens) > 0 && tokens[0] == "." {
				name += "."
				tokens = tokens[1:]
			}
			if strings.HasSuffix(name, ".") && len(tokens) > 0 {
				name += tokens[0]
				tokens = tokens[1:]
			}
			if !strings.Contains(name, ".") && len(tokens) > 0 && strings.HasPrefix(tokens[0], ".") {
				name += tokens[0]
				tokens = tokens[1:]
			}
			typ, prop, dotted := strings.Cut(name, ".")
			reason := strings.EqualFold(name, "reason")
			if !(first && reason) && !(dotted && authKeywordRE.MatchString(typ) && authKeywordRE.MatchString(prop)) {
				return "", false
			}
			if len(tokens) < 2 || tokens[0] != "=" {
				return "", false
			}
			value := tokens[1]
			tokens = tokens[2:]
			// A quoted local-part can be followed by @domain without a separator.
			if !reason && strings.HasPrefix(value, "\"") && len(tokens) > 0 && strings.HasPrefix(tokens[0], "@") {
				if !signDomainRE.MatchString(tokens[0][1:]) {
					return "", false
				}
				tokens = tokens[1:]
			}
			if !authValue(value) {
				if reason {
					return "", false
				}
				local, domain, found := strings.Cut(value, "@")
				if !found || !signDomainRE.MatchString(domain) || local != "" && !authAtomRE.MatchString(local) {
					return "", false
				}
			}
			first = false
		}
	}
	return authserv, true
}

var (
	authAtomRE  = regexp.MustCompile("^" + idDotAtom + "$")
	mimeTokenRE = regexp.MustCompile("^[^\\x00-\\x20\\x7f-\\xff()<>@,;:\\\\\"/\\[\\]?=]+$")
)

func authValue(s string) bool {
	return len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' || isASCII(s) && mimeTokenRE.MatchString(s)
}
