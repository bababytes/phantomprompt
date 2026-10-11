package proxy

import (
	"crypto/subtle"
	"net/http"
	"net/url"
	"strings"

	"phantomprompt/internal/mask"
)

// Coding agents point their documented base URL setting at these routes, so only model traffic
// reaches PhantomPrompt, with no CA and no MITM, while the rest of their traffic is unchanged.
//
//	/<agent>/s/<session>/<secret>/[<upstream>/]<path>  a command started with phantom
//	/<agent>/k/<key>/[<upstream>/]<path>               a fixed key set by hand (App Store edition)
const ClaudeRoute = "/claude/"
const CodexRoute = "/codex/"

type gatewayRoute struct {
	adapter, label string
	// upstreams maps the leading path of a request to a base URL; "" when there is a single one.
	upstreams     map[string]string
	allowed       func(method, path string) bool
	sessionHeader string
}

var gatewayRoutes = map[string]gatewayRoute{
	ClaudeRoute: {
		adapter: ClaudeCode, label: "claude-gateway",
		upstreams: map[string]string{"": "https://api.anthropic.com"},
		allowed: func(method, path string) bool {
			return method == http.MethodPost && (path == "v1/messages" || strings.HasPrefix(path, "v1/messages/"))
		},
		sessionHeader: "X-Claude-Code-Session-Id",
	},
	CodexRoute: {
		adapter: CodexCLI, label: "codex-gateway",
		upstreams: map[string]string{"backend-api/codex": "https://chatgpt.com/backend-api/codex", "v1": "https://api.openai.com/v1"},
		allowed: func(method, path string) bool {
			return method == http.MethodPost && path == "responses" || method == http.MethodGet && path == "models"
		},
		sessionHeader: "Session-Id",
	},
}

func ClaudeBaseURL(gateway, session, secret string) string {
	return "http://" + gateway + ClaudeRoute + "s/" + session + "/" + secret
}

func ClaudeKeyURL(gateway, key string) string {
	return "http://" + gateway + ClaudeRoute + "k/" + key
}

// codexPaths keep the upstream's own path at the end of the base URL: Codex enables its ChatGPT
// backend routes only for an OpenAI provider whose base URL ends with /backend-api/codex.
var codexPaths = map[string]string{"chatgpt": "backend-api/codex", "openai": "v1"}

// CodexBaseURL is the base_url of the Codex model provider; upstream is "chatgpt" for a ChatGPT
// login and "openai" for an API key.
func CodexBaseURL(gateway, session, secret, upstream string) string {
	return "http://" + gateway + CodexRoute + "s/" + session + "/" + secret + "/" + codexPaths[upstream]
}

func CodexKeyURL(gateway, key, upstream string) string {
	return "http://" + gateway + CodexRoute + "k/" + key + "/" + codexPaths[upstream]
}

// SetGatewayKey enables the /<agent>/k/<key>/ routes; an empty key disables them.
func (a *App) SetGatewayKey(key string) {
	a.webMu.Lock()
	defer a.webMu.Unlock()
	a.gatewayKey = key
}

func gatewayRouteFor(path string) (string, gatewayRoute, bool) {
	for prefix, route := range gatewayRoutes {
		if strings.HasPrefix(path, prefix) {
			return prefix, route, true
		}
	}
	return "", gatewayRoute{}, false
}

func (a *App) agentGateway(w http.ResponseWriter, r *http.Request, expectedHost string, prefix string, route gatewayRoute) {
	if r.Host != expectedHost || r.Header.Get("Origin") != "" || r.Header.Get("Sec-Fetch-Site") != "" && r.Header.Get("Sec-Fetch-Site") != "none" {
		http.Error(w, "Local origin required", http.StatusForbidden)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, prefix), "/")
	var session string
	var rest []string
	switch {
	case len(parts) >= 4 && parts[0] == "s" && a.cliSessions.authorized(parts[1], parts[2]):
		session, rest = parts[1], parts[3:]
		a.cliSessions.touch(session, false)
	case len(parts) >= 3 && parts[0] == "k" && a.gatewayKeyMatches(parts[1]):
		var err error
		if session, err = a.keySession(route, r.Header.Get(route.sessionHeader)); err != nil {
			WriteResponse(w, cliFailure(r, http.StatusConflict, "session_unavailable"))
			return
		}
		rest = parts[2:]
	default:
		http.Error(w, "Local authentication required", http.StatusUnauthorized)
		return
	}
	path := strings.Join(rest, "/")
	base, single := route.upstreams[""]
	if !single {
		for prefix, url := range route.upstreams {
			if suffix, ok := strings.CutPrefix(path, prefix+"/"); ok {
				base, path = url, suffix
				break
			}
		}
		if base == "" {
			http.NotFound(w, r)
			return
		}
	}
	if !route.allowed(r.Method, path) || strings.Contains(path, "..") {
		http.NotFound(w, r)
		return
	}
	target, _ := url.Parse(base)
	upstream := r.Clone(r.Context())
	upstream.URL = &url.URL{Scheme: target.Scheme, Host: target.Host, Path: target.Path + "/" + path, RawQuery: r.URL.RawQuery}
	upstream.Host = target.Host
	upstream.RequestURI = ""
	forwarded, response := a.CLI(upstream, tunnel{authority: target.Host + ":443", adapter: route.adapter, session: session})
	if response == nil {
		var err error
		if response, err = a.ModelTransport.RoundTrip(forwarded); err != nil {
			response = cliFailure(upstream, http.StatusBadGateway, "upstream_TLS_or_connection_failed")
		}
	}
	WriteResponse(w, response)
}

func (a *App) gatewayKeyMatches(key string) bool {
	a.webMu.Lock()
	defer a.webMu.Unlock()
	return a.gatewayKey != "" && subtle.ConstantTimeCompare([]byte(key), []byte(a.gatewayKey)) == 1
}

// keySession gives each agent session its own map, keyed by the session id the agent sends.
func (a *App) keySession(route gatewayRoute, agentSession string) (string, error) {
	if len(mask.GUIDs(agentSession)) != 1 || len(agentSession) != 36 {
		agentSession = "shared"
	}
	name := route.label + "|" + agentSession
	a.webMu.Lock()
	defer a.webMu.Unlock()
	if id := a.webSessions[name]; id != "" {
		if _, release, err := a.Sessions.Acquire(id, "cli"); err == nil {
			release()
			a.cliSessions.touch(id, false)
			return id, nil
		}
		delete(a.webSessions, name)
	}
	a.policyMu.RLock()
	policy := clonePolicy(a.policy)
	policy.LenientFields = true
	id, err := a.Sessions.CreateFor(policy, 65536, 8<<20, cliLifetime)
	a.policyMu.RUnlock()
	if err != nil {
		return "", err
	}
	secret, err := mask.RandomID()
	if err != nil {
		a.Sessions.Delete(id)
		return "", err
	}
	if !a.cliSessions.add(id, secret, route.label, 0) {
		a.Sessions.Delete(id)
		return "", mask.ErrLimit
	}
	a.webSessions[name] = id
	return id, nil
}
