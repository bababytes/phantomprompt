package demo

import (
	"sync"
	"time"

	"phantomprompt/internal/mask"
)

const loginLifetime = 2 * time.Minute

// Logins hands out one-time panel links. A link returns the panel with the access token once; the
// browser tab keeps the token in sessionStorage, which is separate for every port, unlike a cookie
// that every server on the same local host name would receive.
type Logins struct {
	mu    sync.Mutex
	codes map[string]time.Time
	now   func() time.Time
}

func NewLogins() *Logins {
	return &Logins{codes: make(map[string]time.Time), now: time.Now}
}

// Issue returns a path ("/login/<code>") that opens the panel once within two minutes.
func (l *Logins) Issue() string {
	code, err := mask.RandomID()
	if err != nil {
		return "/"
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	for old, issued := range l.codes {
		if now.Sub(issued) > loginLifetime {
			delete(l.codes, old)
		}
	}
	l.codes[code] = now
	return "/login/" + code
}

func (l *Logins) redeem(code string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	issued, ok := l.codes[code]
	delete(l.codes, code)
	return ok && l.now().Sub(issued) <= loginLifetime
}

const lockedPage = `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>PhantomPrompt</title></head>
<body style="font:16px/1.5 system-ui,sans-serif;max-width:560px;margin:15vh auto;padding:0 16px">
<h1>PhantomPrompt</h1><p>Open the panel from the PhantomPrompt app icon, or run <code>phantom open</code> in a terminal.</p>
<p>Panel links work once, so this page cannot be bookmarked.</p></body></html>`
