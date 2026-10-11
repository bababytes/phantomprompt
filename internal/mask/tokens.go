package mask

import (
	"unicode"
	"unicode/utf8"
)

func EstimateTokens(text string) int {
	tokens := 0
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		switch {
		case unicode.IsSpace(r):
			i += size
		case r < utf8.RuneSelf && unicode.IsLetter(r):
			start := i
			for i < len(text) && text[i] < utf8.RuneSelf && unicode.IsLetter(rune(text[i])) {
				i++
			}
			tokens += (i - start + 4) / 5
		case r >= '0' && r <= '9':
			start := i
			for i < len(text) && text[i] >= '0' && text[i] <= '9' {
				i++
			}
			tokens += (i - start + 2) / 3
		case unicode.IsLetter(r):
			letters := 0
			for i < len(text) {
				next, width := utf8.DecodeRuneInString(text[i:])
				if next < utf8.RuneSelf || !unicode.IsLetter(next) {
					break
				}
				i += width
				letters++
			}
			tokens += (letters + 1) / 2
		default:
			tokens++
			i += size
		}
	}
	return tokens
}
