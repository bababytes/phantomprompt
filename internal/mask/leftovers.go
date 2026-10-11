package mask

import (
	"regexp"
	"strings"
)

var looseToken = regexp.MustCompile(`(?i)(?:\[[\t ]?)?PP[\t ]?_[\t ]?[A-Z]{2,10}[\t ]?_[\t ]?[A-Z0-9]{1,10}[\t ]?\]?`)

const maxLooseToken = 32
const maxLeftovers = 4096

// Leftovers collects placeholders that survived a restore (re-cased, cut short, split by a space,
// missing a bracket or unknown to the session), so the user can be told an answer still holds
// some. Issued placeholders count too, unless the restore reported them as kept on purpose. Text
// may arrive in pieces; the same placeholder seen again (a stream repeats its text in final
// events) counts once.
type Leftovers struct {
	session *Session
	carry   string
	seen    map[string]bool
	issued  map[string]bool
}

func (s *Session) Leftovers() *Leftovers {
	return &Leftovers{session: s, seen: make(map[string]bool), issued: make(map[string]bool)}
}

func (l *Leftovers) Write(p []byte) (int, error) {
	l.scan(l.carry+string(p), false)
	return len(p), nil
}

func (l *Leftovers) Count(kept map[string]bool) int {
	l.scan(l.carry, true)
	count := len(l.seen)
	for token := range l.issued {
		if !kept[token] {
			count++
		}
	}
	return count
}

func (l *Leftovers) scan(text string, final bool) {
	l.carry = ""
	safe := len(text)
	if !final {
		safe = max(0, len(text)-maxLooseToken)
	}
	evaluated := 0
	for _, loc := range looseToken.FindAllStringIndex(text, -1) {
		if loc[0] >= safe {
			break
		}
		evaluated = loc[1]
		token := text[loc[0]:loc[1]]
		opened, closed := token[0] == '[', token[len(token)-1] == ']'
		if !closed {
			token = strings.TrimRight(token, " \t")
		}
		end := loc[0] + len(token)
		if !opened && !closed || !opened && loc[0] > 0 && wordByte(text[loc[0]-1]) || !closed && end < len(text) && wordByte(text[end]) {
			continue
		}
		if tokenPattern.FindString(token) == token && l.session.issued(token) {
			if len(l.issued) < maxLeftovers {
				l.issued[token] = true
			}
			continue
		}
		if len(l.seen) < maxLeftovers {
			l.seen[strings.ToUpper(strings.NewReplacer("[", "", "]", "", " ", "", "\t", "").Replace(token))] = true
		}
	}
	if !final {
		l.carry = text[max(safe, evaluated):]
	}
}

func wordByte(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_'
}

func (s *Session) issued(token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, known := s.reverse[token]
	return known
}
