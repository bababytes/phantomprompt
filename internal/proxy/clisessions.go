package proxy

import (
	"crypto/rand"
	"crypto/subtle"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	cliActiveWindow = 90 * time.Second
	cliRetention    = 30 * time.Minute
	maxCLISessions  = 128
)

var (
	cliLabel     = regexp.MustCompile(`^[A-Za-z0-9._-]{1,40}$`)
	cliSessionID = regexp.MustCompile(`^[0-9a-f]{32}$`)
)

type CLISession struct {
	ID            string     `json:"id"`
	Label         string     `json:"label"`
	Folder        string     `json:"folder,omitempty"`
	PID           int        `json:"pid,omitempty"`
	Started       time.Time  `json:"started"`
	LastSeen      time.Time  `json:"last_seen"`
	LastActivity  *time.Time `json:"last_activity,omitempty"`
	Services      []string   `json:"services"`
	Prompts       uint64     `json:"prompts"`
	Requests      uint64     `json:"requests"`
	Masked        uint64     `json:"masked"`
	Blocked       uint64     `json:"blocked"`
	TokenOverhead int64      `json:"token_overhead"`
	InputTokens   int64      `json:"input_tokens"`
	UsageOverhead int64      `json:"usage_overhead"`
	Active        bool       `json:"active"`
	Expired       bool       `json:"expired"`
	Disconnected  bool       `json:"disconnected,omitempty"`
}

type cliEntry struct {
	secret   string
	terminal string
	closed   bool
	view     CLISession
}

type cliRegistry struct {
	mu      sync.Mutex
	entries map[string]*cliEntry
	now     func() time.Time
}

func (c *cliRegistry) clock() time.Time {
	if c.now == nil {
		return time.Now().UTC()
	}
	return c.now()
}

func (c *cliRegistry) prune(now time.Time) {
	for id, e := range c.entries {
		if idle := now.Sub(e.view.LastSeen); idle >= cliRetention || e.view.Disconnected && idle >= cliActiveWindow {
			delete(c.entries, id)
		}
	}
}

func (c *cliRegistry) add(id, secret, label string, pid int) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.insert(id, secret, label, pid) != nil
}

func (c *cliRegistry) insert(id, secret, label string, pid int) *cliEntry {
	now := c.clock()
	c.prune(now)
	if len(c.entries) >= maxCLISessions {
		return nil
	}
	if c.entries == nil {
		c.entries = make(map[string]*cliEntry)
	}
	if !cliLabel.MatchString(label) {
		label = "cli"
	}
	if pid < 0 || pid > 1<<22 {
		pid = 0
	}
	e := &cliEntry{secret: secret, view: CLISession{ID: rand.Text(), Label: label, PID: pid, Started: now, LastSeen: now, Services: []string{}}}
	c.entries[id] = e
	return e
}

func displayFolder(path string) string {
	if !filepath.IsAbs(path) || len(path) > 1024 || !utf8.ValidString(path) || strings.ContainsFunc(path, unicode.IsControl) {
		return ""
	}
	clean := filepath.Clean(path)
	if home, err := os.UserHomeDir(); err == nil && (clean == home || strings.HasPrefix(clean, home+string(filepath.Separator))) {
		return "~" + strings.TrimPrefix(clean, home)
	}
	return clean
}

func (c *cliRegistry) setFolder(id, path string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.entries[id]; e != nil {
		e.view.Folder = displayFolder(path)
	}
}

func (c *cliRegistry) setTerminal(id, terminal string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.entries[id]; e != nil {
		e.terminal = terminal
	}
}

func (c *cliRegistry) folder(id string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.entries[id]; e != nil {
		return e.view.Folder
	}
	return ""
}

func (c *cliRegistry) authorized(id, secret string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.entries[id]
	return e != nil && !e.closed && !e.view.Disconnected && subtle.ConstantTimeCompare([]byte(secret), []byte(e.secret)) == 1
}

func (c *cliRegistry) touch(id string, expired bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.entries[id]; e != nil {
		e.view.LastSeen = c.clock()
		e.view.Expired = expired
	}
}

type terminalState int

const (
	terminalConnected terminalState = iota
	terminalDisconnected
	terminalClosed
)

func (c *cliRegistry) heartbeat(id, label string, pid int, folder, terminal string) (terminalState, *CLISession) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.entries[id]
	if e == nil {
		if !cliSessionID.MatchString(id) {
			return terminalDisconnected, nil
		}
		if e = c.insert(id, "", label, pid); e == nil {
			return terminalDisconnected, nil
		}
		e.view.Disconnected = true
		e.view.Folder = displayFolder(folder)
		e.terminal = terminal
		added := e.view
		return terminalDisconnected, &added
	}
	switch {
	case e.closed:
		e.view.LastSeen = c.clock()
		return terminalClosed, nil
	case e.view.Disconnected:
		e.view.LastSeen = c.clock()
		return terminalDisconnected, nil
	}
	return terminalConnected, nil
}

func (a *App) touchCLISession(id string) {
	_, release, err := a.Sessions.Acquire(id, "cli")
	if err == nil {
		release()
	}
	a.cliSessions.touch(id, err != nil)
}

func (c *cliRegistry) closeTerminal(panelID string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, e := range c.entries {
		if e.view.ID == panelID && (e.view.PID > 0 || e.view.Disconnected) && !e.closed {
			e.closed = true
			closed := []string{id}
			for inner, child := range c.entries {
				if child.terminal == id && !child.closed {
					child.closed = true
					closed = append(closed, inner)
				}
			}
			return closed
		}
	}
	return nil
}

func (c *cliRegistry) closedInPanel(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.entries[id]
	return e != nil && e.closed
}

func (c *cliRegistry) remove(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, id)
}

func (c *cliRegistry) record(id, service string, update func(*CLISession)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.entries[id]
	if e == nil {
		return
	}
	now := c.clock()
	e.view.LastActivity = &now
	if !slices.Contains(e.view.Services, service) {
		e.view.Services = append(e.view.Services, service)
	}
	update(&e.view)
}

func (c *cliRegistry) linkService(id, service string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.entries[id]; e != nil && !slices.Contains(e.view.Services, service) {
		e.view.Services = append(e.view.Services, service)
	}
}

func (c *cliRegistry) snapshot() []CLISession {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.clock()
	c.prune(now)
	views := make(map[string]*CLISession, len(c.entries))
	for id, e := range c.entries {
		if e.closed {
			continue
		}
		view := e.view
		view.Services = slices.Clone(e.view.Services)
		view.Active = !view.Expired && !view.Disconnected && now.Sub(view.LastSeen) < cliActiveWindow
		views[id] = &view
	}
	for id, view := range views {
		if outer := c.outerView(id, views); outer != nil {
			outer.include(*view)
		}
	}
	result := make([]CLISession, 0, len(views))
	for id, view := range views {
		if c.outerView(id, views) == nil {
			result = append(result, *view)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Started.Before(result[j].Started) })
	return result
}

func (c *cliRegistry) outerView(id string, views map[string]*CLISession) *CLISession {
	terminal := c.entries[id].terminal
	if outer := c.entries[terminal]; outer == nil || outer.terminal != "" {
		return nil
	}
	return views[terminal]
}

func (v *CLISession) include(inner CLISession) {
	v.Prompts += inner.Prompts
	v.Requests += inner.Requests
	v.Masked += inner.Masked
	v.Blocked += inner.Blocked
	v.TokenOverhead += inner.TokenOverhead
	v.InputTokens += inner.InputTokens
	v.UsageOverhead += inner.UsageOverhead
	v.Active = v.Active || inner.Active
	if inner.LastActivity != nil && (v.LastActivity == nil || inner.LastActivity.After(*v.LastActivity)) {
		v.LastActivity = inner.LastActivity
	}
	for _, service := range inner.Services {
		if !slices.Contains(v.Services, service) {
			v.Services = append(v.Services, service)
		}
	}
}

func (c *cliRegistry) resetCounters() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.entries {
		e.view.Prompts, e.view.Requests, e.view.Masked, e.view.Blocked = 0, 0, 0, 0
		e.view.TokenOverhead, e.view.InputTokens, e.view.UsageOverhead = 0, 0, 0
	}
}

func (a *App) CLISessions() []CLISession { return a.cliSessions.snapshot() }

func (a *App) CloseTerminal(panelID string) bool {
	closed := a.cliSessions.closeTerminal(panelID)
	for _, id := range closed {
		a.Sessions.Delete(id)
		a.forgetTurns(id)
	}
	return len(closed) > 0
}

func (c *cliRegistry) processes() map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	pids := make(map[string]int, len(c.entries))
	for id, e := range c.entries {
		pids[id] = e.view.PID
	}
	return pids
}

// sweepTerminals ends the sessions of terminals whose process is gone (closed without exit,
// killed), and drops turn grouping state that no longer has a terminal.
func (a *App) sweepTerminals() {
	pids := a.cliSessions.processes()
	for id, pid := range pids {
		if pid > 0 && syscall.Kill(pid, 0) == syscall.ESRCH {
			a.Sessions.Delete(id)
			a.cliSessions.remove(id)
			delete(pids, id)
		}
	}
	a.turns.mu.Lock()
	defer a.turns.mu.Unlock()
	for id := range a.turns.groups {
		if _, live := pids[id]; !live {
			delete(a.turns.groups, id)
		}
	}
}
