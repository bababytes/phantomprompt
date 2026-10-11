package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"

	"phantomprompt/internal/mask"
	"phantomprompt/internal/protocol"
)

// LocalRequest rejects browser cross-origin access and unexpected Host headers.
func LocalRequest(r *http.Request, expectedHost string) bool {
	if r.Host != expectedHost || r.URL.IsAbs() || r.URL.RawQuery != "" {
		return false
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "none" && site != "same-origin" {
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+expectedHost {
		return false
	}
	return true
}

func (a *App) Control(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !a.Authorized(r.Header.Get(AccessHeader)) {
		http.Error(w, "Local authentication required", http.StatusUnauthorized)
		return
	}
	switch {
	case r.URL.Path == "/sessions" && r.Method == http.MethodPost:
		var policy mask.Policy
		if r.Body == nil {
			http.Error(w, "JSON policy required", 400)
			return
		}
		data, err := io.ReadAll(io.LimitReader(r.Body, 8193))
		if err != nil || len(data) > 8192 || protocol.Decode(data, &policy) != nil {
			http.Error(w, "Invalid policy", 400)
			return
		}
		id, err := a.Sessions.Create(policy)
		if err != nil {
			http.Error(w, "Session unavailable or empty policy", 409)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"session": id})
	case r.URL.Path == "/sessions" && r.Method == http.MethodDelete:
		a.Sessions.Delete(r.Header.Get(SessionHeader))
		w.WriteHeader(http.StatusNoContent)
	case r.URL.Path == "/status" && r.Method == http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"forwarded": a.Forwarded.Load(), "blocked": a.Blocked.Load(), "vin": a.MaskedVIN.Load(), "pesel": a.MaskedPESEL.Load(), "email": a.MaskedEmail.Load(), "fields": a.MaskedFields.Load(), "guid": a.MaskedGUID.Load(), "custom": a.MaskedCustom.Load(), "secret": a.MaskedSecret.Load(), "file": a.MaskedFile.Load(), "new_values": a.MaskedNew.Load(), "typed_values": a.MaskedTyped.Load(), "context_values": a.MaskedContext.Load(), "prompts_sent": a.PromptsSent.Load(), "token_overhead": a.TokenOverhead.Load(), "input_tokens": a.InputTokens.Load(), "usage_overhead": a.UsageOverhead.Load(), "protection": a.Protection(), "integrations": a.Integrations(), "prompts": a.PromptStats(), "cli_sessions": a.CLISessions(), "protected_paths": a.ProtectedPathStatus(), "trusted_paths": a.TrustedPathStatus(), "custom_rules": a.CustomRuleStats(), "secret_rules": a.SecretRuleStats(), "format_alerts": a.FormatAlerts(), "log_folder": a.LogFolder()})
	case r.URL.Path == "/events" && r.Method == http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(a.Events.Snapshot())
	case r.URL.Path == "/discovery" && r.Method == http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(a.Discovery())
	case r.URL.Path == "/events" && r.Method == http.MethodDelete:
		a.Events.Clear()
		a.customRules.clearExamples()
		a.resetTotals()
		w.WriteHeader(http.StatusNoContent)
	default:
		http.NotFound(w, r)
	}
}

func (a *App) Gateway(expectedHost string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if prefix, route, ok := gatewayRouteFor(r.URL.Path); ok {
			a.agentGateway(w, r, expectedHost, prefix, route)
			return
		}
		if !LocalRequest(r, expectedHost) {
			http.Error(w, "Local origin required", 403)
			return
		}
		if r.URL.Path == "/sessions" || r.URL.Path == "/status" || r.URL.Path == "/events" || r.URL.Path == "/discovery" {
			a.Control(w, r)
			return
		}
		if !a.Authorized(r.Header.Get(AccessHeader)) {
			http.Error(w, "Local authentication required", 401)
			return
		}
		if a.Config.GatewayOrigin == "" {
			http.Error(w, "Gateway not configured", 404)
			return
		}
		u, err := url.Parse(a.Config.GatewayOrigin + r.URL.EscapedPath())
		if err != nil || strings.Contains(r.URL.EscapedPath(), "%") {
			http.Error(w, "Invalid path", 400)
			return
		}
		out := r.Clone(r.Context())
		out.URL = u
		out.Host = u.Host
		out.RequestURI = ""
		WriteResponse(w, a.Protected(out))
	})
}
