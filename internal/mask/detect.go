package mask

import (
	"regexp"
	"strconv"
	"time"
	"unicode"
	"unicode/utf8"
)

const vinPattern, peselPattern = `[A-HJ-NPR-Za-hj-npr-z0-9]{17}`, `[0-9]{11}`

var candidates = regexp.MustCompile(vinPattern + "|" + peselPattern)

func DetectorPatterns() map[string]string {
	return map[string]string{"vin": vinPattern, "pesel": peselPattern, "guid": guidCandidate.String(), "email": emailCandidate.String()}
}

type Match struct {
	Start, End int
	Kind       string
	Valid      bool
	Rule       string
}

// Detect includes invalid PESEL candidates because a typo does not remove sensitivity.
func Detect(text string) []Match {
	var matches []Match
	// Match maximal ASCII runs first so a PESEL prefix cannot hide a complete VIN.
	for start := 0; start < len(text); {
		end := start
		for end < len(text) && asciiWord(text[end]) {
			end++
		}
		if end == start {
			start++
			continue
		}
		word := text[start:end]
		eligible := len(word) == 11 || len(word) == 17
		if len(word) == 11 {
			for i := range word {
				if word[i] < '0' || word[i] > '9' {
					eligible = false
					break
				}
			}
		}
		if eligible && boundary(text, start, end) {
			loc := candidates.FindStringIndex(word)
			if loc != nil && loc[0] == 0 && loc[1] == len(word) {
				kind, valid := "VIN", true
				if len(word) == 11 {
					kind, valid = "PESEL", ValidPESEL(word)
				}
				matches = append(matches, Match{Start: start, End: end, Kind: kind, Valid: valid})
			}
		}
		start = end
	}
	return matches
}

func asciiWord(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_'
}

func boundary(s string, start, end int) bool {
	if start > 0 {
		r, _ := utf8.DecodeLastRuneInString(s[:start])
		if unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_' {
			return false
		}
	}
	if end < len(s) {
		r, _ := utf8.DecodeRuneInString(s[end:])
		if unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_' {
			return false
		}
	}
	return true
}

func ValidPESEL(s string) bool {
	if len(s) != 11 {
		return false
	}
	weights := [...]int{1, 3, 7, 9, 1, 3, 7, 9, 1, 3}
	sum := 0
	for i := range s {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
		if i < 10 {
			sum += int(s[i]-'0') * weights[i]
		}
	}
	if (10-sum%10)%10 != int(s[10]-'0') {
		return false
	}
	year, _ := strconv.Atoi(s[:2])
	month, _ := strconv.Atoi(s[2:4])
	day, _ := strconv.Atoi(s[4:6])
	centuries := [...]int{1900, 2000, 2100, 2200, 1800}
	if month/20 >= len(centuries) {
		return false
	}
	year += centuries[month/20]
	month %= 20
	if month < 1 || month > 12 || day < 1 || day > 31 {
		return false
	}
	d := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	return d.Year() == year && int(d.Month()) == month && d.Day() == day
}
