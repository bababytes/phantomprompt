package proxy

import (
	"slices"
	"sync"
	"time"

	"phantomprompt/internal/mask"
)

// RuleStat tells how often one custom regex masked something; the values themselves are never kept.
type RuleStat struct {
	ID       string     `json:"id,omitempty"`
	Name     string     `json:"name,omitempty"`
	Pattern  string     `json:"pattern"`
	Hits     uint64     `json:"hits"`
	Unique   uint64     `json:"unique"`
	LastHit  *time.Time `json:"last_hit,omitempty"`
	Examples []string   `json:"examples,omitempty"`
}

const maxRuleExamples = 12

type ruleStats struct {
	mu    sync.Mutex
	items map[string]*RuleStat
}

func (r *ruleStats) record(counts mask.Counts) {
	if len(counts.RuleHits) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.items == nil {
		r.items = make(map[string]*RuleStat)
	}
	now := time.Now().UTC()
	for pattern, hits := range counts.RuleHits {
		stat := r.items[pattern]
		if stat == nil {
			stat = &RuleStat{Pattern: pattern}
			r.items[pattern] = stat
		}
		stat.Hits += uint64(hits)
		stat.Unique += uint64(counts.RuleNew[pattern])
		stat.LastHit = &now
		for _, preview := range counts.RulePreviews[pattern] {
			if slices.Contains(stat.Examples, preview) {
				continue
			}
			stat.Examples = append(stat.Examples, preview)
			if len(stat.Examples) > maxRuleExamples {
				stat.Examples = slices.Delete(stat.Examples, 0, 1)
			}
		}
	}
}

func (r *ruleStats) clearExamples() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, stat := range r.items {
		stat.Examples = nil
	}
}

// CustomRuleStats lists the active custom rules in their order, with zero counts for rules that
// have not matched yet.
func (a *App) CustomRuleStats() []RuleStat {
	rules := a.Policy().CustomRules
	a.customRules.mu.Lock()
	defer a.customRules.mu.Unlock()
	stats := make([]RuleStat, 0, len(rules))
	for _, rule := range rules {
		stat := RuleStat{Pattern: rule.Pattern}
		if seen := a.customRules.items[rule.Pattern]; seen != nil {
			stat = *seen
			stat.Examples = slices.Clone(seen.Examples)
		}
		stats = append(stats, stat)
	}
	return stats
}

// SecretRuleStats lists the enabled built-in secret patterns the same way.
func (a *App) SecretRuleStats() []RuleStat {
	disabled := a.Policy().DisabledSecrets
	a.customRules.mu.Lock()
	defer a.customRules.mu.Unlock()
	var stats []RuleStat
	for _, detector := range mask.SecretDetectors() {
		if slices.Contains(disabled, detector.ID) {
			continue
		}
		stat := RuleStat{}
		if seen := a.customRules.items[mask.SecretRuleKey(detector.ID)]; seen != nil {
			stat = *seen
			stat.Examples = slices.Clone(seen.Examples)
		}
		stat.ID, stat.Name, stat.Pattern = detector.ID, detector.Name, detector.Pattern
		stats = append(stats, stat)
	}
	return stats
}

type RuleUse struct {
	Secret bool `json:"secret,omitempty"`
	Values int  `json:"values"`
	Hits   int  `json:"hits"`
}

func (u RuleUse) add(other RuleUse) RuleUse {
	return RuleUse{Secret: u.Secret || other.Secret, Values: u.Values + other.Values, Hits: u.Hits + other.Hits}
}

func (a *App) ruleUses(counts mask.Counts) map[string]RuleUse {
	if len(counts.RuleHits) == 0 {
		return nil
	}
	rules := a.Policy().CustomRules
	detectors := mask.SecretDetectors()
	uses := make(map[string]RuleUse, len(counts.RuleHits))
	for pattern, hits := range counts.RuleHits {
		var rule mask.RegexRule
		if index := slices.IndexFunc(rules, func(candidate mask.RegexRule) bool { return candidate.Pattern == pattern }); index >= 0 {
			rule = rules[index]
		} else if index := slices.IndexFunc(detectors, func(detector mask.SecretDetector) bool { return mask.SecretRuleKey(detector.ID) == pattern }); index >= 0 {
			rule = mask.RegexRule{Name: detectors[index].Name, Secret: true}
		}
		name := rule.Name
		switch {
		case name != "":
		case rule.Secret:
			name = "unnamed secret rule"
		default:
			name = "unnamed rule"
		}
		uses[name] = uses[name].add(RuleUse{Secret: rule.Secret, Values: counts.RuleNew[pattern], Hits: hits})
	}
	return uses
}

func (a *App) setMatches(event *Event, counts mask.Counts) {
	event.Rules = a.ruleUses(counts)
	counts.RuleHits, counts.RuleNew, counts.RulePreviews = nil, nil, nil
	event.Matches = counts
}
