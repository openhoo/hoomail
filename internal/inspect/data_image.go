package inspect

import (
	"bytes"
	"encoding/base64"
	"strings"
)

const maxEmbeddedDataImageBytes = 25 << 20

func sanitizeEmbeddedImageDataURL(source string) (string, bool) {
	if len(source) < len("data:,") || !strings.EqualFold(source[:5], "data:") {
		return "", false
	}
	comma := strings.IndexByte(source, ',')
	if comma < 0 {
		return "", false
	}
	metadata := strings.Split(source[5:comma], ";")
	if len(metadata) != 2 || !strings.EqualFold(strings.TrimSpace(metadata[1]), "base64") {
		return "", false
	}
	contentType := strings.ToLower(strings.TrimSpace(metadata[0]))
	if contentType == "image/jpg" {
		contentType = "image/jpeg"
	}
	encoded := source[comma+1:]
	if encoded == "" || base64.StdEncoding.DecodedLen(len(encoded)) > maxEmbeddedDataImageBytes {
		return "", false
	}
	decoded := make([]byte, base64.StdEncoding.DecodedLen(len(encoded)))
	length, err := base64.StdEncoding.Strict().Decode(decoded, []byte(encoded))
	if err != nil {
		return "", false
	}
	decoded = decoded[:length]

	var safe []byte
	switch contentType {
	case "image/svg+xml":
		safe, err = SanitizeSVG(decoded)
		if err != nil {
			return "", false
		}
	case "image/png":
		if !bytes.HasPrefix(decoded, []byte("\x89PNG\r\n\x1a\n")) {
			return "", false
		}
		safe = decoded
	case "image/jpeg":
		if len(decoded) < 3 || decoded[0] != 0xff || decoded[1] != 0xd8 || decoded[2] != 0xff {
			return "", false
		}
		safe = decoded
	case "image/gif":
		if !bytes.HasPrefix(decoded, []byte("GIF87a")) && !bytes.HasPrefix(decoded, []byte("GIF89a")) {
			return "", false
		}
		safe = decoded
	case "image/webp":
		if len(decoded) < 12 || !bytes.Equal(decoded[:4], []byte("RIFF")) || !bytes.Equal(decoded[8:12], []byte("WEBP")) {
			return "", false
		}
		safe = decoded
	default:
		return "", false
	}

	return "data:" + contentType + ";base64," + base64.StdEncoding.EncodeToString(safe), true
}

func isNormalizedEmbeddedImage(opaque string) bool {
	for _, prefix := range []string{
		"image/png;base64,",
		"image/jpeg;base64,",
		"image/gif;base64,",
		"image/webp;base64,",
		"image/svg+xml;base64,",
	} {
		if strings.HasPrefix(opaque, prefix) {
			return len(opaque) > len(prefix)
		}
	}
	return false
}
