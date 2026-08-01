package runner

import (
	"errors"
	"strings"
	"unicode"
)

func ParseCommandLine(input string) ([]string, error) {
	var (
		args      []string
		current   strings.Builder
		quote     rune
		escaped   bool
		tokenOpen bool
	)
	flush := func() {
		if tokenOpen {
			args = append(args, current.String())
			current.Reset()
			tokenOpen = false
		}
	}
	for _, char := range input {
		if escaped {
			current.WriteRune(char)
			tokenOpen = true
			escaped = false
			continue
		}
		switch quote {
		case '\'':
			if char == '\'' {
				quote = 0
			} else {
				current.WriteRune(char)
			}
			tokenOpen = true
			continue
		case '"':
			switch char {
			case '"':
				quote = 0
			case '\\':
				escaped = true
			default:
				current.WriteRune(char)
			}
			tokenOpen = true
			continue
		}
		switch {
		case char == '\\':
			escaped = true
			tokenOpen = true
		case char == '\'' || char == '"':
			quote = char
			tokenOpen = true
		case unicode.IsSpace(char):
			flush()
		default:
			current.WriteRune(char)
			tokenOpen = true
		}
	}
	if escaped {
		return nil, errors.New("command line ends with an incomplete escape")
	}
	if quote != 0 {
		return nil, errors.New("command line has an unterminated quote")
	}
	flush()
	if len(args) == 0 {
		return nil, errors.New("command line is empty")
	}
	return args, nil
}

func RequiresShell(input string) bool {
	var (
		quote   rune
		escaped bool
	)
	for _, char := range input {
		if escaped {
			escaped = false
			continue
		}
		if quote == '\'' {
			if char == '\'' {
				quote = 0
			}
			continue
		}
		if quote == '"' {
			switch char {
			case '"':
				quote = 0
			case '\\':
				escaped = true
			case '$', '`':
				return true
			}
			continue
		}
		switch char {
		case '\\':
			escaped = true
		case '\'', '"':
			quote = char
		case '|', '&', ';', '<', '>', '$', '`', '(', ')', '{', '}', '*', '?', '[', ']', '\n', '\r':
			return true
		}
	}
	fields, err := ParseCommandLine(input)
	if err != nil || len(fields) == 0 {
		return true
	}
	return strings.Contains(fields[0], "=") && !strings.ContainsAny(fields[0], `/\`)
}
