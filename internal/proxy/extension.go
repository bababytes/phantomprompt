package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"phantomprompt/internal/protocol"
)

const maxExtensionRequest = 2 << 20
const MaxExtensionReply = 900 << 10

type CLISettings struct {
	Proxy        string   `json:"proxy"`
	Gateway      string   `json:"gateway,omitempty"`
	BlockedPaths []string `json:"blocked_paths,omitempty"`
	User         string   `json:"user"`
	CA           string   `json:"ca"`
	Integrations []string `json:"integrations"`
}

// LocalAddresses tells local clients where to connect; PanelURL returns a one-time login link.
type LocalAddresses struct {
	Proxy, Gateway string
	PanelURL       func() string
}

type trustRequest struct {
	Path    string `json:"path"`
	Trusted bool   `json:"trusted"`
}

type cliSessionRequest struct {
	Label  string `json:"label"`
	PID    int    `json:"pid"`
	Folder string `json:"folder"`
}

func (a *App) Extension(addresses LocalAddresses) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/cli", func(w http.ResponseWriter, r *http.Request) {
		var blocked []string
		for _, entry := range a.Policy().BlockedPaths {
			if expanded, err := expandHome(entry); err == nil {
				blocked = append(blocked, expanded)
			}
		}
		writeExtension(w, CLISettings{Proxy: addresses.Proxy, Gateway: addresses.Gateway, BlockedPaths: blocked, User: "phantomprompt", CA: string(a.CA.PEM), Integrations: a.Config.Integrations})
	})
	mux.HandleFunc("GET /v1/panel", func(w http.ResponseWriter, r *http.Request) {
		if addresses.PanelURL == nil {
			http.NotFound(w, r)
			return
		}
		writeExtension(w, map[string]string{"url": addresses.PanelURL()})
	})
	mux.HandleFunc("POST /v1/shutdown", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
		go func() { time.Sleep(200 * time.Millisecond); a.RequestShutdown() }()
	})
	mux.HandleFunc("POST /v1/cli/session", func(w http.ResponseWriter, r *http.Request) {
		var req cliSessionRequest
		if r.ContentLength != 0 && !readExtension(w, r, &req) {
			return
		}
		id, secret, err := a.CreateCLISession(req.Label, req.PID)
		if err != nil {
			http.Error(w, "Session unavailable", http.StatusConflict)
			return
		}
		a.cliSessions.setFolder(id, req.Folder)
		writeExtension(w, map[string]string{"session": id, "secret": secret})
	})
	mux.HandleFunc("POST /v1/cli/touch", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]string
		if !readExtension(w, r, &req) {
			return
		}
		pid, _ := strconv.Atoi(req["pid"])
		state, added := a.cliSessions.heartbeat(req["session"], req["label"], pid, req["folder"])
		if added != nil {
			a.Events.log.Logger().Warn("terminal from before a restart is disconnected", "terminal", added.Label, "pid", added.PID, "folder", added.Folder)
		}
		switch state {
		case terminalClosed:
			w.WriteHeader(http.StatusGone)
		case terminalDisconnected:
			w.WriteHeader(http.StatusNotFound)
		default:
			a.touchCLISession(req["session"])
			w.WriteHeader(http.StatusNoContent)
		}
	})
	mux.HandleFunc("POST /v1/cli/close", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]string
		if !readExtension(w, r, &req) {
			return
		}
		a.Sessions.Delete(req["session"])
		a.cliSessions.remove(req["session"])
		a.forgetTurns(req["session"])
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /v1/trusted", func(w http.ResponseWriter, r *http.Request) { writeExtension(w, a.TrustedPathStatus()) })
	mux.HandleFunc("POST /v1/trusted", func(w http.ResponseWriter, r *http.Request) {
		var req trustRequest
		if !readExtension(w, r, &req) {
			return
		}
		if err := a.SetTrustedPath(req.Path, req.Trusted); err != nil {
			http.Error(w, "Folder rejected: use an absolute path that is not a protected folder", http.StatusBadRequest)
			return
		}
		writeExtension(w, a.TrustedPathStatus())
	})
	mux.HandleFunc("GET /v1/discovery", func(w http.ResponseWriter, r *http.Request) { writeExtension(w, a.Discovery()) })
	mux.HandleFunc("GET /v1/events", func(w http.ResponseWriter, r *http.Request) { writeExtension(w, a.Events.Snapshot()) })
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		mux.ServeHTTP(w, r)
	})
}

func readExtension(w http.ResponseWriter, r *http.Request, value any) bool {
	data, err := io.ReadAll(io.LimitReader(r.Body, maxExtensionRequest+1))
	if err != nil || len(data) > maxExtensionRequest || protocol.Decode(data, value) != nil {
		http.Error(w, "Invalid extension request", http.StatusBadRequest)
		return false
	}
	return true
}

func writeExtension(w http.ResponseWriter, value any) {
	data, err := json.Marshal(value)
	if err != nil || len(data) > MaxExtensionReply {
		http.Error(w, "Extension reply too large", http.StatusRequestEntityTooLarge)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}
