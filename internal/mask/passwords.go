package mask

import (
	"regexp"
	"slices"
	"strings"
	"unicode"
)

const labelledSecretID = "labelled-password"

// labelledSecretPattern finds "key = value" pairs (and SQL "PASSWORD '…'"); labelledSecret decides
// from the key's words and the value's shape whether the value is a password, token or secret.
const labelledSecretPattern = `(?i)` + labelledSecretBody

const labelledSecretBody = `\b(password|identified[\t ]+by)[\t ]+('[^'\r\n]{0,256}')|(--)?\b([a-z_][a-z0-9_.-]{0,80})["']?[\t ]*(?::[\t ]*[a-z_][\w.<>\[\]]{0,40}[\t ]*)?(?::=|=>|=|:)[\t ]*("(?:[^"\\\r\n]|\\.){0,256}"|'[^'\r\n]{0,256}'|[^\s"';&,]{0,256})`

// labelledSecretAt matches at the start of a label only; labelledSecrets runs it where a label
// word occurs, which is far cheaper than searching the whole text with the open pattern.
var labelledSecretAt = regexp.MustCompile(`(?i)^(?:` + labelledSecretBody + `)`)

// labelWords are the words a secret label must contain, lowercase.
var labelWords = []string{"pass", "pwd", "secret", "token", "key", "identified"}

// labelWindow covers a label, its type annotation, the separator and the longest value.
const labelWindow = 1024

var secretWords = map[string]bool{"password": true, "passwd": true, "passphrase": true, "secret": true, "token": true, "apikey": true, "accesskey": true, "secretkey": true}

// "pass" and "pwd" alone are too ambiguous (a test result, the PWD directory variable); they count
// only as part of a longer key such as DB_PASS or MONGO_PWD.
var shortSecretWords = map[string]bool{"pass": true, "pwd": true}

var keyQualifiers = map[string]bool{"api": true, "access": true, "private": true, "signing": true, "encryption": true, "master": true}

// Words around the secret word that make the key describe a secret instead of holding one:
// token_endpoint, password_min_length, existingSecret, nextPageToken.
var secretMetaWords = map[string]bool{
	"url": true, "uri": true, "endpoint": true, "file": true, "path": true, "dir": true, "location": true,
	"type": true, "name": true, "length": true, "len": true, "size": true, "count": true, "ttl": true,
	"expiry": true, "expires": true, "expiration": true, "lifetime": true, "validity": true, "timeout": true,
	"header": true, "policy": true, "min": true, "max": true, "enabled": true, "enable": true, "required": true,
	"regex": true, "pattern": true, "field": true, "label": true, "hint": true, "prompt": true, "ref": true,
	"encoder": true, "encoding": true, "algorithm": true, "format": true, "mode": true, "provider": true,
	"manager": true, "service": true, "strategy": true, "validator": true, "generator": true, "env": true,
	"var": true, "variable": true, "source": true, "limit": true, "version": true, "alphabet": true,
	"glyphs": true, "chars": true, "charset": true, "characters": true, "prefix": true, "suffix": true,
	"separator": true, "placeholder": true,
}

var secretLeadWords = map[string]bool{
	"existing": true, "use": true, "enable": true, "enabled": true, "has": true, "is": true, "show": true,
	"hide": true, "forgot": true, "require": true, "required": true, "allow": true, "max": true, "min": true,
	"no": true, "skip": true, "page": true, "next": true, "continuation": true, "cancellation": true,
	"num": true, "total": true,
}

var placeholderValues = map[string]bool{
	"none": true, "null": true, "nil": true, "empty": true, "undefined": true, "true": true, "false": true,
	"yes": true, "no": true, "on": true, "off": true, "redacted": true, "hidden": true, "masked": true,
	"n/a": true, "na": true, "tbd": true, "todo": true, "required": true, "optional": true, "string": true,
	"str": true, "secret": true, "password": true, "passwd": true, "token": true,
}

func keyWords(key string) []string {
	var words []string
	var word []rune
	runes := []rune(key)
	flush := func() {
		if len(word) > 0 {
			words = append(words, strings.ToLower(string(word)))
			word = word[:0]
		}
	}
	for i, r := range runes {
		switch {
		case r == '.' || r == '_' || r == '-':
			flush()
			continue
		case unicode.IsUpper(r) && i > 0 && (unicode.IsLower(runes[i-1]) || unicode.IsDigit(runes[i-1])):
			flush()
		case unicode.IsUpper(r) && i > 0 && unicode.IsUpper(runes[i-1]) && i+1 < len(runes) && unicode.IsLower(runes[i+1]):
			flush()
		}
		word = append(word, r)
	}
	flush()
	return words
}

func secretKey(key string) bool {
	words := keyWords(strings.TrimPrefix(key, "--"))
	at := -1
	for i, word := range words {
		switch {
		case secretWords[word], shortSecretWords[word] && len(words) > 1:
			at = i
		case word == "key" && i > 0 && keyQualifiers[words[i-1]]:
			at = i
		case strings.HasSuffix(word, "password"), strings.HasSuffix(word, "passwd"), strings.HasSuffix(word, "secret"), strings.HasSuffix(word, "token"):
			at = i
		}
	}
	if at < 0 {
		return false
	}
	for _, word := range words[:at] {
		if secretLeadWords[word] {
			return false
		}
	}
	for _, word := range words[at+1:] {
		if secretMetaWords[word] {
			return false
		}
	}
	return true
}

func secretValue(value string, quoted bool) bool {
	if value == "" || placeholderValues[strings.ToLower(value)] || codeTypes[value] {
		return false
	}
	for _, prefix := range []string{"${", "{{", "#{", "%(", "$(", "/", "./", "../", "~/"} {
		if strings.HasPrefix(value, prefix) {
			return false
		}
	}
	if strings.HasPrefix(value, "<") && strings.HasSuffix(value, ">") || strings.Trim(value, "*xX.-_#|>!?") == "" || strings.HasPrefix(value, "=") {
		return false
	}
	return !prose(value) && (quoted || !codeValue("password", value))
}

// prose tells a sentence ("Blocked · unknown token") from a passphrase-like value ("p@ss w0rd").
func prose(value string) bool {
	words := 0
	for _, word := range strings.Fields(value) {
		if strings.IndexFunc(strings.TrimRight(word, ".,:;!?"), func(r rune) bool { return !unicode.IsLetter(r) }) < 0 {
			words++
		}
	}
	return words >= 2
}

// strongSecret tells a password worth masking everywhere from a weak one ("postgres", "admin"):
// a weak value learned by the session would also be masked in every command and file that uses
// the same ordinary word.
func strongSecret(value string) bool {
	var lower, upper, digit, other bool
	for _, r := range value {
		switch {
		case unicode.IsLower(r):
			lower = true
		case unicode.IsUpper(r):
			upper = true
		case unicode.IsDigit(r):
			digit = true
		default:
			other = true
		}
	}
	classes := 0
	for _, present := range []bool{lower, upper, digit, other} {
		if present {
			classes++
		}
	}
	return len(value) >= 8 && classes >= 2
}

// learnable reports whether a masked value joins the session's known values, which are then masked
// wherever they appear, even without the label or rule that found them.
func learnable(kind, rule, value string) bool {
	return contextKinds[kind] && len(value) >= MinKnownValue && (rule != SecretRuleKey(labelledSecretID) || strongSecret(value))
}

func labelledSecrets(text string) [][]int {
	lower := asciiLower(text)
	var starts []int
	for _, word := range labelWords {
		for from := 0; ; {
			at := strings.Index(lower[from:], word)
			if at < 0 {
				break
			}
			at += from
			starts = append(starts, labelStart(text, at))
			from = at + len(word)
		}
	}
	slices.Sort(starts)
	var spans [][]int
	covered := 0
	for _, start := range slices.Compact(starts) {
		if len(spans) > maxSecretMatches {
			break
		}
		if start < covered {
			continue
		}
		from := start
		if from >= 2 && text[from-2:from] == "--" {
			from -= 2
		}
		loc := labelledSecretAt.FindStringSubmatchIndex(text[from:min(len(text), from+labelWindow)])
		if loc == nil {
			continue
		}
		for i := range loc {
			if loc[i] >= 0 {
				loc[i] += from
			}
		}
		if span, ok := labelledSecret(text, loc); ok {
			spans = append(spans, span)
			covered = max(loc[1], span[1])
		}
	}
	return spans
}

// labelStart walks back from a label word to the first letter or underscore of its key.
func labelStart(text string, at int) int {
	start := at
	for start > 0 && keyByte(text[start-1]) {
		start--
	}
	for start < at && !(text[start] == '_' || text[start]|0x20 >= 'a' && text[start]|0x20 <= 'z') {
		start++
	}
	return start
}

func keyByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '.' || c == '-'
}

// asciiLower lowercases ASCII letters only, so byte offsets stay those of text.
func asciiLower(text string) string {
	b := []byte(text)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

// labelledSecret returns the value's span for a submatch of labelledSecretPattern. A quoted value
// counts anywhere; a bare one only where config files put it (a line of its own, a --flag, or a
// connection or query string), so code such as "password = input" is left alone.
func labelledSecret(text string, loc []int) ([]int, bool) {
	if loc[4] >= 0 {
		start, end := loc[4]+1, loc[5]-1
		return []int{start, end}, secretValue(text[start:end], true)
	}
	keyStart, valueStart, valueEnd := loc[8], loc[10], loc[11]
	if !secretKey(text[keyStart:loc[9]]) || valueStart == valueEnd {
		return nil, false
	}
	// A key inside a string literal that is still open ("OPENAI_API_KEY=" + value) makes the next
	// quote the literal's end, not the start of a value.
	insideLiteral := keyStart > 0 && strings.IndexByte(`"'`, text[keyStart-1]) >= 0 && text[loc[9]] != text[keyStart-1]
	if first := text[valueStart]; (first == '"' || first == '\'') && valueEnd-valueStart >= 2 {
		return []int{valueStart + 1, valueEnd - 1}, !insideLiteral && secretValue(text[valueStart+1:valueEnd-1], true)
	}
	labelStart := keyStart
	if loc[6] >= 0 {
		labelStart = loc[6]
	}
	lineStart := strings.LastIndexByte(text[:labelStart], '\n') + 1
	lineStart += lineNumberPrefix(text[lineStart:labelStart])
	prefix := strings.TrimRight(strings.TrimSpace(text[lineStart:labelStart]), `"'`)
	switch {
	case loc[6] >= 0:
	case prefix == "" || prefix == "export" || prefix == "-":
		valueEnd = lineEnd(text, valueStart)
		if insideLiteral {
			if quote := strings.IndexByte(text[valueStart:valueEnd], text[keyStart-1]); quote >= 0 {
				valueEnd = valueStart + quote
			}
		}
		if comment := strings.Index(text[valueStart:valueEnd], " #"); comment >= 0 {
			valueEnd = trimSpace(text, valueStart, valueStart+comment)
		}
		value := text[valueStart:valueEnd]
		if strings.ContainsAny(value, " \t") || strings.HasSuffix(value, ";") || strings.HasSuffix(value, ",") {
			return nil, false
		}
	case strings.ContainsAny(prefix[len(prefix)-1:], ";&?") && !strings.HasSuffix(prefix, "&&"):
	default:
		return nil, false
	}
	return []int{valueStart, valueEnd}, secretValue(text[valueStart:valueEnd], false)
}
