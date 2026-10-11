package mask

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

var (
	ErrLimit   = errors.New("masking capacity exceeded")
	ErrSession = errors.New("session expired or unavailable; start a new conversation")
	ErrToken   = errors.New("unknown reserved token in input")
	ErrPaths   = errors.New("invalid protected path; use up to 16 absolute or ~/ paths, not / or ~ itself")
	ErrFolders = errors.New("a protected folder is not indexed completely")
)

const Prefix = "[PP_"

const tokenAlphabet = "abcdefghijkmnpqrstuvwxyz23456789"
const tokenIDLength = 6

// tokenPattern matches only strings this package generates, so arbitrary "[PP_...]" in user text is left alone.
var tokenPattern = regexp.MustCompile(`\[PP_[A-Z]+_[a-z2-9]{6}\]`)

// TokenID returns a short unbiased id (alphabet size 32 divides 256 evenly).
func TokenID() (string, error) {
	var b [tokenIDLength]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	for i := range b {
		b[i] = tokenAlphabet[b[i]&31]
	}
	return string(b[:]), nil
}

type Counts struct {
	PESEL          int            `json:"pesel"`
	VIN            int            `json:"vin"`
	Fields         int            `json:"fields"`
	Email          int            `json:"email"`
	GUID           int            `json:"guid"`
	Custom         int            `json:"custom"`
	Secret         int            `json:"secret"`
	File           int            `json:"file"`
	New            int            `json:"new"`
	TokenOverhead  int            `json:"token_overhead"`
	FieldNames     map[string]int `json:"field_names,omitempty"`
	NewKinds       map[string]int `json:"new_kinds,omitempty"`
	NewFields      map[string]int `json:"new_field_names,omitempty"`
	ContextKinds   map[string]int `json:"context_kinds,omitempty"`
	TypedKinds     map[string]int `json:"typed_kinds,omitempty"`
	UnknownBlocks  map[string]int `json:"unknown_blocks,omitempty"`
	BlockedTexts   int            `json:"blocked_texts,omitempty"`
	Attachments    int            `json:"attachments,omitempty"`
	MaskedFolders  map[string]int `json:"masked_folders,omitempty"`
	BlockedFolders map[string]int `json:"blocked_folders,omitempty"`
	// RuleHits and RuleNew count matches and new values per custom regex. They are never
	// serialized, because a pattern can contain the very word it protects.
	RuleHits map[string]int `json:"-"`
	RuleNew  map[string]int `json:"-"`
	// RulePreviews holds partly hidden new values per custom regex, for the rule history.
	RulePreviews map[string][]string `json:"-"`
	NewTokens    []string            `json:"-"`
}

var kindKeys = map[string]string{"VIN": "vin", "PESEL": "pesel", "EMAIL": "email", "GUID": "guid", "CUSTOM": "custom", "SECRET": "secret", "FILE": "file", "FIELD": "fields"}

func addCounts(into map[string]int, from map[string]int) map[string]int {
	for key, n := range from {
		if into == nil {
			into = make(map[string]int)
		}
		into[key] += n
	}
	return into
}

func addPreviews(into map[string][]string, from map[string][]string) map[string][]string {
	for key, values := range from {
		if into == nil {
			into = make(map[string][]string)
		}
		into[key] = append(into[key], values...)
	}
	return into
}

func (c Counts) Add(other Counts) Counts {
	c.PESEL += other.PESEL
	c.VIN += other.VIN
	c.Fields += other.Fields
	c.Email += other.Email
	c.GUID += other.GUID
	c.Custom += other.Custom
	c.Secret += other.Secret
	c.File += other.File
	c.New += other.New
	c.TokenOverhead += other.TokenOverhead
	c.FieldNames = addCounts(addCounts(nil, c.FieldNames), other.FieldNames)
	c.NewKinds = addCounts(addCounts(nil, c.NewKinds), other.NewKinds)
	c.NewFields = addCounts(addCounts(nil, c.NewFields), other.NewFields)
	c.ContextKinds = addCounts(addCounts(nil, c.ContextKinds), other.ContextKinds)
	c.TypedKinds = addCounts(addCounts(nil, c.TypedKinds), other.TypedKinds)
	c.NewTokens = append(slices.Clip(c.NewTokens), other.NewTokens...)
	c.UnknownBlocks = addCounts(addCounts(nil, c.UnknownBlocks), other.UnknownBlocks)
	c.BlockedTexts += other.BlockedTexts
	c.Attachments += other.Attachments
	c.RuleHits = addCounts(addCounts(nil, c.RuleHits), other.RuleHits)
	c.RuleNew = addCounts(addCounts(nil, c.RuleNew), other.RuleNew)
	c.RulePreviews = addPreviews(addPreviews(nil, c.RulePreviews), other.RulePreviews)
	c.MaskedFolders = addCounts(addCounts(nil, c.MaskedFolders), other.MaskedFolders)
	c.BlockedFolders = addCounts(addCounts(nil, c.BlockedFolders), other.BlockedFolders)
	return c
}

func (c Counts) repeated() Counts {
	c.New, c.NewKinds, c.NewFields, c.RuleNew, c.RulePreviews, c.MaskedFolders, c.NewTokens = 0, nil, nil, nil, nil, nil, nil
	return c
}

func (c Counts) FolderAccess() bool {
	return len(c.MaskedFolders) > 0 || len(c.BlockedFolders) > 0
}

func (c Counts) Total() int {
	return c.VIN + c.PESEL + c.Fields + c.Email + c.GUID + c.Custom + c.Secret + c.File + c.BlockedTexts
}

type Policy struct {
	GUID           bool     `json:"guid"`
	VIN            bool     `json:"vin"`
	PESEL          bool     `json:"pesel"`
	Email          bool     `json:"email"`
	Fields         bool     `json:"fields"`
	Secrets        bool     `json:"secrets"`
	CustomFields   []string `json:"custom_fields,omitempty"`
	DisabledFields []string `json:"disabled_fields,omitempty"`
	// DisabledSecrets lists the IDs of built-in secret patterns that are switched off.
	DisabledSecrets []string    `json:"disabled_secrets,omitempty"`
	CustomRules     []RegexRule `json:"custom_rules,omitempty"`
	// CustomOff switches CustomRules off without deleting them; when omitted they are on.
	CustomOff      bool     `json:"custom_off,omitempty"`
	ProtectedPaths []string `json:"protected_paths,omitempty"`
	// BlockedPaths is the subset of ProtectedPaths whose content is removed instead of masked.
	BlockedPaths []string `json:"blocked_paths,omitempty"`
	// TrustedPaths are folders whose content is sent unmasked, such as test fixtures with fake data.
	TrustedPaths []string `json:"trusted_paths,omitempty"`

	LenientFields bool `json:"lenient_fields,omitempty"`
}

func DefaultPolicy() Policy {
	return Policy{VIN: true, GUID: true, Email: true, Fields: true, Secrets: true}
}
func ValidatePolicy(policy Policy) error {
	if _, err := FieldNames(policy.CustomFields, policy.DisabledFields); err != nil {
		return err
	}
	if _, err := CompileRules(policy.CustomRules); err != nil {
		return err
	}
	if !validSecretIDs(policy.DisabledSecrets) {
		return ErrSecrets
	}
	if !validPaths(policy.ProtectedPaths) || !validPaths(policy.TrustedPaths) {
		return ErrPaths
	}
	for _, trusted := range policy.TrustedPaths {
		if slices.ContainsFunc(policy.ProtectedPaths, func(entry string) bool { return path.Clean(entry) == path.Clean(trusted) }) {
			return ErrPaths
		}
	}
	for _, blocked := range policy.BlockedPaths {
		if !slices.ContainsFunc(policy.ProtectedPaths, func(entry string) bool { return path.Clean(entry) == path.Clean(blocked) }) {
			return ErrPaths
		}
	}
	if !policy.VIN && !policy.PESEL && !policy.Email && !policy.Fields && !policy.GUID && !policy.Secrets && (len(policy.CustomRules) == 0 || policy.CustomOff) && len(policy.ProtectedPaths) == 0 {
		return ErrSession
	}
	return nil
}

func (p Policy) clone() Policy {
	p.CustomFields = slices.Clone(p.CustomFields)
	p.DisabledFields = slices.Clone(p.DisabledFields)
	p.DisabledSecrets = slices.Clone(p.DisabledSecrets)
	p.CustomRules = slices.Clone(p.CustomRules)
	p.ProtectedPaths = slices.Clone(p.ProtectedPaths)
	p.BlockedPaths = slices.Clone(p.BlockedPaths)
	p.TrustedPaths = slices.Clone(p.TrustedPaths)
	return p
}

func validPaths(paths []string) bool {
	if len(paths) > 16 {
		return false
	}
	seen := make(map[string]bool, len(paths))
	for _, entry := range paths {
		if len(entry) > 512 || strings.ContainsRune(entry, 0) || !strings.HasPrefix(entry, "/") && !strings.HasPrefix(entry, "~/") {
			return false
		}
		clean := path.Clean(entry)
		if clean == "/" || clean == "~" || seen[clean] {
			return false
		}
		seen[clean] = true
	}
	return true
}

func (p Policy) Enabled(kind string) bool {
	return kind == "VIN" && p.VIN || kind == "PESEL" && p.PESEL || kind == "EMAIL" && p.Email || kind == "GUID" && p.GUID || kind == "SECRET" && p.Secrets
}

type Session struct {
	mu                          sync.Mutex
	forward, reverse            map[string]string
	bytes, maxBytes, maxEntries int
	closed                      bool
	Policy                      Policy
	fields                      map[string]bool
	quoted                      map[string]bool
	initErr                     error
	regex                       []*regexp.Regexp
	files                       *FileIndex
	labels                      map[string]string
	foreign                     map[string]bool
	secret                      map[string]bool
	known                       *knownIndex
	generation                  int
	memo                        map[memoKey]memoEntry
	memoBytes                   int
	contextValues               int
	seen                        map[[32]byte]struct{}
	rules                       map[string]string
}

// memoEntry caches the masked form of a text, so a conversation re-sent on every turn is scanned
// only for its new part. Without a destination it holds texts that produced no new mappings and
// expires when a label-detected value is learned. For a destination it holds the form the text was
// first sent with until the rules change: the provider already has that form, and re-masking it
// with values learned later would only change the history and break the provider's prompt cache.
type memoEntry struct {
	generation, revision int
	masked               string
	counts               Counts
}

type memoKey struct {
	destination string
	digest      [32]byte
}

const minMemoText = 256
const maxMemoBytes = 32 << 20
const entryOverhead = 64

func NewSession(maxEntries, maxBytes int, policy Policy) *Session {
	s := &Session{forward: make(map[string]string), reverse: make(map[string]string), quoted: make(map[string]bool), foreign: make(map[string]bool), secret: make(map[string]bool), memo: make(map[memoKey]memoEntry), seen: make(map[[32]byte]struct{}), rules: make(map[string]string), maxEntries: maxEntries, maxBytes: maxBytes}
	s.initErr = s.apply(policy.clone())
	return s
}

func (s *Session) knownIndex() *knownIndex {
	if s.known == nil || s.known.revision != s.contextValues {
		s.known = buildKnown(s.reverse, s.rules, s.contextValues)
	}
	return s.known
}

func (s *Session) apply(policy Policy) error {
	s.generation++
	s.Policy = policy
	s.fields, s.regex, s.labels = nil, nil, nil
	names, err := FieldNames(policy.CustomFields, policy.DisabledFields)
	if err != nil {
		return err
	}
	regex, err := CompileRules(policy.CustomRules)
	if err != nil {
		return err
	}
	if !policy.CustomOff {
		s.regex = regex
	}
	if policy.Fields {
		s.fields = names
		s.labels = fieldLabels(policy.CustomFields, policy.DisabledFields)
	}
	return nil
}

func (s *Session) SetRules(policy Policy) error {
	if err := ValidatePolicy(policy); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrSession
	}
	policy = policy.clone()
	policy.LenientFields = s.Policy.LenientFields
	s.initErr = s.apply(policy)
	return s.initErr
}

func (s *Session) SetFiles(files *FileIndex) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files = files
	s.generation++
}

func (s *Session) fieldLabel(name string) string {
	if label := s.labels[normalizeField(name)]; label != "" {
		return label
	}
	return name
}

func (s *Session) Enabled(kind string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Policy.Enabled(kind)
}

func RandomID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func (s *Session) Mask(texts []string) ([]string, Counts, error) {
	return s.MaskExcept(texts, nil)
}

func (s *Session) MaskExcept(texts []string, public []string) ([]string, Counts, error) {
	return s.MaskFor("", texts, public)
}

// MaskFor masks texts sent to destination; a text already sent there keeps its first masked form
// until the rules change (see memoEntry).
func (s *Session) MaskFor(destination string, texts []string, public []string) ([]string, Counts, error) {
	values := make([]Text, len(texts))
	for i, text := range texts {
		values[i] = Text{Value: text}
	}
	return s.MaskTexts(destination, values, public)
}

type Text struct {
	Value   string
	Label   string
	Literal bool
}

func (t Text) memoText() string {
	if t.Label == "" && !t.Literal {
		return t.Value
	}
	return fmt.Sprintf("%s\x00%t\x00%s", t.Label, t.Literal, t.Value)
}

func (s *Session) MaskTexts(destination string, texts []Text, public []string) ([]string, Counts, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.initErr != nil {
		return nil, Counts{}, s.initErr
	}
	if s.closed {
		return nil, Counts{}, ErrSession
	}
	if !s.files.ready(s.Policy.ProtectedPaths) {
		return nil, Counts{}, ErrFolders
	}
	first, err := s.maskPass(destination, texts, public)
	if err != nil {
		return nil, Counts{}, err
	}
	if !first.learnedContext {
		s.commit(first)
		return first.out, first.counts, nil
	}
	// A value learned through its label in this request must also be masked where it appears
	// without the label in the same request, so the request is masked again with it known.
	first.memos = nil
	s.commit(first)
	second, err := s.maskPass(destination, texts, public)
	if err != nil {
		return nil, Counts{}, err
	}
	s.commit(second)
	second.counts.New, second.counts.NewKinds, second.counts.NewFields = first.counts.New+second.counts.New, addCounts(addCounts(nil, first.counts.NewKinds), second.counts.NewKinds), addCounts(addCounts(nil, first.counts.NewFields), second.counts.NewFields)
	second.counts.RuleNew = addCounts(addCounts(nil, first.counts.RuleNew), second.counts.RuleNew)
	second.counts.RulePreviews = addPreviews(addPreviews(nil, first.counts.RulePreviews), second.counts.RulePreviews)
	second.counts.MaskedFolders = addCounts(addCounts(nil, first.counts.MaskedFolders), second.counts.MaskedFolders)
	second.counts.NewTokens = append(slices.Clip(first.counts.NewTokens), second.counts.NewTokens...)
	second.counts.BlockedFolders = addCounts(addCounts(nil, first.counts.BlockedFolders), second.counts.BlockedFolders)
	return second.out, second.counts, nil
}

type maskPass struct {
	out            []string
	counts         Counts
	staged         map[string]string
	original       map[string]string
	quoted         map[string]bool
	foreign        map[string]bool
	secret         map[string]bool
	memos          map[memoKey]memoEntry
	rules          map[string]string
	seen           map[[32]byte]struct{}
	addedBytes     int
	learnedContext bool
}

func (p *maskPass) firstSight(s *Session, key [32]byte) bool {
	_, seen := s.seen[key]
	_, staged := p.seen[key]
	p.seen[key] = struct{}{}
	return !seen && !staged
}

func (s *Session) maskPass(destination string, texts []Text, public []string) (*maskPass, error) {
	p := &maskPass{out: make([]string, len(texts)), staged: make(map[string]string), original: make(map[string]string), quoted: make(map[string]bool), foreign: make(map[string]bool), secret: make(map[string]bool), memos: make(map[memoKey]memoEntry), seen: make(map[[32]byte]struct{}), rules: make(map[string]string)}
	sent := destination != ""
	for i, item := range texts {
		text := item.Value
		memoized := sent || len(text) >= minMemoText
		key := memoKey{destination: destination}
		if memoized {
			key.digest = sha256.Sum256([]byte(item.memoText()))
			if cached, ok := s.memo[key]; ok && cached.generation == s.generation && (sent || cached.revision == s.contextValues) {
				p.out[i] = cached.masked
				p.counts = p.counts.Add(cached.counts)
				continue
			}
		}
		for _, token := range tokenPattern.FindAllString(text, -1) {
			if _, issued := s.reverse[token]; issued || s.foreign[token] || p.foreign[token] {
				continue
			}
			p.foreign[token] = true
			p.addedBytes += len(token) + entryOverhead
			if p.entries(s) > s.maxEntries || s.bytes+p.addedBytes > s.maxBytes {
				return nil, ErrLimit
			}
		}
		if folder, blocked := s.files.blockedFolder(text); blocked {
			p.out[i] = BlockedNotice
			part := Counts{BlockedTexts: 1, TokenOverhead: EstimateTokens(BlockedNotice) - EstimateTokens(text)}
			if p.firstSight(s, sha256.Sum256([]byte("text\x00"+text))) {
				part.BlockedFolders = map[string]int{folder: 1}
			}
			p.counts = p.counts.Add(part)
			continue
		}
		var b strings.Builder
		var part Counts
		last := 0
		usedPublic := false
		matches, err := s.matches(text)
		if err != nil {
			return nil, err
		}
		if item.Label != "" {
			matches = s.withLabel(item.Label, text, matches)
		}
		if item.Literal && len(matches) > 0 {
			matches = []replacement{wholeLiteral(text, matches)}
		}
		for _, m := range matches {
			value := text[m.Start:m.End]
			if m.Kind != "FILE" && s.files.trustedValue(value) {
				continue
			}
			token := m.Token
			if token == "" {
				if _, known := s.reverse[value]; known {
					continue
				}
				if slices.Contains(public, value) {
					usedPublic = true
					continue
				}
				key := "text:" + value
				if m.Quoted {
					key = "json:" + value
				}
				token = s.forward[key]
				if token == "" {
					token = p.staged[key]
				}
				if token == "" {
					if token, err = s.newToken(m.Kind, p); err != nil {
						return nil, err
					}
					p.addedBytes += len(key) + len(value) + len(token)
					if p.entries(s)+1 > s.maxEntries || s.bytes+p.addedBytes > s.maxBytes {
						return nil, ErrLimit
					}
					p.staged[key] = token
					p.original[token] = value
					p.quoted[token] = m.Quoted
					p.learnedContext = p.learnedContext || learnable(m.Kind, m.Rule, value)
					part.New++
					part.NewKinds = addCounts(part.NewKinds, map[string]int{kindKeys[m.Kind]: 1})
					part.NewTokens = append(part.NewTokens, token)
					if m.Kind == "FIELD" {
						part.NewFields = addCounts(part.NewFields, map[string]int{s.fieldLabel(m.Name): 1})
					}
					if m.Kind == "FILE" {
						if part.MaskedFolders == nil {
							part.MaskedFolders = make(map[string]int)
						}
						part.MaskedFolders[m.Name] = 1
					}
					if m.Rule != "" {
						p.rules[token] = m.Rule
						part.RuleNew = addCounts(part.RuleNew, map[string]int{m.Rule: 1})
						part.RulePreviews = addPreviews(part.RulePreviews, map[string][]string{m.Rule: {Preview(value, m.Kind == "SECRET")}})
					}
				}
				if m.Kind == "FIELD" {
					part.FieldNames = addCounts(part.FieldNames, map[string]int{s.fieldLabel(m.Name): 1})
				}
				if m.Rule != "" {
					part.RuleHits = addCounts(part.RuleHits, map[string]int{m.Rule: 1})
				}
			} else if rule := s.rules[token]; rule != "" {
				part.RuleHits = addCounts(part.RuleHits, map[string]int{rule: 1})
			}
			if m.Secret && tokenKind(token) != "SECRET" && !s.secret[token] {
				p.secret[token] = true
			}
			b.WriteString(text[last:m.Start])
			if m.Quoted && m.Token == "" {
				encoded, _ := json.Marshal(token)
				b.Write(encoded)
			} else {
				b.WriteString(token)
			}
			last = m.End
			part.countKind(m.Kind)
		}
		b.WriteString(text[last:])
		p.out[i] = b.String()
		if last > 0 {
			part.TokenOverhead = EstimateTokens(p.out[i]) - EstimateTokens(text)
		}
		if memoized && !usedPublic && (sent || part.New == 0) {
			p.memos[key] = memoEntry{generation: s.generation, masked: p.out[i], counts: part.repeated()}
		}
		p.counts = p.counts.Add(part)
	}
	return p, nil
}

func (p *maskPass) entries(s *Session) int {
	return len(s.forward) + len(s.foreign) + len(p.staged) + len(p.foreign)
}

func wholeLiteral(text string, matches []replacement) replacement {
	first := matches[0]
	secret := slices.ContainsFunc(matches, func(m replacement) bool { return m.Secret || m.Kind == "SECRET" })
	return replacement{Start: 0, End: len(text), Kind: first.Kind, Name: first.Name, Rule: first.Rule, Quoted: true, Secret: secret && first.Kind != "SECRET"}
}

func (s *Session) newToken(kind string, p *maskPass) (string, error) {
	for {
		id, err := TokenID()
		if err != nil {
			return "", err
		}
		token := Prefix + kind + "_" + id + "]"
		_, issued := s.reverse[token]
		_, pending := p.original[token]
		if !issued && !pending && !s.foreign[token] && !p.foreign[token] {
			return token, nil
		}
	}
}

// commit publishes a pass; memo entries without a destination are valid until the next
// label-detected value is learned.
func (s *Session) commit(p *maskPass) {
	for key, token := range p.staged {
		s.forward[key] = token
		s.reverse[token] = p.original[token]
		s.quoted[token] = p.quoted[token]
		if learnable(tokenKind(token), p.rules[token], p.original[token]) {
			s.contextValues++
		}
	}
	for token := range p.foreign {
		s.foreign[token] = true
	}
	for token := range p.secret {
		s.secret[token] = true
	}
	for token, rule := range p.rules {
		s.rules[token] = rule
	}
	s.bytes += p.addedBytes
	s.remember(p.seen)
	for key, entry := range p.memos {
		if key.destination == "" && len(p.staged) > 0 {
			continue
		}
		entry.revision = s.contextValues
		size := len(entry.masked) + len(key.destination) + entryOverhead
		if s.memoBytes+size > maxMemoBytes {
			clear(s.memo)
			s.memoBytes = 0
		}
		if _, exists := s.memo[key]; !exists {
			s.memoBytes += size
		}
		s.memo[key] = entry
	}
}

const maxSeen = 4096

func (s *Session) remember(keys map[[32]byte]struct{}) {
	if len(s.seen)+len(keys) > maxSeen {
		clear(s.seen)
	}
	for key := range keys {
		s.seen[key] = struct{}{}
	}
}

func (s *Session) RefusedAccess(calls map[string][]string) Counts {
	s.mu.Lock()
	defer s.mu.Unlock()
	var counts Counts
	if s.files == nil || s.closed {
		return counts
	}
	for id, inputs := range calls {
		key := sha256.Sum256([]byte("tool\x00" + id))
		if _, seen := s.seen[key]; seen {
			continue
		}
		s.remember(map[[32]byte]struct{}{key: {}})
		referenced := make(map[string]int)
		for _, input := range inputs {
			for _, folder := range s.files.referencedFolders(input) {
				if s.files.folders[folder].blocked {
					referenced[s.files.folders[folder].label] = 1
				}
			}
		}
		counts.BlockedFolders = addCounts(counts.BlockedFolders, referenced)
	}
	return counts
}

func (c *Counts) countKind(kind string) {
	switch kind {
	case "PESEL":
		c.PESEL++
	case "VIN":
		c.VIN++
	case "GUID":
		c.GUID++
	case "EMAIL":
		c.Email++
	case "CUSTOM":
		c.Custom++
	case "SECRET":
		c.Secret++
	case "FILE":
		c.File++
	default:
		c.Fields++
	}
}

func (s *Session) Restore(text string) string { out, _ := s.restore(text, false, nil, nil); return out }

func (s *Session) RestoreJSON(text string) string {
	out, _ := s.restore(text, true, nil, nil)
	return out
}

// RestoreExcept leaves placeholders of the withheld kinds in place, adds them to kept when it is
// not nil and reports how many it kept.
func (s *Session) RestoreExcept(text string, inJSON bool, withhold, kept map[string]bool) (string, int) {
	return s.restore(text, inJSON, withhold, kept)
}

func (s *Session) MarkKept(text string, kept map[string]bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, token := range tokenPattern.FindAllString(text, -1) {
		if _, known := s.reverse[token]; known {
			kept[token] = true
		}
	}
}

func (s *Session) restore(text string, inJSON bool, withhold, kept map[string]bool) (string, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	withheld := 0
	var b strings.Builder
	for {
		i := strings.Index(text, Prefix)
		if i < 0 {
			b.WriteString(text)
			break
		}
		j := strings.IndexByte(text[i:], ']')
		if j < 0 {
			b.WriteString(text)
			break
		}
		end := i + j + 1
		token := text[i:end]
		value, known := s.reverse[token]
		kind := tokenKind(token)
		if s.secret[token] {
			kind = "SECRET"
		}
		if known && withhold[kind] {
			known = false
			withheld++
			if kept != nil {
				kept[token] = true
			}
		}
		before := i
		if quote := quoteBefore(text, i); known && s.quoted[token] && quote != "" && strings.HasPrefix(text[end:], quote) {
			before -= len(quote)
			end += len(quote)
			for level := len(quote); level > 1; level /= 2 {
				value = escapeJSON(value)
			}
		}
		b.WriteString(text[:before])
		switch {
		case !known:
			b.WriteString(token)
		case inJSON && before == i:
			b.WriteString(escapeJSON(value))
		default:
			b.WriteString(value)
		}
		text = text[end:]
	}
	return b.String(), withheld
}

// quoteBefore returns the quote ending at i as it is written at its JSON string level: " for
// none, \" inside one string, \\\" inside two, and so on.
func quoteBefore(text string, i int) string {
	if i == 0 || text[i-1] != '"' {
		return ""
	}
	backslashes := 0
	for backslashes < i-1 && text[i-2-backslashes] == '\\' {
		backslashes++
	}
	length := 1
	for ; backslashes%2 == 1 && length < 64; backslashes /= 2 {
		length *= 2
	}
	return text[i-length : i]
}

func escapeJSON(value string) string {
	var b strings.Builder
	encoder := json.NewEncoder(&b)
	encoder.SetEscapeHTML(false)
	encoder.Encode(value)
	encoded := strings.TrimSuffix(b.String(), "\n")
	return encoded[1 : len(encoded)-1]
}

// RestoreTyped returns the JSON value of a typed placeholder (a number, object, array or literal
// masked under a named field) that stands alone, so a tool input gets the value with its type.
func (s *Session) RestoreTyped(text string, withhold map[string]bool) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, known := s.reverse[text]
	kind := tokenKind(text)
	if s.secret[text] {
		kind = "SECRET"
	}
	return value, known && s.quoted[text] && !withhold[kind]
}

func (s *Session) Unmasked(text string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.initErr != nil {
		return nil, s.initErr
	}
	if s.closed {
		return nil, ErrSession
	}
	if !s.files.ready(s.Policy.ProtectedPaths) {
		return nil, ErrFolders
	}
	matches, err := s.matches(text)
	if err != nil {
		return nil, err
	}
	var kinds []string
	if _, blocked := s.files.blockedFolder(text); blocked {
		kinds = append(kinds, "BLOCKED")
	}
	for _, m := range matches {
		if _, known := s.reverse[text[m.Start:m.End]]; !known && !s.files.trustedValue(text[m.Start:m.End]) {
			kinds = append(kinds, m.Kind)
		}
	}
	return kinds, nil
}

func (s *Session) Originals(text string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var values []string
	for {
		i := strings.Index(text, Prefix)
		if i < 0 {
			return values
		}
		j := strings.IndexByte(text[i:], ']')
		if j < 0 {
			return values
		}
		if value, known := s.reverse[text[i:i+j+1]]; known {
			values = append(values, value)
		}
		text = text[i+j+1:]
	}
}

func (s *Session) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	clear(s.forward)
	clear(s.reverse)
	clear(s.quoted)
	clear(s.foreign)
	clear(s.secret)
	clear(s.memo)
	clear(s.seen)
	clear(s.rules)
	s.memoBytes = 0
	s.known = nil
	s.regex = nil
	s.fields = nil
	s.Policy.CustomRules = nil
	s.Policy.CustomFields = nil
	s.bytes = 0
	s.closed = true
}

// Stream retains only a possible token prefix, not the tail of every text chunk.
type Stream struct {
	session        *Session
	pending        string
	inJSON         bool
	withhold, kept map[string]bool
	Withheld       int
}

func NewStream(s *Session) *Stream     { return &Stream{session: s} }
func NewJSONStream(s *Session) *Stream { return &Stream{session: s, inJSON: true} }

// NewWithholdingJSONStream restores tool input but keeps placeholders of the given kinds, and
// records them in kept.
func NewWithholdingJSONStream(s *Session, withhold, kept map[string]bool) *Stream {
	return &Stream{session: s, inJSON: true, withhold: withhold, kept: kept}
}

// NewWithholdingStream is NewWithholdingJSONStream for plain text or JSON tool input.
func NewWithholdingStream(s *Session, inJSON bool, withhold, kept map[string]bool) *Stream {
	return &Stream{session: s, inJSON: inJSON, withhold: withhold, kept: kept}
}

func (s *Stream) restore(text string) string {
	out, withheld := s.session.restore(text, s.inJSON, s.withhold, s.kept)
	s.Withheld += withheld
	return out
}
func (s *Stream) Push(chunk string, final bool) string {
	text := s.pending + chunk
	s.pending = ""
	if final {
		return s.restore(text)
	}
	cut := pendingFrom(text)
	s.pending = text[cut:]
	return s.restore(text[:cut])
}

// pendingFrom returns where the part of text that the next chunk may still change starts: a token
// not complete yet, a complete one whose closing quote has not fully arrived, or a quote, escaped
// for its JSON string level, that may open a token.
func pendingFrom(text string) int {
	cut := len(text)
	for n := min(len(Prefix)-1, len(text)); n > 0; n-- {
		if strings.HasSuffix(text, Prefix[:n]) {
			cut = len(text) - n
			break
		}
	}
	if i := strings.LastIndex(text, Prefix); i >= 0 && len(text)-i <= 64 {
		j := strings.IndexByte(text[i:], ']')
		quote := quoteBefore(text, i)
		if rest := text[i+j+1:]; j < 0 || quote != "" && len(rest) < len(quote) && strings.HasPrefix(quote, rest) {
			cut = min(cut, i)
		} else if rest == quote {
			return cut
		}
	}
	if cut == len(text) {
		cut = len(strings.TrimRight(strings.TrimSuffix(text, `"`), `\`))
		if len(text)-cut > 64 {
			cut = len(text)
		}
	}
	return cut - len(quoteBefore(text, cut))
}

type entry struct {
	session          *Session
	origin           string
	touched, created time.Time
	lifetime         time.Duration
	active           int
	closing          bool
}
type Store struct {
	mu            sync.Mutex
	entries       map[string]*entry
	ttl, lifetime time.Duration
	max           int
	now           func() time.Time
	policy        Policy
	files         *FileIndex
}

func NewStore(ttl, lifetime time.Duration, max int, policy Policy) *Store {
	return &Store{entries: make(map[string]*entry), ttl: ttl, lifetime: lifetime, max: max, now: time.Now, policy: policy}
}

func (s *Store) Create(policy Policy) (string, error) { return s.CreateSized(policy, 4096, 512<<10) }

func (s *Store) CreateSized(policy Policy, maxEntries, maxBytes int) (string, error) {
	return s.CreateFor(policy, maxEntries, maxBytes, s.lifetime)
}

// CreateFor sets a lifetime for one session, for example a terminal that keeps its session alive.
func (s *Store) CreateFor(policy Policy, maxEntries, maxBytes int, lifetime time.Duration) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweep()
	if err := ValidatePolicy(policy); err != nil {
		return "", err
	}
	if len(s.entries) >= s.max {
		return "", ErrLimit
	}
	id, err := RandomID()
	if err != nil {
		return "", err
	}
	now := s.now()
	session := NewSession(maxEntries, maxBytes, policy)
	session.files = s.files
	s.entries[id] = &entry{session: session, touched: now, created: now, lifetime: lifetime}
	return id, nil
}

func (s *Store) Acquire(id, origin string) (*Session, func(), error) {
	if id == "" {
		s.mu.Lock()
		session := NewSession(4096, 512<<10, s.policy)
		session.files = s.files
		s.mu.Unlock()
		return session, session.Clear, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweep()
	e := s.entries[id]
	if e == nil || e.closing || e.origin != "" && e.origin != origin {
		return nil, nil, ErrSession
	}
	e.origin = origin
	e.active++
	e.touched = s.now()
	var once sync.Once
	return e.session, func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			e.active--
			e.touched = s.now()
			if e.closing && e.active == 0 {
				e.session.Clear()
				delete(s.entries, id)
			}
		})
	}, nil
}

func (s *Store) Delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.entries[id]; e != nil {
		e.closing = true
		if e.active == 0 {
			e.session.Clear()
			delete(s.entries, id)
		}
	}
}

func (s *Store) sweep() {
	now := s.now()
	for id, e := range s.entries {
		if now.Sub(e.created) >= e.lifetime || e.active == 0 && now.Sub(e.touched) >= s.ttl {
			e.closing = true
		}
		if e.closing && e.active == 0 {
			e.session.Clear()
			delete(s.entries, id)
		}
	}
}

func (s *Store) Sweep() { s.mu.Lock(); defer s.mu.Unlock(); s.sweep() }

func (s *Store) SetPolicy(policy Policy) error {
	if err := ValidatePolicy(policy); err != nil {
		return err
	}
	s.mu.Lock()
	s.policy = policy.clone()
	sessions := s.live()
	s.mu.Unlock()
	for _, session := range sessions {
		if err := session.SetRules(policy); err != nil && err != ErrSession {
			return err
		}
	}
	return nil
}

func (s *Store) SetFiles(files *FileIndex) {
	s.mu.Lock()
	s.files = files
	sessions := s.live()
	s.mu.Unlock()
	for _, session := range sessions {
		session.SetFiles(files)
	}
}

func (s *Store) live() []*Session {
	sessions := make([]*Session, 0, len(s.entries))
	for _, e := range s.entries {
		if !e.closing {
			sessions = append(sessions, e.session)
		}
	}
	return sessions
}
func (s *Store) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, e := range s.entries {
		e.closing = true
		if e.active == 0 {
			e.session.Clear()
			delete(s.entries, id)
		}
	}
}
