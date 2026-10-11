package applog

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func readDay(t *testing.T, folder, day string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(folder, day+".log"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func dayFiles(t *testing.T, folder string) []string {
	t.Helper()
	entries, err := os.ReadDir(folder)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func TestOneReadableFilePerDayWithPrivatePermissions(t *testing.T) {
	folder := filepath.Join(t.TempDir(), "PhantomPrompt")
	os.MkdirAll(folder, 0755)
	os.WriteFile(filepath.Join(folder, "2026-10-11.log"), []byte("time=earlier level=INFO msg=kept\n"), 0644)
	clock := time.Date(2026, 10, 11, 23, 59, 0, 0, time.Local)
	log, err := open(folder, func() time.Time { return clock }, keepDays, maxDayBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	if info, _ := os.Stat(folder); info.Mode().Perm() != 0700 {
		t.Fatalf("folder mode %v, want 0700", info.Mode().Perm())
	}
	log.Logger().Error("request blocked", "status", 422, "reason", "attachments_not_supported")
	clock = clock.Add(2 * time.Minute)
	log.Logger().Warn("protection turned off")

	first, second := readDay(t, folder, "2026-10-11"), readDay(t, folder, "2026-10-12")
	if !strings.HasPrefix(first, "time=earlier level=INFO msg=kept\n") || !strings.Contains(first, `level=ERROR msg="request blocked" status=422 reason=attachments_not_supported`) || strings.Contains(first, "protection") {
		t.Fatalf("first day:\n%s", first)
	}
	if strings.Count(second, "\n") != 1 || !strings.Contains(second, `level=WARN msg="protection turned off"`) {
		t.Fatalf("second day:\n%s", second)
	}
	for _, day := range []string{"2026-10-11", "2026-10-12"} {
		if info, _ := os.Stat(filepath.Join(folder, day+".log")); info.Mode().Perm() != 0600 {
			t.Fatalf("%s mode %v, want 0600", day, info.Mode().Perm())
		}
	}
	if log.Folder() != folder {
		t.Fatalf("folder %q", log.Folder())
	}
}

func TestOnlyTheNewestSevenDaysAreKept(t *testing.T) {
	folder := t.TempDir()
	for day := 1; day <= 9; day++ {
		os.WriteFile(filepath.Join(folder, time.Date(2026, 10, day, 12, 0, 0, 0, time.Local).Format(time.DateOnly)+".log"), nil, 0600)
	}
	for _, other := range []string{"notes.txt", "2026-09-01.log.bak", "PhantomPrompt.log"} {
		os.WriteFile(filepath.Join(folder, other), nil, 0600)
	}
	clock := time.Date(2026, 10, 10, 9, 0, 0, 0, time.Local)
	log, err := open(folder, func() time.Time { return clock }, keepDays, maxDayBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	want := []string{"2026-09-01.log.bak", "2026-10-04.log", "2026-10-05.log", "2026-10-06.log", "2026-10-07.log", "2026-10-08.log", "2026-10-09.log", "2026-10-10.log", "PhantomPrompt.log", "notes.txt"}
	if got := dayFiles(t, folder); !slices.Equal(got, want) {
		t.Fatalf("after open %v", got)
	}
	clock = clock.AddDate(0, 0, 1)
	log.Logger().Info("app started")
	want = slices.Insert(slices.Delete(want, 1, 2), 7, "2026-10-11.log")
	if got := dayFiles(t, folder); !slices.Equal(got, want) {
		t.Fatalf("after the day changed %v", got)
	}

	future := t.TempDir()
	for day := 20; day <= 28; day++ {
		os.WriteFile(filepath.Join(future, time.Date(2026, 10, day, 12, 0, 0, 0, time.Local).Format(time.DateOnly)+".log"), nil, 0600)
	}
	skewed, err := open(future, func() time.Time { return time.Date(2026, 10, 12, 9, 0, 0, 0, time.Local) }, keepDays, maxDayBytes)
	if err != nil {
		t.Fatal(err)
	}
	skewed.Logger().Info("app started")
	skewed.Close()
	if got := dayFiles(t, future); len(got) != keepDays || got[0] != "2026-10-12.log" || !strings.Contains(readDay(t, future, "2026-10-12"), "app started") {
		t.Fatalf("today's file must survive files dated in the future: %v", got)
	}
}

func TestDailyLimitWritesOneNoticeAndDropsTheRest(t *testing.T) {
	folder := t.TempDir()
	clock := time.Date(2026, 10, 11, 10, 0, 0, 0, time.Local)
	log, err := open(folder, func() time.Time { return clock }, keepDays, 400)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	for range 50 {
		log.Logger().Error("request blocked", "reason", "attachments_not_supported")
	}
	data := readDay(t, folder, "2026-10-11")
	lines := strings.Split(strings.TrimSuffix(data, "\n"), "\n")
	if strings.Count(data, "daily log limit reached") != 1 || !strings.Contains(lines[len(lines)-1], "daily log limit reached") || len(lines) < 3 || len(data) > 400+len(lines[len(lines)-1])+1 {
		t.Fatalf("limited day (%d bytes):\n%s", len(data), data)
	}
	clock = clock.AddDate(0, 0, 1)
	log.Logger().Error("request blocked", "reason", "next day")
	if !strings.Contains(readDay(t, folder, "2026-10-12"), "next day") {
		t.Fatal("the limit must not carry over to the next day")
	}
	log.Close()

	reopened, err := open(folder, func() time.Time { return clock.AddDate(0, 0, -1) }, keepDays, 400)
	if err != nil {
		t.Fatal(err)
	}
	reopened.Logger().Error("request blocked", "reason", "after a restart")
	reopened.Close()
	if after := readDay(t, folder, "2026-10-11"); after != data {
		t.Fatal("a restart wrote past the daily limit")
	}
}

func TestWriteErrorsNeverFailTheCaller(t *testing.T) {
	folder := t.TempDir()
	log, err := Open(folder)
	if err != nil {
		t.Fatal(err)
	}
	day := time.Now().Format(time.DateOnly)
	log.Logger().Info("before a failure")
	log.writer.file.Close()
	log.Logger().Info("lost while the file was broken")
	log.Logger().Info("after a failure")
	log.Close()
	log.Logger().Info("after close")
	data := readDay(t, folder, day)
	if !strings.Contains(data, "before a failure") || !strings.Contains(data, "after a failure") || strings.Contains(data, "lost while") || strings.Contains(data, "after close") || !log.writer.reported {
		t.Fatalf("the log did not recover after a write error (reported %v):\n%s", log.writer.reported, data)
	}

	if _, err := Open(filepath.Join(folder, "missing", "\x00bad")); err == nil {
		t.Fatal("an invalid folder was accepted")
	}
	var nothing *Log
	nothing.Logger().Error("no log")
	nothing.CaptureCrashes()
	if nothing.Folder() != "" || nothing.Close() != nil {
		t.Fatal("a missing log must do nothing")
	}
}

func TestCrashOutputGoesToTodaysFile(t *testing.T) {
	if folder := os.Getenv("APPLOG_CRASH_FOLDER"); folder != "" {
		log, err := Open(folder)
		if err != nil {
			os.Exit(3)
		}
		log.CaptureCrashes()
		panic("synthetic crash for the log")
	}
	folder := t.TempDir()
	crash := exec.Command(os.Args[0], "-test.run=^TestCrashOutputGoesToTodaysFile$")
	crash.Env = append(os.Environ(), "APPLOG_CRASH_FOLDER="+folder)
	if err := crash.Run(); err == nil {
		t.Fatal("the crashing process exited cleanly")
	}
	if data := readDay(t, folder, time.Now().Format(time.DateOnly)); !strings.Contains(data, "panic: synthetic crash for the log") || !strings.Contains(data, "goroutine ") {
		t.Fatalf("crash output missing:\n%s", data)
	}
}
