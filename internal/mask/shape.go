package mask

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

var ErrShape = errors.New("example has no layout to copy")

type partKind int

const (
	letters partKind = iota
	digits
	alphanumeric
	hexadecimal
	separator
	space
)

type shapeRun struct {
	kind partKind
	text string
}

func classify(r rune) partKind {
	switch {
	case r >= '0' && r <= '9':
		return digits
	case unicode.IsLetter(r):
		return letters
	case unicode.IsSpace(r):
		return space
	}
	return separator
}

func shapeRuns(text string) []shapeRun {
	var runs []shapeRun
	for _, r := range text {
		kind := classify(r)
		if last := len(runs) - 1; last >= 0 && runs[last].kind == kind {
			runs[last].text += string(r)
			continue
		}
		runs = append(runs, shapeRun{kind, string(r)})
	}
	return runs
}

func isWord(kind partKind) bool { return kind == letters || kind == digits }

func hexExample(runs []shapeRun) bool {
	mixed := false
	for index, run := range runs {
		if run.kind != letters {
			continue
		}
		if strings.TrimLeft(run.text, "abcdefABCDEF") != "" {
			return false
		}
		mixed = mixed || index > 0 && runs[index-1].kind == digits || index+1 < len(runs) && runs[index+1].kind == digits
	}
	return mixed
}

func ShapePattern(text string, caseSensitive bool) (string, string, error) {
	if !validExample(text) {
		return "", "", ErrRegex
	}
	runs := shapeRuns(text)
	hex := hexExample(runs)
	var pattern, description []string
	specific, hasLetters := false, false
	add := func(kind partKind, sample string) {
		count := utf8.RuneCountInString(sample)
		switch kind {
		case separator:
			pattern = append(pattern, regexp.QuoteMeta(sample))
			description = append(description, "“"+sample+"”")
			specific = true
			return
		case space:
			pattern = append(pattern, `\s+`)
			description = append(description, "space")
			return
		case digits:
			specific = true
		default:
			hasLetters = true
		}
		quantifier := ""
		if count > 1 {
			quantifier = "{" + strconv.Itoa(count) + "}"
		}
		pattern = append(pattern, characterClass(kind, sample, caseSensitive)+quantifier)
		description = append(description, strconv.Itoa(count)+" "+unitName(kind, count))
	}
	for start := 0; start < len(runs); {
		if !isWord(runs[start].kind) {
			add(runs[start].kind, runs[start].text)
			start++
			continue
		}
		end := start
		word := ""
		for end < len(runs) && isWord(runs[end].kind) {
			word += runs[end].text
			end++
		}
		switch {
		case hex:
			add(hexadecimal, word)
		case end-start > 2:
			add(alphanumeric, word)
		default:
			for _, run := range runs[start:end] {
				add(run.kind, run.text)
			}
		}
		start = end
	}
	if len(pattern) < 2 || !specific {
		return "", "", ErrShape
	}
	result := strings.Join(pattern, "")
	if first, _ := utf8.DecodeRuneInString(text); first < utf8.RuneSelf && isWord(classify(first)) {
		result = `\b` + result
	}
	if last, _ := utf8.DecodeLastRuneInString(text); last < utf8.RuneSelf && isWord(classify(last)) {
		result += `\b`
	}
	summary := strings.Join(description, " ")
	if !caseSensitive {
		result = "(?i:" + result + ")"
		if hasLetters {
			summary += ", any letter case"
		}
	}
	if _, err := CompileRules([]RegexRule{{Pattern: result}}); err != nil {
		return "", "", ErrShape
	}
	return result, summary, nil
}

func characterClass(kind partKind, sample string, caseSensitive bool) string {
	if kind == digits {
		return "[0-9]"
	}
	if strings.IndexFunc(sample, func(r rune) bool { return r >= utf8.RuneSelf && unicode.IsLetter(r) }) >= 0 {
		if kind == letters {
			return `\p{L}`
		}
		return `[\p{L}0-9]`
	}
	letterRange := "a-z"
	if caseSensitive {
		upper := strings.IndexFunc(sample, unicode.IsUpper) >= 0
		lower := strings.IndexFunc(sample, unicode.IsLower) >= 0
		switch {
		case upper && lower:
			letterRange = "A-Za-z"
		case upper:
			letterRange = "A-Z"
		}
	}
	switch kind {
	case letters:
		return "[" + letterRange + "]"
	case hexadecimal:
		return "[0-9" + strings.NewReplacer("z", "f", "Z", "F").Replace(letterRange) + "]"
	}
	return "[" + letterRange + "0-9]"
}

func unitName(kind partKind, count int) string {
	name := map[partKind][2]string{
		letters:      {"letter", "letters"},
		digits:       {"digit", "digits"},
		alphanumeric: {"letter or digit", "letters or digits"},
		hexadecimal:  {"hex character", "hex characters"},
	}[kind]
	if count == 1 {
		return name[0]
	}
	return name[1]
}
