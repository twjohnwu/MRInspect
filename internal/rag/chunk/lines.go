package chunk

import "strings"

// linesPerChunk bounds each fallback chunk's size. REQ-03 specifies no
// exact figure for the lines strategy; this value keeps a chunk small
// enough to stay useful without producing one chunk per line.
const linesPerChunk = 40

// Lines splits source into fixed-size, non-overlapping windows at line
// boundaries. It is the fallback used when a structured-strategy file
// cannot be parsed (REQ-03 / S-11).
func Lines(source string) []Chunk {
	if source == "" {
		return nil
	}
	lines := strings.Split(source, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return nil
	}

	var chunks []Chunk
	for start := 0; start < len(lines); start += linesPerChunk {
		end := start + linesPerChunk
		if end > len(lines) {
			end = len(lines)
		}
		text := strings.Join(lines[start:end], "\n")
		chunks = append(chunks, Chunk{
			Text:      text,
			StartLine: start + 1,
			EndLine:   end,
			TokenEst:  TokenEst(text),
		})
	}
	return chunks
}
