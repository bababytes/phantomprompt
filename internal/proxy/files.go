package proxy

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"

	"phantomprompt/internal/mask"
)

const (
	maxProtectedFile  = 1 << 20
	maxProtectedBytes = 16 << 20
	maxProtectedFiles = 2000
)

// A protected folder keeps a fingerprint of every line; a trusted one keeps only the values found
// in it, so it may be much larger.
var (
	protectedLimits = folderLimits{file: maxProtectedFile, bytes: maxProtectedBytes, files: maxProtectedFiles}
	trustedLimits   = folderLimits{file: 8 << 20, bytes: 256 << 20, files: 50000}
)

type folderLimits struct {
	file, bytes int64
	files       int
}

var errTooLarge = errors.New("folder too large; choose a narrower one")

type ProtectedPathStatus struct {
	Path    string `json:"path"`
	Blocked bool   `json:"blocked,omitempty"`
	Trusted bool   `json:"trusted,omitempty"`
	Files   int    `json:"files"`
	Lines   int    `json:"lines"`
	Values  int    `json:"values,omitempty"`
	Skipped int    `json:"skipped"`
	Error   string `json:"error,omitempty"`
}

type protectedFile struct {
	path     string
	size     int64
	modified int64
}

func expandHome(path string) (string, error) {
	if !strings.HasPrefix(path, "~/") {
		return filepath.Clean(path), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, path[2:]), nil
}

// scannedFile is the cached scan of a trusted file; it is read again only when it changes.
type scannedFile struct {
	size, modified int64
	binary         bool
	values         []string
}

// listProtected lists the files of a folder, leaving out the excluded paths and their content.
// unreadable counts the files that were left out only because they are too large or unreadable.
func listProtected(root string, limits folderLimits, excluded []os.FileInfo) (files []protectedFile, skipped, unreadable int, err error) {
	info, err := os.Lstat(root)
	if err != nil {
		return nil, 0, 0, err
	}
	if info.Mode().IsRegular() {
		if info.Size() > limits.file {
			if binaryFile(root) {
				return nil, 1, 0, nil
			}
			return nil, 0, 1, nil
		}
		return []protectedFile{{root, info.Size(), info.ModTime().UnixNano()}}, 0, 0, nil
	}
	if !info.IsDir() {
		return nil, 0, 0, errors.New("not a regular file or folder")
	}
	var total int64
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if excludedEntry(entry, excluded) {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if err != nil {
			unreadable++
			if entry != nil && entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			if !entry.IsDir() {
				skipped++
			}
			return nil
		}
		info, err := entry.Info()
		if err == nil && info.Size() > limits.file && binaryFile(path) {
			skipped++
			return nil
		}
		if err != nil || info.Size() > limits.file {
			unreadable++
			return nil
		}
		if total += info.Size(); total > limits.bytes || len(files) >= limits.files {
			return errTooLarge
		}
		files = append(files, protectedFile{path, info.Size(), info.ModTime().UnixNano()})
		return nil
	})
	return files, skipped, unreadable, err
}

// binaryFile tells a binary file, whose lines are never indexed, from a large text file.
func binaryFile(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	head := make([]byte, 8192)
	n, _ := io.ReadFull(file, head)
	return bytes.IndexByte(head[:n], 0) >= 0
}

func excludedEntry(entry fs.DirEntry, excluded []os.FileInfo) bool {
	if entry == nil || len(excluded) == 0 {
		return false
	}
	info, err := entry.Info()
	return err == nil && slices.ContainsFunc(excluded, func(protected os.FileInfo) bool { return os.SameFile(info, protected) })
}

func unreadableError(files int) string {
	return fmt.Sprintf("%d file(s) larger than %d MiB or unreadable; protection is paused until they are removed or the folder is narrowed", files, maxProtectedFile>>20)
}

func signature(paths, blocked, trusted []string) (string, [][]protectedFile, []ProtectedPathStatus) {
	paths = append(slices.Clone(paths), trusted...)
	digest := sha256.New()
	listed := make([][]protectedFile, len(paths))
	statuses := make([]ProtectedPathStatus, len(paths))
	for i, entry := range paths {
		statuses[i].Path = entry
		statuses[i].Blocked = slices.ContainsFunc(blocked, func(b string) bool { return filepath.Clean(b) == filepath.Clean(entry) })
		statuses[i].Trusted = i >= len(paths)-len(trusted)
		root, err := expandHome(entry)
		unreadable := 0
		if err == nil && statuses[i].Trusted {
			// Files of a protected folder inside a trusted one are not scanned: their values must
			// not become public.
			var excluded []os.FileInfo
			for _, protected := range paths[:len(paths)-len(trusted)] {
				if expanded, err := expandHome(protected); err == nil {
					if info, err := os.Stat(expanded); err == nil {
						excluded = append(excluded, info)
					}
				}
			}
			listed[i], statuses[i].Skipped, unreadable, err = listProtected(root, trustedLimits, excluded)
			statuses[i].Skipped += unreadable
		} else if err == nil {
			listed[i], statuses[i].Skipped, unreadable, err = listProtected(root, protectedLimits, nil)
		}
		switch {
		case errors.Is(err, fs.ErrNotExist):
			statuses[i].Error = "not found"
		case errors.Is(err, fs.ErrPermission):
			statuses[i].Error = "no permission to read"
		case err != nil:
			statuses[i].Error = err.Error()
		case unreadable > 0 && !statuses[i].Trusted:
			statuses[i].Error = unreadableError(unreadable)
		}
		fmt.Fprintf(digest, "%s\x00%t\x00%t\x00%s\x00", entry, statuses[i].Blocked, statuses[i].Trusted, statuses[i].Error)
		for _, file := range listed[i] {
			fmt.Fprintf(digest, "%s\x00%d\x00%d\x00", file.path, file.size, file.modified)
		}
	}
	return string(digest.Sum(nil)), listed, statuses
}

func pathReferences(entry string) []string {
	root, err := expandHome(entry)
	if err != nil {
		return nil
	}
	references := []string{root}
	if rest, home := strings.CutPrefix(entry, "~/"); home {
		references = append(references, entry, "$HOME/"+rest, "${HOME}/"+rest)
	}
	return references
}

func (a *App) buildFileIndex(policy mask.Policy, listed [][]protectedFile, statuses []ProtectedPathStatus) *mask.FileIndex {
	index := mask.NewFileIndex()
	if rules := detectionRules(policy); rules != a.scannedRules {
		a.scanned, a.scannedRules = nil, rules
	}
	var changed []protectedFile
	for i, files := range listed {
		if !statuses[i].Trusted || statuses[i].Error != "" {
			continue
		}
		for _, file := range files {
			if result, cached := a.scanned[file.path]; !cached || result.size != file.size || result.modified != file.modified {
				changed = append(changed, file)
			}
		}
	}
	fresh := scanFiles(policy, changed)
	scanned := make(map[string]scannedFile)
	for i, files := range listed {
		if !statuses[i].Trusted {
			folder := index.AddFolder(statuses[i].Path, statuses[i].Blocked, pathReferences(statuses[i].Path)...)
			unreadable := 0
			for _, file := range files {
				data, err := os.ReadFile(file.path)
				if err != nil {
					unreadable++
					continue
				}
				if bytes.IndexByte(data[:min(len(data), 8192)], 0) >= 0 {
					statuses[i].Skipped++
					continue
				}
				statuses[i].Files++
				statuses[i].Lines += index.AddFile(folder, string(data))
			}
			if unreadable > 0 && statuses[i].Error == "" {
				statuses[i].Error = unreadableError(unreadable)
			}
			if statuses[i].Error != "" {
				index.SetIncomplete(folder)
			}
			continue
		}
		if statuses[i].Error != "" {
			// A folder that cannot be read whole is not trusted in part.
			continue
		}
		values := make(map[string]bool)
		for _, file := range files {
			result, ok := fresh[file.path]
			if !ok {
				result = a.scanned[file.path]
			}
			scanned[file.path] = result
			if result.binary {
				statuses[i].Skipped++
				continue
			}
			statuses[i].Files++
			for _, value := range result.values {
				values[value] = true
			}
			index.AddTrustedValues(result.values)
		}
		statuses[i].Values = len(values)
	}
	a.scanned = scanned
	return index
}

// scanFiles scans files in parallel, one scanner per worker, leaving a core for model requests.
func scanFiles(policy mask.Policy, files []protectedFile) map[string]scannedFile {
	results := make(map[string]scannedFile, len(files))
	workers := min(max(1, runtime.GOMAXPROCS(0)-1), len(files))
	scanners := make([]*mask.ValueScanner, 0, workers)
	for range workers {
		scanner, err := mask.NewValueScanner(policy)
		if err != nil {
			return results
		}
		scanners = append(scanners, scanner)
	}
	jobs := make(chan protectedFile)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, scanner := range scanners {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for file := range jobs {
				result := scanFile(scanner, file)
				mu.Lock()
				results[file.path] = result
				mu.Unlock()
			}
		}()
	}
	for _, file := range files {
		jobs <- file
	}
	close(jobs)
	wg.Wait()
	return results
}

func scanFile(scanner *mask.ValueScanner, file protectedFile) scannedFile {
	result := scannedFile{size: file.size, modified: file.modified}
	data, err := os.ReadFile(file.path)
	if err != nil || bytes.IndexByte(data[:min(len(data), 8192)], 0) >= 0 {
		result.binary = true
		return result
	}
	result.values = scanner.Values(string(data))
	return result
}

// detectionRules identifies what the detectors find; a change means trusted files are scanned again.
func detectionRules(policy mask.Policy) string {
	policy.ProtectedPaths, policy.BlockedPaths, policy.TrustedPaths = nil, nil, nil
	data, _ := json.Marshal(policy)
	return string(data)
}

func (a *App) refreshProtectedFiles(force bool) {
	a.filesMu.Lock()
	defer a.filesMu.Unlock()
	policy := a.Policy()
	current, listed, statuses := signature(policy.ProtectedPaths, policy.BlockedPaths, policy.TrustedPaths)
	if !force && current == a.filesSignature {
		return
	}
	var index *mask.FileIndex
	if len(statuses) > 0 {
		index = a.buildFileIndex(policy, listed, statuses)
	}
	a.filesSignature = current
	a.filesStatus = statuses
	a.Sessions.SetFiles(index)
}

func (a *App) ProtectedPathStatus() []ProtectedPathStatus { return a.pathStatus(false) }

func (a *App) TrustedPathStatus() []ProtectedPathStatus { return a.pathStatus(true) }

func (a *App) pathStatus(trusted bool) []ProtectedPathStatus {
	a.filesMu.Lock()
	defer a.filesMu.Unlock()
	statuses := []ProtectedPathStatus{}
	for _, status := range a.filesStatus {
		if status.Trusted == trusted {
			statuses = append(statuses, status)
		}
	}
	return statuses
}
