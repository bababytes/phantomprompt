package extension

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

var ErrRunning = errors.New("another PhantomPrompt instance is already running")

var ErrUnsafeSocket = errors.New("the engine socket or its folder is not private to this user")

func appDirectory() (string, error) {
	config, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(config, "PhantomPrompt"), nil
}

func RulesPath() (string, error) {
	directory, err := appDirectory()
	if err != nil {
		return "", err
	}
	return filepath.Join(directory, "rules.json"), nil
}

// maxSocketPath stays below the 104-byte sun_path limit of macOS.
const maxSocketPath = 100

// GatewayKeyPath holds the fixed key of the App Store edition, whose users point Claude Code at
// the gateway once in their shell profile.
func GatewayKeyPath() (string, error) {
	directory, err := appDirectory()
	if err != nil {
		return "", err
	}
	return filepath.Join(directory, "gateway.key"), nil
}

func SocketPath() (string, error) {
	directory, err := appDirectory()
	if err != nil {
		return "", err
	}
	path := filepath.Join(directory, "engine.sock")
	if len(path) <= maxSocketPath {
		return path, nil
	}
	// A long home directory: use the per-user temporary directory, which macOS keeps private.
	return filepath.Join(os.TempDir(), "phantomprompt-"+strconv.Itoa(os.Getuid()), "engine.sock"), nil
}

func Listen(path string) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	if err := privatePath(filepath.Dir(path), true); err != nil {
		return nil, err
	}
	if conn, err := net.DialTimeout("unix", path, time.Second); err == nil {
		conn.Close()
		return nil, ErrRunning
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0600); err != nil {
		listener.Close()
		return nil, err
	}
	return listener, nil
}

// privateSocket checks that the socket and every folder above it belong to this user or the
// system, and that no other account can write to them, so no one else can stand in for the
// engine and hand out a proxy, CA and gateway of their own.
func privateSocket(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode().Type() != os.ModeSocket || !ownedBy(info, os.Getuid()) {
		return ErrUnsafeSocket
	}
	return privatePath(filepath.Dir(path), true)
}

// privatePath checks a folder and its ancestors; owner is whether the folder itself must belong
// to this user rather than to the system.
func privatePath(folder string, owner bool) error {
	resolved, err := filepath.EvalSymlinks(folder)
	if err != nil {
		return err
	}
	for path, own := resolved, owner; ; path, own = filepath.Dir(path), false {
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		writable := info.Mode().Perm()&0o022 != 0 && info.Mode()&os.ModeSticky == 0
		if !info.IsDir() || writable || !ownedBy(info, os.Getuid()) && (own || !ownedBy(info, 0)) {
			return ErrUnsafeSocket
		}
		if parent := filepath.Dir(path); parent == path {
			return nil
		}
	}
}

func ownedBy(info os.FileInfo, uid int) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == uid
}
