package demo

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"phantomprompt/internal/mask"
	"phantomprompt/internal/protocol"
	"phantomprompt/internal/proxy"
)

const Author = "Local Privacy Lab"

const PanelHostName = "phantomprompt.localhost"

func PanelHost(localHost string) string {
	_, port, _ := net.SplitHostPort(localHost)
	return net.JoinHostPort(PanelHostName, port)
}

const Origin = "https://demo.phantomprompt.test"

//go:embed index.html dist
var assets embed.FS

type Backend struct {
	Server   *http.Server
	Listener net.Listener
	CA       *proxy.Authority
}

func StartBackend() (*Backend, error) {
	ca, err := proxy.NewAuthority("PhantomPrompt local demo upstream CA")
	if err != nil {
		return nil, err
	}
	cert, err := ca.Issue("demo.phantomprompt.test")
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	server := &http.Server{Handler: http.HandlerFunc(echo), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10, ErrorLog: log.New(io.Discard, "", 0)}
	b := &Backend{Server: server, Listener: listener, CA: ca}
	go server.Serve(tls.NewListener(listener, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}}))
	return b, nil
}

func (b *Backend) Options(internet bool) (proxy.Options, error) {
	roots := x509.NewCertPool()
	if internet {
		system, err := x509.SystemCertPool()
		if err != nil {
			return proxy.Options{}, err
		}
		roots = system
	}
	roots.AppendCertsFromPEM(b.CA.PEM)
	direct := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	return proxy.Options{Roots: roots, DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		if addr == "demo.phantomprompt.test:443" {
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, b.Listener.Addr().String())
		}
		if !internet {
			return nil, fmt.Errorf("demo does not allow external traffic")
		}
		return direct.DialContext(ctx, network, addr)
	}}, nil
}

func echo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/echo" || r.URL.RawQuery != "" {
		http.Error(w, "Demo route unavailable", 404)
		return
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, proxy.MaxBody+1))
	var request protocol.DemoRequest
	if err != nil || len(data) > proxy.MaxBody || protocol.Decode(data, &request) != nil {
		http.Error(w, "Invalid demo request", 400)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if !request.Stream {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(protocol.DemoResponse{Text: request.Text, Received: request.Text})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	receipt, _ := json.Marshal(map[string]string{"text": request.Text})
	if protocol.WriteEvent(w, protocol.Event{Name: "receipt", Data: string(receipt)}) != nil {
		return
	}
	flusher := w.(http.Flusher)
	flusher.Flush()
	// Small chunks deliberately split placeholders to exercise streaming restoration.
	text := []rune(request.Text)
	for start := 0; start < len(text); start += 7 {
		end := min(start+7, len(text))
		chunk, _ := json.Marshal(map[string]string{"text": string(text[start:end])})
		if protocol.WriteEvent(w, protocol.Event{Name: "delta", Data: string(chunk)}) != nil {
			return
		}
		flusher.Flush()
		select {
		case <-r.Context().Done():
			return
		case <-time.After(8 * time.Millisecond):
		}
	}
	protocol.WriteEvent(w, protocol.Event{Name: "done", Data: `{}`})
	flusher.Flush()
}

// PanelOptions are the parts of the panel that depend on how PhantomPrompt was started.
type PanelOptions struct {
	OpenTerminal func(folder string) error
	ShowLogs     func() error
	Logins       *Logins
	// ClaudeSetup is the shell snippet that points Claude Code at the gateway (App Store edition).
	ClaudeSetup string
}

func Handler(app *proxy.App, proxyAddress, localHost, access string, options PanelOptions) (http.Handler, func(), error) {
	openFolder, logins := options.OpenTerminal, options.Logins
	pURL, err := url.Parse("http://" + proxyAddress)
	if err != nil {
		return nil, nil, err
	}
	pURL.User = url.UserPassword("phantomprompt", access)
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(app.CA.PEM)
	transport := &http.Transport{Proxy: http.ProxyURL(pURL), TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 10 * time.Second, DisableCompression: true}
	client := &http.Client{Transport: transport, Timeout: 2 * time.Minute, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	tmpl, err := template.ParseFS(assets, "index.html")
	if err != nil {
		return nil, nil, err
	}
	dist, err := fs.Sub(assets, "dist")
	if err != nil {
		return nil, nil, err
	}
	files := http.FileServerFS(dist)
	panelHost := PanelHost(localHost)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
		if !proxy.LocalRequest(r, localHost) && !proxy.LocalRequest(r, panelHost) {
			http.Error(w, "Local origin required", 403)
			return
		}
		signedIn := app.Authorized(r.Header.Get(proxy.AccessHeader))
		page := func(signedIn bool) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			data := map[string]any{"Access": "", "Author": Author, "Terminal": openFolder != nil, "Logs": options.ShowLogs != nil, "ClaudeSetup": ""}
			if signedIn {
				data["Access"], data["ClaudeSetup"] = access, options.ClaudeSetup
			}
			tmpl.Execute(w, data)
		}
		switch {
		case strings.HasPrefix(r.URL.Path, "/login/") && r.Method == http.MethodGet:
			if !logins.redeem(strings.TrimPrefix(r.URL.Path, "/login/")) {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(http.StatusForbidden)
				io.WriteString(w, lockedPage)
				return
			}
			page(true)
		case r.URL.Path == "/" && r.Method == http.MethodGet:
			page(signedIn)
		case strings.HasPrefix(r.URL.Path, "/assets/") && !strings.HasSuffix(r.URL.Path, "/") && r.Method == http.MethodGet:
			files.ServeHTTP(w, r)
		case r.URL.Path == "/rules" && r.Method == http.MethodGet:
			if !signedIn {
				http.Error(w, "Local authentication required", 401)
				return
			}
			startup, err := app.StartupPolicy()
			if err != nil {
				http.Error(w, "Rules unavailable", 500)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"defaults": mask.DefaultFields(), "secrets": mask.SecretDetectors(), "patterns": mask.DetectorPatterns(), "policy": app.Policy(), "startup": startup})
		case r.URL.Path == "/api/rules" && r.Method == http.MethodPut:
			if !app.Authorized(r.Header.Get(proxy.AccessHeader)) {
				http.Error(w, "Local authentication required", 401)
				return
			}
			data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
			var policy mask.Policy
			if err != nil || protocol.Decode(data, &policy) != nil {
				http.Error(w, "Invalid rules", 400)
				return
			}
			// The panel sends its whole rule set; trusted folders it does not know about stay as they are.
			var fields map[string]json.RawMessage
			json.Unmarshal(data, &fields)
			_, sendsTrusted := fields["trusted_paths"]
			if err := app.UpdatePolicy(func(current *mask.Policy) {
				if !sendsTrusted {
					policy.TrustedPaths = current.TrustedPaths
				}
				*current = policy
			}); err != nil {
				code, message := 400, "Rules rejected: check field names, patterns and that at least one detector is on"
				if !errors.Is(err, mask.ErrFields) && !errors.Is(err, mask.ErrRegex) && !errors.Is(err, mask.ErrSession) && !errors.Is(err, mask.ErrPaths) && !errors.Is(err, mask.ErrSecrets) {
					code, message = 500, "Rules could not be saved"
				}
				http.Error(w, message, code)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(app.Policy())
		case r.URL.Path == "/ca.pem" && r.Method == http.MethodGet:
			w.Header().Set("Content-Type", "application/x-pem-file")
			w.Write(app.CA.PEM)
		case r.URL.Path == "/sessions" || r.URL.Path == "/status" || r.URL.Path == "/events" || r.URL.Path == "/discovery":
			app.Control(w, r)
		case r.URL.Path == "/api/protection" && r.Method == http.MethodPost:
			if !app.Authorized(r.Header.Get(proxy.AccessHeader)) {
				http.Error(w, "Local authentication required", 401)
				return
			}
			var body struct {
				On bool `json:"on"`
			}
			if json.NewDecoder(io.LimitReader(r.Body, 256)).Decode(&body) != nil {
				http.Error(w, "Invalid request", 400)
				return
			}
			app.SetProtection(body.On)
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/api/shutdown" && r.Method == http.MethodPost:
			if !app.Authorized(r.Header.Get(proxy.AccessHeader)) {
				http.Error(w, "Local authentication required", 401)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			go func() { time.Sleep(200 * time.Millisecond); app.RequestShutdown() }()
		case r.URL.Path == "/api/folders" && r.Method == http.MethodPost:
			if !app.Authorized(r.Header.Get(proxy.AccessHeader)) {
				http.Error(w, "Local authentication required", 401)
				return
			}
			folders(w, r)
		case r.URL.Path == "/api/trusted" && r.Method == http.MethodPost:
			if !app.Authorized(r.Header.Get(proxy.AccessHeader)) {
				http.Error(w, "Local authentication required", 401)
				return
			}
			trustFolder(w, r, app)
		case r.URL.Path == "/api/regex/generate" || r.URL.Path == "/api/regex/test":
			if !app.Authorized(r.Header.Get(proxy.AccessHeader)) {
				http.Error(w, "Local authentication required", 401)
				return
			}
			regexLab(w, r)
		case r.URL.Path == "/api/terminal" && r.Method == http.MethodPost && openFolder != nil:
			if !app.Authorized(r.Header.Get(proxy.AccessHeader)) {
				http.Error(w, "Local authentication required", 401)
				return
			}
			openTerminal(w, r, openFolder)
		case r.URL.Path == "/api/logs/show" && r.Method == http.MethodPost && options.ShowLogs != nil:
			if !app.Authorized(r.Header.Get(proxy.AccessHeader)) {
				http.Error(w, "Local authentication required", 401)
				return
			}
			if options.ShowLogs() != nil {
				http.Error(w, "Could not open the log folder in Finder", http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/api/terminal/close" && r.Method == http.MethodPost:
			if !app.Authorized(r.Header.Get(proxy.AccessHeader)) {
				http.Error(w, "Local authentication required", 401)
				return
			}
			var body struct {
				ID string `json:"id"`
			}
			if json.NewDecoder(io.LimitReader(r.Body, 256)).Decode(&body) != nil {
				http.Error(w, "Invalid request", 400)
				return
			}
			if !app.CloseTerminal(body.ID) {
				http.Error(w, "This terminal is already closed", http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/api/chat" && r.Method == http.MethodPost:
			if !app.Authorized(r.Header.Get(proxy.AccessHeader)) {
				http.Error(w, "Local authentication required", 401)
				return
			}
			data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, proxy.MaxBody))
			if err != nil {
				http.Error(w, "Request too large", 413)
				return
			}
			out, err := http.NewRequestWithContext(r.Context(), http.MethodPost, Origin+"/echo", strings.NewReader(string(data)))
			if err != nil {
				http.Error(w, "Request failed", 500)
				return
			}
			out.Header.Set("Content-Type", "application/json")
			out.Header.Set(proxy.SessionHeader, r.Header.Get(proxy.SessionHeader))
			response, err := client.Do(out)
			if err != nil {
				http.Error(w, "Proxy connection failed; request was not sent directly", 502)
				return
			}
			proxy.WriteResponse(w, response)
		default:
			http.NotFound(w, r)
		}
	}), transport.CloseIdleConnections, nil
}
