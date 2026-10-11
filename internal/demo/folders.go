package demo

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"phantomprompt/internal/proxy"
)

const maxFolderEntries = 1000

type folderEntry struct {
	Name string `json:"name"`
	Dir  bool   `json:"dir"`
}

type folderListing struct {
	Path      string        `json:"path"`
	Parent    string        `json:"parent"`
	Entries   []folderEntry `json:"entries"`
	Truncated bool          `json:"truncated,omitempty"`
}

func displayPath(path, home string) string {
	if home != "" && (path == home || strings.HasPrefix(path, home+string(filepath.Separator))) {
		return "~" + strings.TrimPrefix(path, home)
	}
	return path
}

func expandFolder(path, home string) (string, bool) {
	if strings.HasPrefix(path, "~") && home == "" {
		return "", false
	}
	switch {
	case path == "~":
		path = home
	case strings.HasPrefix(path, "~/"):
		path = filepath.Join(home, path[2:])
	}
	if !filepath.IsAbs(path) {
		return "", false
	}
	return filepath.Clean(path), true
}

func openTerminal(w http.ResponseWriter, r *http.Request, open func(string) error) {
	var request struct {
		Path string `json:"path"`
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<10))
	if err != nil || json.Unmarshal(data, &request) != nil {
		http.Error(w, "Invalid terminal request", http.StatusBadRequest)
		return
	}
	home, _ := os.UserHomeDir()
	path, ok := expandFolder(request.Path, home)
	if !ok {
		http.Error(w, "Use an absolute path or one starting with ~/", http.StatusBadRequest)
		return
	}
	info, err := os.Stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		http.Error(w, "Folder not found", http.StatusNotFound)
		return
	case err != nil || !info.IsDir():
		http.Error(w, "Choose a folder, not a file", http.StatusBadRequest)
		return
	}
	if err := open(path); err != nil {
		http.Error(w, "Could not open the Terminal app", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"path": displayPath(path, home)})
}

// trustFolder adds or removes a folder whose content reaches the model unmasked.
func trustFolder(w http.ResponseWriter, r *http.Request, app *proxy.App) {
	var request struct {
		Path    string `json:"path"`
		Trusted bool   `json:"trusted"`
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<10))
	if err != nil || json.Unmarshal(data, &request) != nil {
		http.Error(w, "Invalid folder request", http.StatusBadRequest)
		return
	}
	home, _ := os.UserHomeDir()
	path, ok := expandFolder(request.Path, home)
	if !ok {
		http.Error(w, "Use an absolute path or one starting with ~/", http.StatusBadRequest)
		return
	}
	if request.Trusted {
		info, err := os.Stat(path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			http.Error(w, "Folder not found", http.StatusNotFound)
			return
		case err != nil || !info.IsDir():
			http.Error(w, "Choose a folder, not a file", http.StatusBadRequest)
			return
		}
	}
	if err := app.SetTrustedPath(path, request.Trusted); err != nil {
		http.Error(w, "Folder rejected: choose a narrower folder than your home folder, not a protected one", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(app.TrustedPathStatus())
}

func folders(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Path string `json:"path"`
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<10))
	if err != nil || json.Unmarshal(data, &request) != nil {
		http.Error(w, "Invalid folder request", http.StatusBadRequest)
		return
	}
	home, _ := os.UserHomeDir()
	path, ok := expandFolder(request.Path, home)
	if !ok {
		http.Error(w, "Use an absolute path or one starting with ~/", http.StatusBadRequest)
		return
	}
	entries, err := os.ReadDir(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		http.Error(w, "Folder not found", http.StatusNotFound)
		return
	case errors.Is(err, fs.ErrPermission):
		http.Error(w, "No permission to read this folder", http.StatusForbidden)
		return
	case err != nil:
		http.Error(w, "Not a readable folder", http.StatusBadRequest)
		return
	}
	listing := folderListing{Path: displayPath(path, home), Entries: make([]folderEntry, 0, min(len(entries), maxFolderEntries))}
	if parent := filepath.Dir(path); parent != path {
		listing.Parent = displayPath(parent, home)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir() != entries[j].IsDir() {
			return entries[i].IsDir()
		}
		return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name())
	})
	for _, entry := range entries {
		if len(listing.Entries) == maxFolderEntries {
			listing.Truncated = true
			break
		}
		listing.Entries = append(listing.Entries, folderEntry{Name: entry.Name(), Dir: entry.IsDir()})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(listing)
}
