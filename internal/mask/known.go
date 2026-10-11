package mask

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MinKnownValue keeps very short values (for example "Al") from masking ordinary words.
const MinKnownValue = 3

// Values shorter than this must stand alone ("Jan" is not masked inside "January"); longer ones,
// such as a password glued to a flag in "mysql -phunter2xyz", are masked wherever they appear.
const wholeWordBelow = 8

// contextKinds are detected only through their surroundings (a label, a rule, a file),
// so they would not be found again when a client re-sends them without that context.
var contextKinds = map[string]bool{"FIELD": true, "SECRET": true, "CUSTOM": true, "FILE": true}

func Tokens(text string) []string { return tokenPattern.FindAllString(text, -1) }

func KindKey(token string) string { return kindKeys[tokenKind(token)] }

func tokenKind(token string) string {
	kind, _, _ := strings.Cut(strings.TrimPrefix(token, Prefix), "_")
	return kind
}

type knownNode struct {
	next   map[byte]int32
	fail   int32
	output int32
	link   int32
}

// knownIndex is an Aho-Corasick automaton over values already present in a session map.
type knownIndex struct {
	nodes    []knownNode
	values   []string
	tokens   []string
	revision int
}

func buildKnown(reverse, rules map[string]string, revision int) *knownIndex {
	k := &knownIndex{nodes: []knownNode{{output: -1, link: -1}}, revision: revision}
	tokens := make([]string, 0, len(reverse))
	for token, value := range reverse {
		if learnable(tokenKind(token), rules[token], value) {
			tokens = append(tokens, token)
		}
	}
	sort.Strings(tokens)
	for _, token := range tokens {
		value := reverse[token]
		node := int32(0)
		for i := 0; i < len(value); i++ {
			if k.nodes[node].next == nil {
				k.nodes[node].next = make(map[byte]int32)
			}
			child, ok := k.nodes[node].next[value[i]]
			if !ok {
				child = int32(len(k.nodes))
				k.nodes = append(k.nodes, knownNode{output: -1, link: -1})
				k.nodes[node].next[value[i]] = child
			}
			node = child
		}
		if k.nodes[node].output < 0 {
			k.nodes[node].output = int32(len(k.values))
			k.values = append(k.values, value)
			k.tokens = append(k.tokens, token)
		}
	}
	queue := make([]int32, 0, len(k.nodes))
	for _, child := range k.nodes[0].next {
		queue = append(queue, child)
	}
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		for b, child := range k.nodes[node].next {
			fail := k.nodes[node].fail
			for {
				if next, ok := k.nodes[fail].next[b]; ok && next != child {
					k.nodes[child].fail = next
					break
				}
				if fail == 0 {
					k.nodes[child].fail = 0
					break
				}
				fail = k.nodes[fail].fail
			}
			target := k.nodes[child].fail
			if k.nodes[target].output >= 0 {
				k.nodes[child].link = target
			} else {
				k.nodes[child].link = k.nodes[target].link
			}
			queue = append(queue, child)
		}
	}
	return k
}

func wordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_' }

func knownBoundary(text string, start, end int) bool {
	if first, _ := utf8.DecodeRuneInString(text[start:end]); wordRune(first) && start > 0 {
		if before, _ := utf8.DecodeLastRuneInString(text[:start]); wordRune(before) {
			return false
		}
	}
	if last, _ := utf8.DecodeLastRuneInString(text[start:end]); wordRune(last) && end < len(text) {
		if after, _ := utf8.DecodeRuneInString(text[end:]); wordRune(after) {
			return false
		}
	}
	return true
}

// matches returns every whole-word occurrence of a known value, longest first for equal starts.
func (k *knownIndex) matches(text string, limit int) ([]replacement, error) {
	if k == nil || len(k.values) == 0 {
		return nil, nil
	}
	var found []replacement
	node := int32(0)
	for i := 0; i < len(text); i++ {
		for {
			if next, ok := k.nodes[node].next[text[i]]; ok {
				node = next
				break
			}
			if node == 0 {
				break
			}
			node = k.nodes[node].fail
		}
		for out := node; out >= 0; out = k.nodes[out].link {
			index := k.nodes[out].output
			if index < 0 {
				continue
			}
			start := i + 1 - len(k.values[index])
			if len(k.values[index]) < wholeWordBelow && !knownBoundary(text, start, i+1) {
				continue
			}
			if len(found) >= limit {
				return nil, ErrLimit
			}
			token := k.tokens[index]
			found = append(found, replacement{Start: start, End: i + 1, Kind: tokenKind(token), Token: token})
		}
	}
	sort.SliceStable(found, func(i, j int) bool {
		if found[i].Start != found[j].Start {
			return found[i].Start < found[j].Start
		}
		return found[i].End > found[j].End
	})
	return found, nil
}
