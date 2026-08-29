package lane

import (
	"strings"
	"unicode"

	"mrinspect/internal/gitlab"
)

const maxTerms = 40

var stopwords = map[string]struct{}{
	"a": {}, "an": {}, "and": {}, "are": {}, "as": {}, "at": {},
	"be": {}, "by": {}, "false": {}, "for": {}, "from": {}, "func": {},
	"if": {}, "in": {}, "is": {}, "it": {}, "nil": {}, "not": {},
	"of": {}, "on": {}, "or": {}, "return": {}, "that": {}, "the": {},
	"this": {}, "to": {}, "true": {}, "var": {}, "was": {}, "with": {},
	"err": {},
}

// Terms extracts retrieval terms from merge-request file changes.
func Terms(changes []gitlab.Change) []string {
	collector := newTermCollector()
	for _, change := range changes {
		if collector.add(change.NewPath) {
			return collector.terms
		}
		if change.OldPath != change.NewPath && collector.add(change.OldPath) {
			return collector.terms
		}

		for _, line := range strings.Split(change.Diff, "\n") {
			if !isChangedLine(line) {
				continue
			}
			if collector.add(line[1:]) {
				return collector.terms
			}
		}
	}
	return collector.terms
}

type termCollector struct {
	terms []string
	seen  map[string]struct{}
}

func newTermCollector() *termCollector {
	return &termCollector{
		terms: make([]string, 0, maxTerms),
		seen:  make(map[string]struct{}, maxTerms),
	}
}

// add normalizes every identifier-like run in text and reports whether the
// collector has reached its limit.
func (c *termCollector) add(text string) bool {
	for _, term := range splitTerms(text) {
		if _, stopped := stopwords[term]; stopped {
			continue
		}
		if _, exists := c.seen[term]; exists {
			continue
		}
		c.seen[term] = struct{}{}
		c.terms = append(c.terms, term)
		if len(c.terms) == maxTerms {
			return true
		}
	}
	return false
}

func isChangedLine(line string) bool {
	if len(line) == 0 || line[0] != '+' && line[0] != '-' {
		return false
	}
	return !strings.HasPrefix(line, "+++") && !strings.HasPrefix(line, "---")
}

func splitTerms(text string) []string {
	runes := []rune(text)
	terms := make([]string, 0)
	for start := 0; start < len(runes); {
		for start < len(runes) && !isIdentifierRune(runes[start]) {
			start++
		}
		end := start
		for end < len(runes) && isIdentifierRune(runes[end]) {
			end++
		}
		if start < end {
			terms = appendSubwords(terms, runes[start:end])
		}
		start = end
	}
	return terms
}

func isIdentifierRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

func appendSubwords(terms []string, identifier []rune) []string {
	start := 0
	for i := 0; i <= len(identifier); i++ {
		if i < len(identifier) && identifier[i] != '_' && !camelBoundary(identifier, i) {
			continue
		}
		if start < i && containsLetter(identifier[start:i]) {
			terms = append(terms, strings.ToLower(string(identifier[start:i])))
		}
		start = i + 1
		if i < len(identifier) && identifier[i] != '_' {
			start = i
		}
	}
	return terms
}

func camelBoundary(identifier []rune, i int) bool {
	if i == 0 || i == len(identifier) || !unicode.IsUpper(identifier[i]) {
		return false
	}
	previous := identifier[i-1]
	if unicode.IsLower(previous) || unicode.IsDigit(previous) {
		return true
	}
	return unicode.IsUpper(previous) && i+1 < len(identifier) && unicode.IsLower(identifier[i+1])
}

func containsLetter(runes []rune) bool {
	for _, r := range runes {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}
