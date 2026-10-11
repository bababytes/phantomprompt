package mask

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

const protectedConfig = "Host build-server\n  HostName 10.0.0.17\n  IdentityFile ~/.ssh/deploy_key\n\nshort\n"

func TestProtectedFileLinesAreMasked(t *testing.T) {
	index := NewFileIndex()
	if added := index.AddFile(index.AddFolder("~/.ssh", false), protectedConfig); added != 3 {
		t.Fatalf("indexed %d lines", added)
	}
	store := NewStore(time.Minute, time.Hour, 8, DefaultPolicy())
	before, err := store.Create(DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	store.SetFiles(index)
	after, _ := store.Create(DefaultPolicy())
	for _, id := range []string{before, after, ""} {
		session, release, err := store.Acquire(id, "test")
		if err != nil {
			t.Fatal(err)
		}
		text := "cat output:\n     2\t  HostName 10.0.0.17\n3→  IdentityFile ~/.ssh/deploy_key\nHost build-server   \nshort\nunrelated line here"
		out, counts, err := session.Mask([]string{text})
		if err != nil || counts.File != 3 || strings.Contains(out[0], "10.0.0.17") || strings.Contains(out[0], "deploy_key") || strings.Contains(out[0], "build-server") || !strings.Contains(out[0], "     2\t  [PP_FILE_") || !strings.Contains(out[0], "short\nunrelated line here") {
			t.Fatalf("session %q: out %q counts %+v err %v", id, out, counts, err)
		}
		if session.Restore(out[0]) != text {
			t.Fatal("file line not restored")
		}
		release()
	}
}

func TestProtectedPathsValidation(t *testing.T) {
	for _, paths := range [][]string{{"~/.ssh"}, {"/etc/hosts", "~/.aws/credentials"}} {
		policy := DefaultPolicy()
		policy.ProtectedPaths = paths
		if err := ValidatePolicy(policy); err != nil {
			t.Fatalf("%v rejected: %v", paths, err)
		}
	}
	for _, paths := range [][]string{{"/"}, {"~"}, {"~/"}, {".ssh"}, {"~/.ssh", "~/.ssh/"}, {"/tmp/a\x00b"}} {
		policy := DefaultPolicy()
		policy.ProtectedPaths = paths
		if err := ValidatePolicy(policy); err != ErrPaths {
			t.Fatalf("%q accepted: %v", paths, err)
		}
	}
}

func TestBlockedFolderContentIsRemoved(t *testing.T) {
	policy := DefaultPolicy()
	policy.LenientFields = true
	s := NewSession(4096, 1<<20, policy)
	files := NewFileIndex()
	files.AddFile(files.AddFolder("~/.config/app.conf", false), "masked line from ~/.config/app.conf")
	files.AddFile(files.AddFolder("~/.ssh", true), "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBlockedKeyMaterial host")
	s.SetFiles(files)
	out, counts, err := s.Mask([]string{
		"     1→ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBlockedKeyMaterial host\n     2→other",
		"masked line from ~/.config/app.conf",
		"unrelated text",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out[0] != BlockedNotice || strings.Contains(out[1], "masked line") || out[2] != "unrelated text" || counts.BlockedTexts != 1 || counts.File != 1 {
		t.Fatalf("out %q counts %+v", out, counts)
	}
	if s.Restore(out[0]) != BlockedNotice {
		t.Fatal("blocked content must not be restorable")
	}
	kinds, err := s.Unmasked("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBlockedKeyMaterial host")
	if err != nil || len(kinds) == 0 || kinds[0] != "BLOCKED" {
		t.Fatalf("inspection must report blocked content: %v %v", kinds, err)
	}
}

func TestBlockedPathsMustBeProtected(t *testing.T) {
	if err := ValidatePolicy(Policy{ProtectedPaths: []string{"~/.ssh"}, BlockedPaths: []string{"~/.ssh/"}}); err != nil {
		t.Fatalf("blocked protected path rejected: %v", err)
	}
	if err := ValidatePolicy(Policy{Secrets: true, BlockedPaths: []string{"~/.aws"}}); err != ErrPaths {
		t.Fatal("blocked path outside the protected list accepted")
	}
}

func TestFolderAccessIsReportedOnceAndWithoutContent(t *testing.T) {
	s := NewSession(4096, 1<<20, DefaultPolicy())
	files := NewFileIndex()
	ssh := files.AddFolder("~/.ssh", true, "/home/me/.ssh", "~/.ssh", "$HOME/.ssh")
	notes := files.AddFolder("~/notes", false, "/home/me/notes", "~/notes")
	keyLine := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBlockedKeyMaterial host"
	files.AddFile(ssh, keyLine)
	files.AddFile(notes, "meeting notes about the merger plan")
	s.SetFiles(files)
	texts := []string{"     1→" + keyLine, "meeting notes about the merger plan", "unrelated"}
	_, first, err := s.Mask(texts)
	if err != nil || first.BlockedFolders["~/.ssh"] != 1 || first.MaskedFolders["~/notes"] != 1 || len(first.BlockedFolders)+len(first.MaskedFolders) != 2 {
		t.Fatalf("first access %+v %v", first, err)
	}
	if encoded, _ := json.Marshal(first); strings.Contains(string(encoded), "AAAAC3") || strings.Contains(string(encoded), "merger") {
		t.Fatalf("counts carry folder content: %s", encoded)
	}
	if _, resent, _ := s.Mask(texts); resent.FolderAccess() || resent.BlockedTexts != 1 || resent.File != 1 {
		t.Fatalf("history sent again was reported as a new access: %+v", resent)
	}
	if _, again, _ := s.Mask([]string{"cat output:\n" + keyLine}); again.BlockedFolders["~/.ssh"] != 1 {
		t.Fatalf("a new read of the blocked folder was not reported: %+v", again)
	}

	refused := s.RefusedAccess(map[string][]string{
		"t1": {"cat ~/.ssh/id_ed25519"},
		"t2": {"/home/me/.ssh"},
		"t3": {"cat ~/notes/plan"},
		"t4": {"grep ~/.sshrc", "/home/me/.ssh_old", "/tmp/home/me/.ssh"},
	})
	if refused.BlockedFolders["~/.ssh"] != 2 || len(refused.BlockedFolders) != 1 || refused.MaskedFolders != nil {
		t.Fatalf("refused tool calls %+v", refused)
	}
	if s.RefusedAccess(map[string][]string{"t1": {"cat ~/.ssh/id_ed25519"}}).FolderAccess() {
		t.Fatal("a retried request reported the same tool call twice")
	}
}
