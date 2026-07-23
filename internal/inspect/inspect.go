package inspect

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"
)

type MimeNode struct {
	ContentType string     `json:"contentType"`
	Charset     *string    `json:"charset"`
	Encoding    *string    `json:"encoding"`
	Disposition *string    `json:"disposition"`
	Filename    *string    `json:"filename"`
	Size        int        `json:"size"`
	Children    []MimeNode `json:"children"`
}

type ExtractedLink struct {
	Href string `json:"href"`
	Text string `json:"text"`
	Kind string `json:"kind"`
}

type HeaderCheck struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

type HeaderCheckInput struct {
	Headers map[string]string `json:"headers"`
	HTML    *string           `json:"html"`
	Links   []ExtractedLink   `json:"links"`
	Size    int               `json:"size"`
}

var (
	scriptPairPattern     = regexp.MustCompile(`(?is)<script[\s\S]*?</script\s*>`)
	scriptTagPattern      = regexp.MustCompile(`(?is)<script[^>]*\/?>`)
	iframePairPattern     = regexp.MustCompile(`(?is)<iframe[\s\S]*?</iframe\s*>`)
	dangerousPairPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?is)<object[\s\S]*?</object\s*>`),
		regexp.MustCompile(`(?is)<embed[\s\S]*?</embed\s*>`),
		regexp.MustCompile(`(?is)<applet[\s\S]*?</applet\s*>`),
		regexp.MustCompile(`(?is)<form[\s\S]*?</form\s*>`),
	}
	dangerousTagPattern     = regexp.MustCompile(`(?is)<(object|embed|applet|form|base|meta)[^>]*\/?>`)
	doubleEventPattern      = regexp.MustCompile(`(?i)\son[a-z]+\s*=\s*"[^"]*"`)
	singleEventPattern      = regexp.MustCompile(`(?i)\son[a-z]+\s*=\s*'[^']*'`)
	unquotedEventPattern    = regexp.MustCompile(`(?i)\son[a-z]+\s*=\s*[^\s>]+`)
	doubleDangerousURL      = regexp.MustCompile(`(?i)\s(href|src|action|formaction)\s*=\s*"\s*(javascript|vbscript|data:text/html)[^"'\s>]*"`)
	singleDangerousURL      = regexp.MustCompile(`(?i)\s(href|src|action|formaction)\s*=\s*'\s*(javascript|vbscript|data:text/html)[^"'\s>]*'`)
	unquotedDangerousURL    = regexp.MustCompile(`(?i)\s(href|src|action|formaction)\s*=\s*(javascript|vbscript|data:text/html)[^"'\s>]*`)
	cidPattern              = regexp.MustCompile(`(?i)(["'(])cid:([^"')\s>]+)(["')])`)
	unfoldPattern           = regexp.MustCompile(`\r?\n[ \t]+`)
	leadingLineBreakPattern = regexp.MustCompile(`^\r?\n`)
	anchorPattern           = regexp.MustCompile(`(?is)<a\b[^>]*href\s*=\s*["']([^"']+)["'][^>]*>([\s\S]*?)</a>`)
	tagPattern              = regexp.MustCompile(`(?s)<[^>]+>`)
	spacePattern            = regexp.MustCompile(`\s+`)
	imagePattern            = regexp.MustCompile(`(?is)<img\b[^>]*>`)
	srcPattern              = regexp.MustCompile(`(?i)src\s*=\s*["']([^"']+)["']`)
	widthPattern            = regexp.MustCompile(`(?i)width\s*=\s*["']?([0-9]+)`)
	heightPattern           = regexp.MustCompile(`(?i)height\s*=\s*["']?([0-9]+)`)
	hiddenStylePattern      = regexp.MustCompile(`(?i)display\s*:\s*none|visibility\s*:\s*hidden`)
	altPattern              = regexp.MustCompile(`(?i)alt\s*=\s*["']([^"']*)["']`)
	altPresencePattern      = regexp.MustCompile(`(?i)\balt\s*=`)
	textURLPattern          = regexp.MustCompile(`(?i)https?://[^\s<>"')\]]+`)
	spfPattern              = regexp.MustCompile(`(?i)spf=(\w+)`)
	dkimPattern             = regexp.MustCompile(`(?i)dkim=(\w+)`)
)

func SanitizeEmailHTML(html string) string {
	out := scriptPairPattern.ReplaceAllString(html, "")
	out = scriptTagPattern.ReplaceAllString(out, "")
	out = iframePairPattern.ReplaceAllString(out, "")
	for _, pattern := range dangerousPairPatterns {
		out = pattern.ReplaceAllString(out, "")
	}
	out = dangerousTagPattern.ReplaceAllString(out, "")
	out = doubleEventPattern.ReplaceAllString(out, "")
	out = singleEventPattern.ReplaceAllString(out, "")
	out = unquotedEventPattern.ReplaceAllString(out, "")
	out = doubleDangerousURL.ReplaceAllString(out, ` $1="#"`)
	out = singleDangerousURL.ReplaceAllString(out, ` $1='#'`)
	out = unquotedDangerousURL.ReplaceAllString(out, ` $1=#`)
	return out
}

func RewriteCIDURLs(html string, cidMap map[string]int64) string {
	return cidPattern.ReplaceAllStringFunc(html, func(match string) string {
		parts := cidPattern.FindStringSubmatch(match)
		cid := parts[2]
		normalized := strings.TrimSuffix(strings.TrimPrefix(cid, "<"), ">")
		attachmentID, ok := cidMap[normalized]
		if !ok {
			attachmentID, ok = cidMap[cid]
		}
		if !ok {
			return match
		}
		return parts[1] + "/api/attachments/" + strconv.FormatInt(attachmentID, 10) + parts[3]
	})
}

func BuildMIMETree(raw string) MimeNode {
	headers, body := splitHeadersBody(raw)
	contentTypeHeader := headerValue(headers, "Content-Type")
	contentType := "text/plain"
	if contentTypeHeader != nil {
		contentType = strings.TrimSpace(strings.SplitN(*contentTypeHeader, ";", 2)[0])
	}
	contentType = strings.ToLower(contentType)
	encoding := lowerPointer(headerValue(headers, "Content-Transfer-Encoding"))
	dispositionHeader := headerValue(headers, "Content-Disposition")
	var disposition *string
	if dispositionHeader != nil {
		value := strings.ToLower(strings.TrimSpace(strings.SplitN(*dispositionHeader, ";", 2)[0]))
		disposition = &value
	}
	filename := headerParam(dispositionHeader, "filename")
	if filename == nil {
		filename = headerParam(contentTypeHeader, "name")
	}
	node := MimeNode{
		ContentType: contentType,
		Charset:     headerParam(contentTypeHeader, "charset"),
		Encoding:    encoding,
		Disposition: disposition,
		Filename:    filename,
		Size:        javascriptLength(body),
		Children:    make([]MimeNode, 0),
	}
	if strings.HasPrefix(contentType, "multipart/") {
		if boundary := headerParam(contentTypeHeader, "boundary"); boundary != nil {
			sections := strings.Split(body, "--"+*boundary)
			for _, section := range sections[1:] {
				if strings.HasPrefix(section, "--") {
					break
				}
				trimmed := leadingLineBreakPattern.ReplaceAllString(section, "")
				if strings.TrimSpace(trimmed) == "" {
					continue
				}
				node.Children = append(node.Children, BuildMIMETree(trimmed))
			}
		}
	}
	return node
}

func ExtractLinks(html *string, text *string) []ExtractedLink {
	links := make([]ExtractedLink, 0)
	seen := make(map[string]struct{})
	if html != nil && *html != "" {
		for _, match := range anchorPattern.FindAllStringSubmatch(*html, -1) {
			href := strings.TrimSpace(match[1])
			key := "a:" + href
			if strings.HasPrefix(href, "#") || contains(seen, key) {
				continue
			}
			seen[key] = struct{}{}
			label := tagPattern.ReplaceAllString(match[2], " ")
			label = strings.TrimSpace(spacePattern.ReplaceAllString(label, " "))
			links = append(links, ExtractedLink{Href: href, Text: truncate(label, 120), Kind: "link"})
		}
		for _, tag := range imagePattern.FindAllString(*html, -1) {
			srcMatch := srcPattern.FindStringSubmatch(tag)
			if srcMatch == nil {
				continue
			}
			src := strings.TrimSpace(srcMatch[1])
			key := "i:" + src
			lowerSource := strings.ToLower(src)
			if src == "" || strings.HasPrefix(lowerSource, "cid:") || strings.HasPrefix(lowerSource, "data:") || contains(seen, key) {
				continue
			}
			seen[key] = struct{}{}
			isPixel := hiddenStylePattern.MatchString(tag) || dimensionAtMostOne(widthPattern, tag) || dimensionAtMostOne(heightPattern, tag)
			alt := ""
			if altMatch := altPattern.FindStringSubmatch(tag); altMatch != nil {
				alt = altMatch[1]
			}
			kind := "image"
			if isPixel {
				kind = "tracking-pixel"
			}
			links = append(links, ExtractedLink{Href: src, Text: truncate(alt, 120), Kind: kind})
		}
	} else if text != nil && *text != "" {
		for _, href := range textURLPattern.FindAllString(*text, -1) {
			key := "a:" + href
			if contains(seen, key) {
				continue
			}
			seen[key] = struct{}{}
			links = append(links, ExtractedLink{Href: href, Text: "", Kind: "link"})
		}
	}
	return links
}

func RunHeaderChecks(input HeaderCheckInput) []HeaderCheck {
	normalized := make(map[string]string, len(input.Headers))
	for key, value := range input.Headers {
		normalized[strings.ToLower(key)] = value
	}
	has := func(key string) bool {
		_, ok := normalized[strings.ToLower(key)]
		return ok
	}
	get := func(key string) *string {
		value, ok := normalized[strings.ToLower(key)]
		if !ok {
			return nil
		}
		return &value
	}
	checks := make([]HeaderCheck, 0, 9)
	if has("message-id") {
		checks = append(checks, HeaderCheck{ID: "message-id", Label: "Message-ID", Status: "pass", Detail: "Message-ID header present."})
	} else {
		checks = append(checks, HeaderCheck{ID: "message-id", Label: "Message-ID", Status: "warn", Detail: "Missing Message-ID. Many providers penalize mail without one."})
	}
	if authenticationResults := get("authentication-results"); authenticationResults != nil {
		spf := firstCapture(spfPattern, *authenticationResults)
		dkim := firstCapture(dkimPattern, *authenticationResults)
		status := "info"
		if spf == "pass" && dkim == "pass" {
			status = "pass"
		}
		if spf == "" {
			spf = "n/a"
		}
		if dkim == "" {
			dkim = "n/a"
		}
		checks = append(checks, HeaderCheck{ID: "auth-results", Label: "Authentication-Results", Status: status, Detail: "SPF: " + spf + ", DKIM: " + dkim + "."})
	} else {
		checks = append(checks, HeaderCheck{ID: "auth-results", Label: "SPF / DKIM", Status: "info", Detail: "No Authentication-Results header. Expected in a test environment without a receiving MTA."})
	}
	if has("dkim-signature") {
		checks = append(checks, HeaderCheck{ID: "dkim", Label: "DKIM-Signature", Status: "pass", Detail: "Message is DKIM-signed."})
	} else {
		checks = append(checks, HeaderCheck{ID: "dkim", Label: "DKIM-Signature", Status: "info", Detail: "Message is not DKIM-signed."})
	}
	if has("list-unsubscribe") {
		checks = append(checks, HeaderCheck{ID: "unsubscribe", Label: "List-Unsubscribe", Status: "pass", Detail: "List-Unsubscribe header present (required for bulk mail by Gmail/Yahoo)."})
	} else {
		checks = append(checks, HeaderCheck{ID: "unsubscribe", Label: "List-Unsubscribe", Status: "info", Detail: "No List-Unsubscribe header. Required by Gmail/Yahoo for bulk senders since 2024."})
	}
	if input.HTML != nil && *input.HTML != "" {
		missingAlt := 0
		for _, tag := range imagePattern.FindAllString(*input.HTML, -1) {
			if !altPresencePattern.MatchString(tag) {
				missingAlt++
			}
		}
		if missingAlt == 0 {
			checks = append(checks, HeaderCheck{ID: "img-alt", Label: "Image alt text", Status: "pass", Detail: "All images have alt attributes."})
		} else {
			plural := ""
			if missingAlt > 1 {
				plural = "s"
			}
			checks = append(checks, HeaderCheck{ID: "img-alt", Label: "Image alt text", Status: "warn", Detail: fmt.Sprintf("%d image%s missing alt attributes (hurts accessibility and clipped rendering).", missingAlt, plural)})
		}
		externalImages := countKind(input.Links, "image")
		if externalImages > 0 {
			plural := ""
			if externalImages > 1 {
				plural = "s"
			}
			checks = append(checks, HeaderCheck{ID: "external-images", Label: "External images", Status: "info", Detail: fmt.Sprintf("%d externally hosted image%s — blocked by default in many clients.", externalImages, plural)})
		} else {
			checks = append(checks, HeaderCheck{ID: "external-images", Label: "External images", Status: "pass", Detail: "No externally hosted images."})
		}
	}
	pixels := countKind(input.Links, "tracking-pixel")
	if pixels > 0 {
		plural := ""
		if pixels > 1 {
			plural = "s"
		}
		checks = append(checks, HeaderCheck{ID: "tracking", Label: "Tracking pixels", Status: "warn", Detail: fmt.Sprintf("%d likely tracking pixel%s detected.", pixels, plural)})
	} else {
		checks = append(checks, HeaderCheck{ID: "tracking", Label: "Tracking pixels", Status: "pass", Detail: "No tracking pixels detected."})
	}
	if input.Size > 102*1024 {
		checks = append(checks, HeaderCheck{ID: "size", Label: "Message size", Status: "warn", Detail: fmt.Sprintf("Message is %.0f KB — Gmail clips messages over 102 KB.", float64(input.Size)/1024)})
	} else {
		checks = append(checks, HeaderCheck{ID: "size", Label: "Message size", Status: "pass", Detail: fmt.Sprintf("Message is %.1f KB (under Gmail's 102 KB clip limit).", float64(input.Size)/1024)})
	}
	insecureLinks := 0
	for _, link := range input.Links {
		if link.Kind == "link" && strings.HasPrefix(strings.ToLower(link.Href), "http://") {
			insecureLinks++
		}
	}
	if insecureLinks > 0 {
		detail := fmt.Sprintf("%d link uses plain HTTP instead of HTTPS.", insecureLinks)
		if insecureLinks > 1 {
			detail = fmt.Sprintf("%d links use plain HTTP instead of HTTPS.", insecureLinks)
		}
		checks = append(checks, HeaderCheck{ID: "https", Label: "Link security", Status: "warn", Detail: detail})
	} else {
		checks = append(checks, HeaderCheck{ID: "https", Label: "Link security", Status: "pass", Detail: "All links use HTTPS (or none present)."})
	}
	return checks
}

func splitHeadersBody(part string) (string, string) {
	if separator := strings.Index(part, "\r\n\r\n"); separator != -1 {
		return part[:separator], part[separator+4:]
	}
	if separator := strings.Index(part, "\n\n"); separator != -1 {
		return part[:separator], part[separator+2:]
	}
	return part, ""
}

func headerValue(headers string, name string) *string {
	unfolded := unfoldPattern.ReplaceAllString(headers, " ")
	pattern := regexp.MustCompile(`(?im)^` + regexp.QuoteMeta(name) + `:[ \t]*(.+)$`)
	match := pattern.FindStringSubmatch(unfolded)
	if match == nil {
		return nil
	}
	value := strings.TrimSpace(match[1])
	return &value
}

func headerParam(headerValue *string, name string) *string {
	if headerValue == nil {
		return nil
	}
	pattern := regexp.MustCompile(`(?i)` + regexp.QuoteMeta(name) + `\s*=\s*"?([^";\s]+)"?`)
	match := pattern.FindStringSubmatch(*headerValue)
	if match == nil {
		return nil
	}
	value := match[1]
	return &value
}

func lowerPointer(value *string) *string {
	if value == nil {
		return nil
	}
	lowered := strings.ToLower(*value)
	return &lowered
}

func contains(values map[string]struct{}, key string) bool {
	_, ok := values[key]
	return ok
}

func truncate(value string, limit int) string {
	units := utf16.Encode([]rune(value))
	if len(units) <= limit {
		return value
	}
	return string(utf16.Decode(units[:limit]))
}

func javascriptLength(value string) int {
	return len(utf16.Encode([]rune(value)))
}

func dimensionAtMostOne(pattern *regexp.Regexp, tag string) bool {
	match := pattern.FindStringSubmatch(tag)
	if match == nil {
		return false
	}
	value, _ := strconv.Atoi(match[1])
	return value <= 1
}

func firstCapture(pattern *regexp.Regexp, value string) string {
	match := pattern.FindStringSubmatch(value)
	if match == nil {
		return ""
	}
	return match[1]
}

func countKind(links []ExtractedLink, kind string) int {
	count := 0
	for _, link := range links {
		if link.Kind == kind {
			count++
		}
	}
	return count
}
