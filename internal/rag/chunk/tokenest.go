package chunk

import "math"

// TokenEst estimates a text's token count according to REQ-14.
func TokenEst(text string) int {
	var asciiBytes, cjkRunes, otherRunes int
	for _, r := range text {
		switch {
		case r < 0x80:
			asciiBytes++
		case isCJK(r):
			cjkRunes++
		default:
			otherRunes++
		}
	}
	estimate := float64(asciiBytes)/4.0 + float64(cjkRunes)*1.5 + float64(otherRunes)*3.0
	return int(math.Ceil(estimate))
}

func isCJK(r rune) bool {
	return (r >= 0x3000 && r <= 0x303F) ||
		(r >= 0x3040 && r <= 0x309F) ||
		(r >= 0x30A0 && r <= 0x30FF) ||
		(r >= 0x3400 && r <= 0x4DBF) ||
		(r >= 0x4E00 && r <= 0x9FFF) ||
		(r >= 0xF900 && r <= 0xFAFF) ||
		(r >= 0xFF00 && r <= 0xFFEF) ||
		(r >= 0xAC00 && r <= 0xD7AF) ||
		(r >= 0x20000 && r <= 0x2FA1F)
}
