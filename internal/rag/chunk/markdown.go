package chunk

import "strings"

// Markdown splits a markdown document into heading-delimited chunks. ATX
// headings form a breadcrumb, while fenced code blocks remain opaque.
func Markdown(source string) ([]Chunk, error) {
	lines := strings.Split(source, "\n")
	var chunks []Chunk
	var headings []string
	sectionStart, sectionEnd := 0, 0
	inFence := false

	for index, line := range lines {
		lineNumber := index + 1
		if isFence(line) {
			inFence = !inFence
		}

		level, title, isHeading := atxHeading(line)
		if isHeading && !inFence {
			chunks = appendSection(chunks, lines, sectionStart, sectionEnd, breadcrumb(headings))
			headings = replaceHeading(headings, level, title)
			sectionStart, sectionEnd = lineNumber, lineNumber
			continue
		}

		if sectionStart != 0 && strings.TrimSpace(line) != "" {
			sectionEnd = lineNumber
		}
	}

	if sectionStart == 0 {
		return wholeDocument(source, lines), nil
	}
	return appendSection(chunks, lines, sectionStart, sectionEnd, breadcrumb(headings)), nil
}

func atxHeading(line string) (level int, title string, ok bool) {
	for level < 6 && level < len(line) && line[level] == '#' {
		level++
	}
	if level == 0 || level == len(line) || line[level] != ' ' {
		return 0, "", false
	}
	return level, line[level+1:], true
}

func isFence(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")
}

func replaceHeading(headings []string, level int, title string) []string {
	if len(headings) >= level {
		headings = headings[:level-1]
	}
	return append(headings, title)
}

func breadcrumb(headings []string) string {
	return strings.Join(headings, " > ")
}

func appendSection(chunks []Chunk, lines []string, start, end int, heading string) []Chunk {
	if start == 0 {
		return chunks
	}
	text := strings.Join(lines[start-1:end], "\n")
	return append(chunks, Chunk{
		Text:      text,
		Heading:   heading,
		StartLine: start,
		EndLine:   end,
		TokenEst:  TokenEst(text),
	})
}

func wholeDocument(source string, lines []string) []Chunk {
	if source == "" {
		return nil
	}
	end := len(lines)
	if lines[end-1] == "" {
		end--
	}
	text := strings.Join(lines[:end], "\n")
	return []Chunk{{Text: text, StartLine: 1, EndLine: end, TokenEst: TokenEst(text)}}
}
