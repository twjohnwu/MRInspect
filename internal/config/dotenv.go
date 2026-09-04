package config

import (
	"bufio"
	"io"
	"os"
	"strings"
)

// parseDotenv reads KEY=VALUE lines from r. Blank lines and lines starting
// with '#' are skipped. Each remaining line is split on the first '=' into
// a key and value, both trimmed of surrounding whitespace. An inline
// comment (a '#' preceded by whitespace) is stripped from unquoted values.
// Values wrapped in single or double quotes are unwrapped as-is, keeping
// any inner '#' or spaces. Lines with no '=' or an empty key are reported
// by line number in malformed and not applied to the returned map; their
// content is never echoed.
func parseDotenv(r io.Reader) (map[string]string, []int) {
	values := make(map[string]string)
	var malformed []int

	scanner := bufio.NewScanner(r)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		idx := strings.Index(line, "=")
		if idx < 0 {
			malformed = append(malformed, lineNumber)
			continue
		}

		key := strings.TrimSpace(line[:idx])
		if key == "" {
			malformed = append(malformed, lineNumber)
			continue
		}

		value := strings.TrimSpace(line[idx+1:])
		if quoted, ok := unwrapQuoted(value); ok {
			value = quoted
		} else if hashIdx := strings.Index(value, " #"); hashIdx >= 0 {
			value = strings.TrimSpace(value[:hashIdx])
		} else if strings.HasPrefix(value, "#") {
			value = ""
		} else if tabIdx := strings.Index(value, "\t#"); tabIdx >= 0 {
			value = strings.TrimSpace(value[:tabIdx])
		}

		values[key] = value
	}

	return values, malformed
}

// unwrapQuoted strips a matching pair of surrounding single or double
// quotes from value, returning the inner content unchanged (no comment
// stripping, no further trimming) and true. If value is not fully quoted,
// it returns ("", false).
func unwrapQuoted(value string) (string, bool) {
	if len(value) < 2 {
		return "", false
	}
	first, last := value[0], value[len(value)-1]
	if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
		return value[1 : len(value)-1], true
	}
	return "", false
}

// applyDotenv sets each key in values as a process environment variable,
// but only if the key is not already present in the environment — the
// existing process env always wins.
func applyDotenv(values map[string]string) {
	for key, value := range values {
		if _, ok := os.LookupEnv(key); ok {
			continue
		}
		os.Setenv(key, value)
	}
}

// LoadDotenv reads and applies the .env file at path. A missing file is a
// silent no-op (returns nil). If present, its contents are parsed and
// applied via applyDotenv. Malformed line numbers are returned for the caller
// to report; malformed content is never echoed.
func LoadDotenv(path string) []int {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	values, malformed := parseDotenv(f)
	applyDotenv(values)
	return malformed
}
