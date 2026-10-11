package applog

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"sync"
	"time"
)

const (
	keepDays    = 7
	maxDayBytes = 5 << 20
	timeLayout  = "2006-01-02T15:04:05.000Z07:00"
)

var dayFile = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}\.log$`)

type Log struct {
	logger *slog.Logger
	writer *dailyWriter
}

type dailyWriter struct {
	mu       sync.Mutex
	folder   string
	now      func() time.Time
	keep     int
	max      int64
	crashes  bool
	day      string
	file     *os.File
	size     int64
	full     bool
	closed   bool
	reported bool
}

func Open(folder string) (*Log, error) {
	return open(folder, time.Now, keepDays, maxDayBytes)
}

func open(folder string, now func() time.Time, keep int, limit int64) (*Log, error) {
	if err := os.MkdirAll(folder, 0700); err != nil {
		return nil, err
	}
	if err := os.Chmod(folder, 0700); err != nil {
		return nil, err
	}
	w := &dailyWriter{folder: folder, now: now, keep: keep, max: limit}
	if err := w.openDay(now().Format(time.DateOnly)); err != nil {
		return nil, err
	}
	return &Log{logger: slog.New(slog.NewTextHandler(w, nil)), writer: w}, nil
}

func (l *Log) Logger() *slog.Logger {
	if l == nil {
		return slog.New(slog.DiscardHandler)
	}
	return l.logger
}

func (l *Log) Folder() string {
	if l == nil {
		return ""
	}
	return l.writer.folder
}

func (l *Log) CaptureCrashes() {
	if l == nil {
		return
	}
	w := l.writer
	w.mu.Lock()
	defer w.mu.Unlock()
	w.crashes = true
	if w.file != nil {
		debug.SetCrashOutput(w.file, debug.CrashOptions{})
	}
}

func (l *Log) Close() error {
	if l == nil {
		return nil
	}
	w := l.writer
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	if w.crashes {
		debug.SetCrashOutput(nil, debug.CrashOptions{})
	}
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

func (w *dailyWriter) Write(line []byte) (int, error) {
	n := len(line)
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return n, nil
	}
	if day := w.now().Format(time.DateOnly); w.file == nil || day != w.day {
		if err := w.openDay(day); err != nil {
			w.report(err)
			return n, nil
		}
	}
	if w.full {
		return n, nil
	}
	if w.size+int64(len(line)) > w.max {
		w.full = true
		line = fmt.Appendf(nil, "time=%s level=WARN msg=\"daily log limit reached, later entries of this day are dropped\"\n", w.now().Format(timeLayout))
	}
	written, err := w.file.Write(line)
	w.size += int64(written)
	if err != nil {
		w.file.Close()
		w.file = nil
		w.report(err)
	}
	return n, nil
}

func (w *dailyWriter) openDay(day string) error {
	if w.file != nil {
		w.file.Close()
		w.file = nil
	}
	file, err := os.OpenFile(filepath.Join(w.folder, day+".log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err == nil {
		err = file.Chmod(0600)
	}
	if err != nil {
		file.Close()
		return err
	}
	if w.crashes {
		debug.SetCrashOutput(file, debug.CrashOptions{})
	}
	w.file, w.day, w.size, w.full = file, day, info.Size(), info.Size() >= w.max
	w.prune()
	return nil
}

func (w *dailyWriter) prune() {
	entries, err := os.ReadDir(w.folder)
	if err != nil {
		return
	}
	var days []string
	for _, entry := range entries {
		if entry.Type().IsRegular() && dayFile.MatchString(entry.Name()) && entry.Name() != w.day+".log" {
			days = append(days, entry.Name())
		}
	}
	for _, name := range days[:max(0, len(days)-w.keep+1)] {
		os.Remove(filepath.Join(w.folder, name))
	}
}

func (w *dailyWriter) report(err error) {
	if !w.reported {
		w.reported = true
		fmt.Fprintln(os.Stderr, "PhantomPrompt: cannot write the log:", err)
	}
}
