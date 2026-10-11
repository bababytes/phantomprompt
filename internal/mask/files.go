package mask

import (
	"crypto/sha256"
	"slices"
	"strings"
	"unicode"
)

const MinProtectedLine = 12
const lineSpace = " \t\r\v\f"

// BlockedNotice replaces a whole text that contains a line from a blocked folder. It carries no
// placeholder, so the content can never be restored or sent.
const BlockedNotice = "[PhantomPrompt removed this content: it comes from a blocked folder]"

// FileIndex keeps fingerprints of protected lines and the protected folder each line comes from.
type FileIndex struct {
	lines   map[[32]byte]int
	lengths map[int]struct{}
	folders []protectedFolder
	trusted map[string]bool
}

type protectedFolder struct {
	label      string
	blocked    bool
	incomplete bool
	references []string
}

func NewFileIndex() *FileIndex {
	return &FileIndex{lines: make(map[[32]byte]int), lengths: make(map[int]struct{})}
}

func protectedLine(line string) bool {
	if len(line) < MinProtectedLine {
		return false
	}
	return strings.IndexFunc(line, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) >= 0
}

func (f *FileIndex) AddFolder(label string, blocked bool, references ...string) int {
	f.folders = append(f.folders, protectedFolder{label: label, blocked: blocked, references: slices.DeleteFunc(slices.Clone(references), func(reference string) bool { return reference == "" })})
	return len(f.folders) - 1
}

// SetIncomplete marks a folder whose files could not all be indexed; no text is masked while a
// protected folder is incomplete, because its missing lines would be sent as they are.
func (f *FileIndex) SetIncomplete(folder int) {
	f.folders[folder].incomplete = true
}

func (f *FileIndex) ready(paths []string) bool {
	for _, path := range paths {
		if f == nil || !slices.ContainsFunc(f.folders, func(folder protectedFolder) bool { return folder.label == path && !folder.incomplete }) {
			return false
		}
	}
	return true
}

func (f *FileIndex) AddFile(folder int, content string) int {
	added := 0
	for _, line := range strings.Split(content, "\n") {
		line = strings.Trim(line, lineSpace)
		if !protectedLine(line) {
			continue
		}
		key := sha256.Sum256([]byte(line))
		owner, known := f.lines[key]
		if !known {
			f.lengths[len(line)] = struct{}{}
			added++
		}
		if !known || f.folders[folder].blocked && !f.folders[owner].blocked {
			f.lines[key] = folder
		}
	}
	return added
}

// blockedFolder reports the blocked folder of the first line of text that comes from one.
func (f *FileIndex) blockedFolder(text string) (string, bool) {
	if f.Len() == 0 {
		return "", false
	}
	for offset := 0; offset < len(text); {
		end := strings.IndexByte(text[offset:], '\n')
		if end < 0 {
			end = len(text)
		} else {
			end += offset
		}
		if _, _, folder, found := f.match(text, offset, end); found && f.folders[folder].blocked {
			return f.folders[folder].label, true
		}
		offset = end + 1
	}
	return "", false
}

// AddTrustedValues registers values that are sent as they are wherever they appear: the values
// found in a trusted folder.
func (f *FileIndex) AddTrustedValues(values []string) {
	if f.trusted == nil {
		f.trusted = make(map[string]bool, len(values))
	}
	for _, value := range values {
		f.trusted[value] = true
	}
}

// trustedValue also accepts a value spread over lines that a Read result numbered ("  6→…").
func (f *FileIndex) trustedValue(value string) bool {
	if f == nil || len(f.trusted) == 0 {
		return false
	}
	if f.trusted[value] {
		return true
	}
	if !strings.Contains(value, "\n") {
		return false
	}
	lines := strings.Split(value, "\n")
	for i := 1; i < len(lines); i++ {
		lines[i] = lines[i][lineNumberPrefix(lines[i]):]
	}
	return f.trusted[strings.Join(lines, "\n")]
}

func (f *FileIndex) Len() int {
	if f == nil {
		return 0
	}
	return len(f.lines)
}

func (f *FileIndex) lookup(line string) (int, bool) {
	if _, possible := f.lengths[len(line)]; !possible {
		return 0, false
	}
	folder, found := f.lines[sha256.Sum256([]byte(line))]
	return folder, found
}

func (f *FileIndex) referencedFolders(text string) []int {
	var found []int
	for i, folder := range f.folders {
		if slices.ContainsFunc(folder.references, func(reference string) bool { return mentionsPath(text, reference) }) {
			found = append(found, i)
		}
	}
	return found
}

func mentionsPath(text, path string) bool {
	for offset := 0; offset < len(text); {
		i := strings.Index(text[offset:], path)
		if i < 0 {
			return false
		}
		start, end := offset+i, offset+i+len(path)
		if (start == 0 || !pathByte(text[start-1])) && (end == len(text) || text[end] == '/' || !pathByte(text[end])) {
			return true
		}
		offset = start + 1
	}
	return false
}

func pathByte(c byte) bool {
	return c == '/' || c == '.' || c == '_' || c == '-' || c == '~' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func lineNumberPrefix(line string) int {
	i := 0
	for i < len(line) && line[i] == ' ' {
		i++
	}
	digits := i
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	if i == digits {
		return 0
	}
	switch {
	case strings.HasPrefix(line[i:], "\t"):
		return i + 1
	case strings.HasPrefix(line[i:], "→"):
		return i + len("→")
	}
	return 0
}

func trimmedSpan(text string, start, end int) (int, int) {
	for start < end && strings.IndexByte(lineSpace, text[start]) >= 0 {
		start++
	}
	for end > start && strings.IndexByte(lineSpace, text[end-1]) >= 0 {
		end--
	}
	return start, end
}

func (f *FileIndex) match(text string, offset, end int) (int, int, int, bool) {
	start, stop := trimmedSpan(text, offset, end)
	if stop > start {
		if folder, found := f.lookup(text[start:stop]); found {
			return start, stop, folder, true
		}
	}
	if prefix := lineNumberPrefix(text[offset:end]); prefix > 0 {
		start, stop = trimmedSpan(text, offset+prefix, end)
		if stop > start {
			if folder, found := f.lookup(text[start:stop]); found {
				return start, stop, folder, true
			}
		}
	}
	return 0, 0, 0, false
}

func (f *FileIndex) spans(text string) []replacement {
	if f.Len() == 0 {
		return nil
	}
	var spans []replacement
	for offset := 0; offset < len(text); {
		end := strings.IndexByte(text[offset:], '\n')
		if end < 0 {
			end = len(text)
		} else {
			end += offset
		}
		if start, stop, folder, found := f.match(text, offset, end); found {
			spans = append(spans, replacement{Start: start, End: stop, Kind: "FILE", Name: f.folders[folder].label})
		}
		offset = end + 1
	}
	return spans
}
