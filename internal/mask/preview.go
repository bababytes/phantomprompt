package mask

import "unicode"

const maxPreview = 40
const redacted = "••••••"

func Preview(value string, secret bool) string {
	if secret {
		return redacted
	}
	runes := []rune(value)
	if len(runes) > maxPreview {
		runes = append(runes[:maxPreview-1], '…')
	}
	letters := 0
	for _, r := range runes {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			letters++
		}
	}
	visible := max(1, letters/3)
	seen, hidden := 0, 0
	for index, r := range runes {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			continue
		}
		seen++
		if seen > visible && seen < letters {
			runes[index] = '•'
			hidden++
		}
	}
	if hidden == 0 {
		return redacted
	}
	return string(runes)
}
