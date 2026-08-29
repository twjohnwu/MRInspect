package lane

import "strings"

// neutralize makes a dynamic value safe to place on one markdown line and in
// a table cell. Runs of model-provided newlines become a single space.
func neutralize(value string) string {
	var result strings.Builder
	result.Grow(len(value))

	for index := 0; index < len(value); {
		switch value[index] {
		case '\r', '\n':
			result.WriteByte(' ')
			for index < len(value) && (value[index] == '\r' || value[index] == '\n') {
				index++
			}
		case '|':
			result.WriteString(`\|`)
			index++
		case '#':
			result.WriteString(`\#`)
			index++
		default:
			result.WriteByte(value[index])
			index++
		}
	}

	return result.String()
}
