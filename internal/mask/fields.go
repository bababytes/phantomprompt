package mask

import (
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"sort"
	"strings"
)

var ErrFields = errors.New("invalid sensitive field configuration or value")
var fieldName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)
var fieldLabel = regexp.MustCompile(`"((?:[^"\\]|\\.)*)"[\t\r\n ]*[:=][\t\r\n ]*|\b([A-Za-z][A-Za-z0-9_-]{0,63})[\t ]*[:=][\t ]*|'([A-Za-z][A-Za-z0-9_-]{0,63})'[\t ]*[:=][\t ]*`)
var yamlIndicators = []string{"", "|", ">", "|-", ">-", "|+", ">+"}
var xmlOpenTag = regexp.MustCompile(`<([A-Za-z][A-Za-z0-9_.:-]{0,63})(?:\s[^<>]*)?>`)

var guidCandidate = regexp.MustCompile(`[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}`)

var emailCandidate = regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,63}`)
var fieldSeparators = strings.NewReplacer("_", "", "-", "")

var codeChain = regexp.MustCompile(`^[A-Za-z_]\w*(?:\.[A-Za-z_]\w*)*`)

var codeTypes = map[string]bool{
	"str": true, "string": true, "String": true, "CharSequence": true,
	"int": true, "Int": true, "Integer": true, "long": true, "number": true, "float": true, "double": true, "Double": true, "decimal": true,
	"bool": true, "boolean": true, "Boolean": true, "char": true, "bytes": true,
	"any": true, "Any": true, "unknown": true, "object": true, "Object": true,
	"None": true, "null": true, "nil": true, "undefined": true, "true": true, "false": true,
	"i32": true, "i64": true, "u32": true, "u64": true, "usize": true, "f64": true,
}

const decimalDigits = "0123456789"

var fieldValueChars = map[string]string{
	"email": "@", "address": decimalDigits + " ",
	"phone": decimalDigits, "mobile": decimalDigits, "postcode": decimalDigits, "zipcode": decimalDigits, "vin": decimalDigits,
	"licenceplate": decimalDigits, "accountnumber": decimalDigits, "cardnumber": decimalDigits,
}

func codeValue(field, value string) bool {
	if chars, ok := fieldValueChars[field]; ok && !strings.ContainsAny(value, chars) {
		return true
	}
	if strings.HasPrefix(value, "$") {
		return true
	}
	value = strings.TrimLeft(value, "&*")
	chain := codeChain.FindString(value)
	if chain == "" {
		return false
	}
	head, _, dotted := strings.Cut(chain, ".")
	rest := value[len(chain):]
	switch {
	case codeTypes[head], normalizeField(chain) == field:
		return true
	case rest != "" && strings.IndexByte("(<[", rest[0]) >= 0:
		return true
	}
	return dotted && head[0] >= 'a' && head[0] <= 'z' && (rest == "" || strings.IndexByte("),;]}", rest[0]) >= 0)
}

// fieldNameValue tells a field name or a type standing in a value's place (enum maps, i18n keys,
// struct tags, schemas) from data: masking it would teach the session an ordinary word.
func fieldNameValue(value string, names map[string]bool) bool {
	value = strings.TrimSpace(value)
	return names[normalizeField(value)] || codeTypes[value]
}

func codeAddress(text string, start, end int) bool {
	local, domain, _ := strings.Cut(text[start:end], "@")
	remote := local == "git" && end+1 < len(text) && text[end] == ':' && strings.IndexByte(" \t\r\n", text[end+1]) < 0
	return remote || (local == "this" || local == "super") && domain[0] >= 'A' && domain[0] <= 'Z'
}

// "name" is intentionally excluded: it is far too generic for agent/code traffic
// ("tool name: view", "function name: ...") and masking it corrupts tool references.
// Callers who need it can add "name" through sensitive_fields.
var defaultFields = []string{
	"email", "phone", "mobile", "firstName", "lastName", "fullName",
	"address", "postcode", "zipCode", "vin", "licencePlate",
	"accountNumber", "cardNumber",
}

func DefaultFields() []string { return append([]string(nil), defaultFields...) }

func normalizeField(name string) string {
	return strings.ToLower(fieldSeparators.Replace(name))
}

// FieldNames adds custom names to the built-in set minus disabled built-ins; names never contain actual values.
func FieldNames(custom, disabled []string) (map[string]bool, error) {
	if len(custom) > 64 || len(disabled) > len(defaultFields) {
		return nil, ErrFields
	}
	off := make(map[string]bool, len(disabled))
	for _, name := range disabled {
		key := normalizeField(name)
		if !fieldName.MatchString(name) || !slices.ContainsFunc(defaultFields, func(field string) bool { return normalizeField(field) == key }) {
			return nil, ErrFields
		}
		off[key] = true
	}
	names := make(map[string]bool, len(defaultFields)+len(custom))
	for _, name := range defaultFields {
		if !off[normalizeField(name)] {
			names[normalizeField(name)] = true
		}
	}
	for _, name := range custom {
		if !fieldName.MatchString(name) {
			return nil, ErrFields
		}
		names[normalizeField(name)] = true
	}
	return names, nil
}

type replacement struct {
	Start, End int
	Kind       string
	Quoted     bool
	Secret     bool
	Name       string
	Rule       string
	Token      string
}

func fieldLabels(custom, disabled []string) map[string]string {
	off := make(map[string]bool, len(disabled))
	for _, name := range disabled {
		off[normalizeField(name)] = true
	}
	labels := make(map[string]string, len(defaultFields)+len(custom))
	for _, name := range defaultFields {
		if !off[normalizeField(name)] {
			labels[normalizeField(name)] = name
		}
	}
	for _, name := range custom {
		labels[normalizeField(name)] = name
	}
	return labels
}

func lineEnd(text string, start int) int {
	end := start
	for end < len(text) && text[end] != '\r' && text[end] != '\n' {
		end++
	}
	for end > start && (text[end-1] == ' ' || text[end-1] == '\t') {
		end--
	}
	return end
}

func closingQuote(text string, start int) (int, bool) {
	for i := start + 1; i < len(text); i++ {
		switch text[i] {
		case '\\':
			i++
		case '"':
			return i, true
		case '\r', '\n':
			return i, false
		}
	}
	return len(text), false
}

func singleQuoted(text string, start int) (int, bool) {
	for i := start + 1; i < len(text); i++ {
		switch text[i] {
		case '\\':
			i++
		case '\'':
			return i, true
		case '\r', '\n':
			return i, false
		}
	}
	return len(text), false
}

func trimSpace(text string, start, end int) int {
	for end > start && (text[end-1] == ' ' || text[end-1] == '\t') {
		end--
	}
	return end
}

func proseTail(text string, start, end int) int {
	for end > start && strings.IndexByte(".,;:", text[end-1]) >= 0 {
		end--
	}
	marks := end
	for marks > start && strings.IndexByte("?!", text[marks-1]) >= 0 {
		marks--
	}
	if marks < end && marks > start && (text[marks-1] == ' ' || text[marks-1] == '\t') {
		end = marks
	}
	return trimSpace(text, start, end)
}

var enclosingPairs = map[byte]byte{'"': '"', '\'': '\'', '`': '`', '*': '*', '_': '_', '~': '~', '(': ')', '[': ']'}

func enclosedTail(text string, labelStart, start, end int) int {
	open := labelStart
	for open > 0 && enclosingPairs[text[open-1]] != 0 {
		open--
	}
	if open == labelStart {
		return end
	}
	closer := make([]byte, 0, labelStart-open)
	for i := labelStart - 1; i >= open; i-- {
		closer = append(closer, enclosingPairs[text[i]])
	}
	at := strings.Index(text[start:end], string(closer))
	if at <= 0 {
		return end
	}
	return proseTail(text, start, start+at)
}

func indentation(line string) int {
	return len(line) - len(strings.TrimLeft(line, " \t"))
}

func yamlBlock(text string, keyStart, keyLineEnd int) (int, int, bool) {
	keyColumn := keyStart - (strings.LastIndexByte(text[:keyStart], '\n') + 1)
	first, last := -1, -1
	for position := keyLineEnd; ; {
		newline := strings.IndexByte(text[position:], '\n')
		if newline < 0 {
			break
		}
		lineStart := position + newline + 1
		lineStop := strings.IndexByte(text[lineStart:], '\n')
		if lineStop < 0 {
			lineStop = len(text)
		} else {
			lineStop += lineStart
		}
		line := strings.TrimRight(text[lineStart:lineStop], " \t\r")
		if strings.TrimSpace(line) != "" {
			indent := indentation(line)
			if indent < keyColumn || indent == keyColumn && !strings.HasPrefix(line[indent:], "- ") {
				break
			}
			if first < 0 {
				first = lineStart + indent
			}
			last = lineStart + len(line)
		}
		position = lineStop
	}
	return first, last, first >= 0
}

func xmlFieldMatches(text string, names map[string]bool) []replacement {
	if !strings.Contains(text, "</") {
		return nil
	}
	var matches []replacement
	covered := 0
	for _, tag := range xmlOpenTag.FindAllStringSubmatchIndex(text, -1) {
		if tag[0] < covered {
			continue
		}
		name := text[tag[2]:tag[3]]
		local := name[strings.LastIndexAny(name, ":")+1:]
		if !names[normalizeField(local)] {
			continue
		}
		closing := strings.Index(text[tag[1]:], "</"+name+">")
		if closing < 0 {
			continue
		}
		start, end := trimmedSpan(text, tag[1], tag[1]+closing)
		covered = tag[1] + closing
		if end > start && !fieldNameValue(text[start:end], names) {
			matches = append(matches, replacement{Start: start, End: end, Kind: "FIELD", Name: local})
		}
	}
	return matches
}

func fieldMatches(text string, names map[string]bool, lenient bool) ([]replacement, error) {
	xml := xmlFieldMatches(text, names)
	if !strings.ContainsAny(text, ":=") {
		return xml, nil
	}
	var matches []replacement
	covered := 0
	for _, label := range fieldLabel.FindAllStringSubmatchIndex(text, -1) {
		if label[0] < covered {
			continue
		}
		name := ""
		quotedKey := label[2] >= 0
		switch {
		case quotedKey:
			if json.Unmarshal([]byte(`"`+text[label[2]:label[3]]+`"`), &name) != nil {
				continue
			}
		case label[6] >= 0:
			name = text[label[6]:label[7]]
		default:
			name = text[label[4]:label[5]]
		}
		if !names[normalizeField(name)] {
			continue
		}
		start := label[1]
		if start >= len(text) {
			continue
		}
		end := start
		quoted := false
		first := text[start]
		rest := strings.TrimSpace(text[start:lineEnd(text, start)])
		separator := strings.TrimRight(text[label[0]:label[1]], " \t\r\n")
		if strings.HasSuffix(separator, ":") && slices.Contains(yamlIndicators, rest) {
			if blockStart, blockEnd, ok := yamlBlock(text, label[0], lineEnd(text, start)); ok {
				covered = blockEnd
				matches = append(matches, replacement{Start: blockStart, End: blockEnd, Kind: "FIELD", Name: name})
			}
			continue
		}
		if loc := tokenPattern.FindStringIndex(text[start:]); loc != nil && loc[0] == 0 {
			covered = start + loc[1]
			continue
		}
		if first == '\'' {
			quote, closed := singleQuoted(text, start)
			covered = quote
			if closed {
				covered++
			}
			if quote > start+1 && !fieldNameValue(text[start+1:quote], names) {
				matches = append(matches, replacement{Start: start + 1, End: quote, Kind: "FIELD", Name: name})
			}
			continue
		}
		if first == '"' || first == '{' || first == '[' || quotedKey {
			decoder := json.NewDecoder(strings.NewReader(text[start:]))
			var value json.RawMessage
			if decoder.Decode(&value) != nil {
				if !lenient {
					return nil, ErrFields
				}
				if first != '"' {
					continue
				}
				quote, closed := closingQuote(text, start)
				covered = quote
				if closed {
					covered++
				}
				if quote > start+1 && !fieldNameValue(text[start+1:quote], names) {
					matches = append(matches, replacement{Start: start + 1, End: quote, Kind: "FIELD", Name: name})
				}
				continue
			}
			end = start + int(decoder.InputOffset())
			next := end
			for next < len(text) && (text[next] == ' ' || text[next] == '\t') {
				next++
			}
			unexpected := next < len(text) && !strings.ContainsRune(",;}\r\n]", rune(text[next]))
			if unexpected && !lenient {
				return nil, ErrFields
			}
			covered = end
			if unexpected && first != '"' {
				end = lineEnd(text, start)
				covered = end
				matches = append(matches, replacement{Start: start, End: end, Kind: "FIELD", Name: name})
				continue
			}
			if first == '"' {
				start++
				end--
				if fieldNameValue(text[start:end], names) {
					continue
				}
			} else {
				quoted = true
			}
		} else {
			end = enclosedTail(text, label[0], start, proseTail(text, start, lineEnd(text, start)))
			if codeValue(normalizeField(name), text[start:end]) || fieldNameValue(text[start:end], names) {
				continue
			}
			covered = end
		}
		if end > start {
			matches = append(matches, replacement{Start: start, End: end, Kind: "FIELD", Quoted: quoted, Name: name})
		}
	}
	return addUncovered(matches, xml), nil
}

func (s *Session) matches(text string) ([]replacement, error) {
	var matches []replacement
	if s.fields != nil {
		var err error
		matches, err = fieldMatches(text, s.fields, s.Policy.LenientFields)
		if err != nil {
			return nil, err
		}
	}
	if s.files != nil {
		matches = addUncovered(s.files.spans(text), matches)
	}
	var reserved, secrets []replacement
	if len(s.regex) > 0 {
		for offset := 0; offset < len(text); {
			start := strings.Index(text[offset:], Prefix)
			if start < 0 {
				break
			}
			start += offset
			end := strings.IndexByte(text[start:], ']')
			if end < 0 {
				break
			}
			offset = start + end + 1
			reserved = append(reserved, replacement{Start: start, End: offset})
		}
	}
	for index, rule := range s.regex {
		kind, pattern := "CUSTOM", ""
		if index < len(s.Policy.CustomRules) {
			pattern = s.Policy.CustomRules[index].Pattern
			if s.Policy.CustomRules[index].Secret {
				kind = "SECRET"
			}
		}
		var custom []replacement
		for _, loc := range rule.FindAllStringIndex(text, 8193) {
			if loc[0] == loc[1] {
				return nil, ErrRegex
			}
			custom = append(custom, replacement{Start: loc[0], End: loc[1], Kind: kind, Rule: pattern})
		}
		if len(matches)+len(custom) > 8192 {
			return nil, ErrLimit
		}
		if kind == "SECRET" {
			secrets = append(secrets, custom...)
		}
		// Internal tokens remain opaque when clients send their conversation history again.
		filtered := addUncovered(append([]replacement(nil), reserved...), custom)
		custom = nil
		for _, match := range filtered {
			if match.Kind != "" {
				custom = append(custom, match)
			}
		}
		matches = addUncovered(matches, custom)
	}
	known, err := s.knownIndex().matches(text, 65536)
	if err != nil {
		return nil, err
	}
	if len(known) > 0 {
		var issued []replacement
		for _, loc := range tokenPattern.FindAllStringIndex(text, -1) {
			issued = append(issued, replacement{Start: loc[0], End: loc[1]})
		}
		outside := addUncovered(issued, known)
		known = known[:0]
		for _, match := range outside {
			if match.Token != "" {
				known = append(known, match)
			}
		}
		matches = addUncovered(matches, known)
	}
	var candidates []Match
	if s.Policy.Secrets {
		detected, err := detectSecrets(text, s.Policy.DisabledSecrets)
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, detected...)
		for _, m := range detected {
			secrets = append(secrets, replacement{Start: m.Start, End: m.End})
		}
	}
	if s.Policy.Email {
		for offset := 0; offset < len(text); {
			at := strings.IndexByte(text[offset:], '@')
			if at < 0 {
				break
			}
			at += offset
			start, end := at, at+1
			for start > 0 && emailLocal(text[start-1]) {
				start--
			}
			for end < len(text) && emailDomain(text[end]) {
				end++
			}
			if loc := emailCandidate.FindStringIndex(text[start:end]); loc != nil && !codeAddress(text, start+loc[0], start+loc[1]) {
				candidates = append(candidates, Match{Start: start + loc[0], End: start + loc[1], Kind: "EMAIL", Valid: true})
			}
			offset = end
		}
	}
	if s.Policy.GUID {
		candidates = append(candidates, detectGUIDs(text)...)
	}
	candidates = append(candidates, Detect(text)...)
	if len(matches)+len(candidates) > 8192 {
		return nil, ErrLimit
	}
	var remaining []replacement
	for _, m := range candidates {
		if !s.Policy.Enabled(m.Kind) {
			continue
		}
		remaining = append(remaining, replacement{Start: m.Start, End: m.End, Kind: m.Kind, Rule: m.Rule})
	}
	result := addUncovered(matches, remaining)
	markSecret(result, secrets)
	return result, nil
}

func (s *Session) labelMatches(label, text string) []replacement {
	start, end := trimmedSpan(text, 0, len(text))
	if end == start {
		return nil
	}
	value := text[start:end]
	secret := s.Policy.Secrets && !slices.Contains(s.Policy.DisabledSecrets, labelledSecretID) && secretKey(label) && secretValue(value, true)
	switch {
	case s.fields[normalizeField(label)] && !fieldNameValue(value, s.fields):
		return []replacement{{Start: start, End: end, Kind: "FIELD", Name: label, Secret: secret}}
	case secret:
		return []replacement{{Start: start, End: end, Kind: "SECRET", Rule: SecretRuleKey(labelledSecretID)}}
	}
	return nil
}

func (s *Session) withLabel(label, text string, matches []replacement) []replacement {
	labelled := s.labelMatches(label, text)
	if len(labelled) == 0 {
		return matches
	}
	whole := labelled[0]
	for i, m := range matches {
		if m.Start == whole.Start && m.End == whole.End {
			matches[i].Secret = m.Secret || m.Kind != "SECRET" && (whole.Secret || whole.Kind == "SECRET")
			return matches
		}
	}
	return addUncovered(labelled, matches)
}

func markSecret(matches, secrets []replacement) {
	if len(secrets) == 0 {
		return
	}
	sort.Slice(secrets, func(i, j int) bool { return secrets[i].Start < secrets[j].Start })
	reach := make([]int, len(secrets))
	for i, span := range secrets {
		reach[i] = span.End
		if i > 0 {
			reach[i] = max(reach[i], reach[i-1])
		}
	}
	for i := range matches {
		before := sort.Search(len(secrets), func(j int) bool { return secrets[j].Start >= matches[i].End })
		if before > 0 && reach[before-1] > matches[i].Start {
			matches[i].Secret = true
		}
	}
}

func detectGUIDs(text string) []Match {
	var matches []Match
	for offset := 0; offset < len(text); {
		dash := strings.IndexByte(text[offset:], '-')
		if dash < 0 {
			break
		}
		dash += offset
		start, end := dash-8, dash+28
		if start >= 0 && end <= len(text) && boundary(text, start, end) && guidCandidate.MatchString(text[start:end]) {
			matches = append(matches, Match{Start: start, End: end, Kind: "GUID", Valid: true})
			offset = end
		} else {
			offset = dash + 1
		}
	}
	return matches
}

func GUIDs(text string) []string {
	var values []string
	for _, m := range detectGUIDs(text) {
		values = append(values, text[m.Start:m.End])
	}
	return values
}

func emailDomain(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '-'
}
func emailLocal(c byte) bool { return emailDomain(c) || c == '_' || c == '%' || c == '+' }
