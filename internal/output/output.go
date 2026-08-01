package output

import (
	"encoding/json"
	"io"
	"strings"
	"unicode/utf8"
)

func WriteJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func StatusLabel(status string, color bool) string {
	label := "[" + strings.ToUpper(status) + "]"
	if !color {
		return label
	}
	switch strings.ToLower(status) {
	case "ok":
		return "\033[32m" + label + "\033[0m"
	case "fail":
		return "\033[31m" + label + "\033[0m"
	case "warn":
		return "\033[33m" + label + "\033[0m"
	default:
		return label
	}
}

func TrimSummary(value string, byteLimit int) string {
	value = strings.TrimSpace(value)
	if byteLimit <= 0 || len(value) <= byteLimit {
		return value
	}
	end := byteLimit
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:end] + "\n[truncated]"
}
