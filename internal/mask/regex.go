package mask

import (
	"errors"
	"regexp"
	"regexp/syntax"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

var ErrRegex = errors.New("invalid custom regex rule; use up to 16 non-empty Go patterns of at most 512 bytes")

type RegexRule struct {
	Pattern string `json:"pattern"`
	Name    string `json:"name,omitempty"`
	// Secret masks matches as SECRET, so shell, web and MCP tool calls never get them back.
	Secret bool `json:"secret,omitempty"`
}

func validExample(text string) bool {
	return strings.TrimSpace(text) != "" && utf8.ValidString(text) && len(text) <= 128 && !strings.Contains(text, Prefix)
}

func LiteralPattern(text string, caseSensitive bool) (string, error) {
	if !validExample(text) {
		return "", ErrRegex
	}
	pattern := regexp.QuoteMeta(text)
	if !caseSensitive {
		pattern = "(?i:" + pattern + ")"
	}
	return pattern, nil
}

func CompileRules(rules []RegexRule) ([]*regexp.Regexp, error) {
	if len(rules) > 16 {
		return nil, ErrRegex
	}
	compiled := make([]*regexp.Regexp, 0, len(rules))
	for _, rule := range rules {
		if rule.Pattern == "" || len(rule.Pattern) > 512 || !utf8.ValidString(rule.Pattern) || len(rule.Name) > 64 || !utf8.ValidString(rule.Name) || strings.ContainsFunc(rule.Name, unicode.IsControl) {
			return nil, ErrRegex
		}
		re, err := regexp.Compile(rule.Pattern)
		if err != nil || re.MatchString("") {
			return nil, ErrRegex
		}
		parsed, err := syntax.Parse(rule.Pattern, syntax.Perl)
		if err != nil || canMatchEmpty(parsed) {
			return nil, ErrRegex
		}
		compiled = append(compiled, re)
	}
	return compiled, nil
}

// Assertions can match zero characters in non-empty input, even if MatchString("") is false.
func canMatchEmpty(re *syntax.Regexp) bool {
	switch re.Op {
	case syntax.OpNoMatch, syntax.OpCharClass, syntax.OpAnyChar, syntax.OpAnyCharNotNL:
		return false
	case syntax.OpLiteral:
		return len(re.Rune) == 0
	case syntax.OpCapture, syntax.OpPlus:
		return canMatchEmpty(re.Sub[0])
	case syntax.OpRepeat:
		return re.Min == 0 || canMatchEmpty(re.Sub[0])
	case syntax.OpConcat:
		for _, sub := range re.Sub {
			if !canMatchEmpty(sub) {
				return false
			}
		}
		return true
	case syntax.OpAlternate:
		for _, sub := range re.Sub {
			if canMatchEmpty(sub) {
				return true
			}
		}
		return false
	default:
		return true
	}
}

// Existing matches take precedence; sorted intervals avoid a quadratic overlap scan.
func addUncovered(existing, candidates []replacement) []replacement {
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].Start < candidates[j].Start })
	selected := make([]replacement, 0, len(candidates))
	i, end := 0, -1
	for _, candidate := range candidates {
		for i < len(existing) && existing[i].End <= candidate.Start {
			i++
		}
		if candidate.Start < end || i < len(existing) && existing[i].Start < candidate.End {
			continue
		}
		selected = append(selected, candidate)
		end = candidate.End
	}
	result := append(existing, selected...)
	sort.Slice(result, func(i, j int) bool { return result[i].Start < result[j].Start })
	return result
}
