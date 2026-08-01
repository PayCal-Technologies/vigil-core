package output

import (
	"bytes"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestWriteJSONIsIndentedAndDoesNotEscapeHTML(t *testing.T) {
	var buffer bytes.Buffer
	if err := WriteJSON(&buffer, map[string]string{"value": "<pack>"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buffer.String(), "\n  \"value\": \"<pack>\"\n") {
		t.Fatalf("output = %q", buffer.String())
	}
}

func TestTrimSummaryDoesNotSplitUTF8(t *testing.T) {
	got := TrimSummary("abc\u20acdef", 5)
	if !utf8.ValidString(got) {
		t.Fatalf("invalid UTF-8: %q", got)
	}
	if got != "abc\n[truncated]" {
		t.Fatalf("summary = %q", got)
	}
}

func TestStatusLabelColorIsExplicit(t *testing.T) {
	if got := StatusLabel("ok", false); got != "[OK]" {
		t.Fatalf("plain label = %q", got)
	}
	if got := StatusLabel("fail", true); !strings.Contains(got, "\x1b[31m") {
		t.Fatalf("colored label = %q", got)
	}
}
