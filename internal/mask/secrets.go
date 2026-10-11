package mask

import (
	"errors"
	"regexp"
	"slices"
	"strings"
)

// secretRule pairs a cheap substring pre-filter with a RE2 pattern. The pre-filter lets
// Mask skip the pattern entirely unless the text could contain it, and RE2 guarantees
// linear-time matching, so no input can make scanning hang.
type secretRule struct {
	id, name   string
	prefilters []string
	re         *regexp.Regexp
	find       func(text string) [][]int
}

var ErrSecrets = errors.New("unknown built-in secret pattern")

const maxSecretMatches = 4096

var secretRules = buildSecretRules()

func buildSecretRules() []secretRule {
	rules := []struct {
		id, name   string
		prefilters []string
		pattern    string
		find       func(text string) [][]int
	}{
		// Known credential formats (no label needed).
		{"aws-access-key", "AWS access key ID", []string{"AKIA", "ASIA"}, `\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`, nil},
		{"github-token", "GitHub token", []string{"ghp_", "gho_", "ghu_", "ghs_", "ghr_"}, `\bgh[pousr]_[A-Za-z0-9]{36,251}\b`, nil},
		{"github-fine-grained-token", "GitHub fine-grained token", []string{"github_pat_"}, `\bgithub_pat_[0-9A-Za-z_]{22,255}\b`, nil},
		{"slack-token", "Slack token", []string{"xox"}, `\bxox[baprse]-[0-9A-Za-z-]{10,255}\b`, nil},
		{"google-api-key", "Google API key", []string{"AIza"}, `\bAIza[0-9A-Za-z_\-]{35}\b`, nil},
		{"stripe-key", "Stripe key", []string{"k_live_", "k_test_"}, `\b[rs]k_(?:live|test)_[0-9A-Za-z]{16,247}\b`, nil},
		{"openai-anthropic-key", "OpenAI or Anthropic API key", []string{"sk-"}, `\bsk-(?:ant-)?[A-Za-z0-9_\-]{20,255}\b`, nil},
		{"jwt", "JSON Web Token", []string{"eyJ"}, `\beyJ[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]{8,}\b`, nil},
		{"slack-app-token", "Slack app token", []string{"xapp-", "xoxb-"}, `\bxapp-[0-9]-[A-Za-z0-9-]{10,255}\b`, nil},
		{"private-key", "Private key block", []string{"PRIVATE KEY"}, `(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----`, nil},
		// Only the password (group 1) is masked; template variables like ${PASSWORD} are left alone.
		{"url-credentials", "Password in a URL", []string{"://"}, `[a-z][a-z0-9+.-]*://[^\s/@:]+:([^\s/@$<{][^\s/@]*)@`, nil},
		{labelledSecretID, "Password, token or secret after a label", []string{"pass", "pwd", "secret", "token", "key", "identified"}, labelledSecretPattern, labelledSecrets},
	}
	compiled := make([]secretRule, 0, len(rules))
	for _, r := range rules {
		compiled = append(compiled, secretRule{id: r.id, name: r.name, prefilters: r.prefilters, re: regexp.MustCompile(r.pattern), find: r.find})
	}
	return compiled
}

type SecretDetector struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Pattern string `json:"pattern"`
}

func SecretDetectors() []SecretDetector {
	detectors := make([]SecretDetector, 0, len(secretRules))
	for _, rule := range secretRules {
		detectors = append(detectors, SecretDetector{ID: rule.id, Name: rule.name, Pattern: rule.re.String()})
	}
	return detectors
}

func knownSecretID(id string) bool {
	return slices.ContainsFunc(secretRules, func(rule secretRule) bool { return rule.id == id })
}

func KnownSecretIDs(ids []string) []string {
	return slices.DeleteFunc(slices.Clone(ids), func(id string) bool { return !knownSecretID(id) })
}

func validSecretIDs(ids []string) bool {
	if len(ids) > len(secretRules) {
		return false
	}
	for _, id := range ids {
		if !knownSecretID(id) {
			return false
		}
	}
	return true
}

func containsAnyFold(text string, needles []string) bool {
	lower := ""
	for _, n := range needles {
		if strings.Contains(text, n) {
			return true
		}
		if lower == "" {
			lower = strings.ToLower(text)
		}
		if strings.Contains(lower, strings.ToLower(n)) {
			return true
		}
	}
	return false
}

// detectSecrets returns non-overlapping SECRET spans. It is allocation-light on text
// without any credential markers because every rule is gated by a substring pre-filter.
func SecretRuleKey(id string) string { return "builtin:" + id }

func detectSecrets(text string, disabled []string) ([]Match, error) {
	var matches []Match
	for _, rule := range secretRules {
		if slices.Contains(disabled, rule.id) || !containsAnyFold(text, rule.prefilters) {
			continue
		}
		var spans [][]int
		if rule.find != nil {
			spans = rule.find(text)
		} else {
			spans = rule.re.FindAllStringSubmatchIndex(text, maxSecretMatches+1)
		}
		if len(spans) > maxSecretMatches {
			return nil, ErrLimit
		}
		for _, loc := range spans {
			if rule.find == nil && rule.re.NumSubexp() > 0 {
				loc = loc[2:4]
			}
			if loc[1] > loc[0] {
				matches = append(matches, Match{Start: loc[0], End: loc[1], Kind: "SECRET", Valid: true, Rule: SecretRuleKey(rule.id)})
			}
		}
	}
	return matches, nil
}
