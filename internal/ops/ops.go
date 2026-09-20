// Package ops manages the structure on the host and the approval mechanism.
//
// The structure is the one from the README, plus audit/:
//
//	rules/        read-only in the VM
//	public/       read-only in the VM
//	approved/     read-only in the VM
//	rejected/     read-only in the VM
//	pending/      read-write
//	logs/         read-write
//	audit/        NOT MOUNTED - see AuditDir in config
package ops

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pixelgo-io/pixelgo-ops/internal/config"
)

// Dirs are the directories created by "init".
// The value says whether the VM can write to them.
var Dirs = map[string]bool{
	config.DirRules:    false,
	config.DirPublic:   false,
	config.DirApproved: false,
	config.DirRejected: false,
	config.DirPending:  true,
	config.DirLogs:     true,
}

// Mounted reports whether a directory created by Init is visible inside the VM
// at all.
//
// audit/ is the one that is not: it holds the proxy's traffic log, and later
// the gateway's budget log. Those are records OF the agent, so the agent must
// not be able to reach them. It is deliberately not in Dirs, because Dirs is
// "what gets mounted, and how" - audit/ is not part of that question.
func Mounted(dir string) bool {
	_, ok := Dirs[dir]
	return ok
}

// AllDirs is every directory Init creates, mounted or not.
//
// Fixed order, not map iteration: this drives what "init" prints, and a listing
// that comes out shuffled on every run is harder to read and harder to compare
// against the README. Read-only first, then writable, then unmounted - the same
// order as the README's structure section.
func AllDirs() []string {
	return []string{
		config.DirRules,
		config.DirPublic,
		config.DirApproved,
		config.DirRejected,
		config.DirPending,
		config.DirLogs,
		config.DirAudit,
	}
}

// ErrExists is returned by Init when the structure is already there.
//
// A separate error rather than a silent success: re-running init on a directory
// that already holds someone's rules must not look like it did something, and
// must certainly not reset anything.
var ErrExists = errors.New("structure already exists")

// Init creates the structure and the default configuration. Does not overwrite.
//
// Safe to interrupt: every step is idempotent, so a run that stops halfway
// leaves nothing a second run cannot finish. That matters because the first
// thing a new user does is run this with sudo and Ctrl-C it when they realise
// they meant a different directory.
func Init(opsDir string) error {
	if opsDir == "" {
		return fmt.Errorf("init: empty directory")
	}

	// "Already initialised" means the config file is there. Checking the
	// directories instead would call a half-finished run complete.
	if _, err := os.Stat(config.Path(opsDir)); err == nil {
		return fmt.Errorf("init: %s: %w", opsDir, ErrExists)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("init: %s: %w", opsDir, err)
	}

	if err := os.MkdirAll(opsDir, 0o755); err != nil {
		return fmt.Errorf("init: %s: %w", opsDir, err)
	}

	for _, d := range AllDirs() {
		p := filepath.Join(opsDir, d)
		if err := os.MkdirAll(p, 0o755); err != nil {
			return fmt.Errorf("init: %s: %w", p, err)
		}
	}

	// The allowlist starts empty on purpose. The README's argument: start with
	// almost nothing and add what is justified, because the reverse - permissive
	// first, narrowed later - leaves you unable to tell what is necessary from
	// what slipped in.
	if err := touch(filepath.Join(opsDir, config.FileAllowlist)); err != nil {
		return fmt.Errorf("init: allowlist: %w", err)
	}

	cfg := config.Default()
	cfg.OpsDir = opsDir
	// Written last: Init treats this file as the marker that the structure is
	// complete, so an interrupted run must not leave it behind.
	if err := cfg.Save(opsDir); err != nil {
		return fmt.Errorf("init: config: %w", err)
	}
	return nil
}

// touch creates an empty file if it is not already there, leaving an existing
// one untouched.
func touch(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	return f.Close()
}

// Request is an approval request written by the agent into pending/.
type Request struct {
	ID     string
	Path   string
	Action string
}

// ListPending returns the requests in pending/.
func ListPending(opsDir string) ([]Request, error) { panic("not implemented") }

// Approve moves the request from pending/ to approved/.
//
// The move IS the act of approval. The agent has no write access to approved/,
// precisely so it cannot grant itself one.
func Approve(opsDir, id string) error { panic("not implemented") }

// Reject moves the request to rejected/. Final - a rejected request is not
// resent, not reworded, not split into smaller pieces.
func Reject(opsDir, id string) error { panic("not implemented") }

// RulesEmpty reports whether rules/ is empty.
//
// "up" refuses to start if it is: an agent with no written rules has no way
// to follow them.
//
// A directory holding only empty files counts as empty: a file someone created
// and never filled in is not a set of rules, and letting it pass would defeat
// the check at exactly the moment it matters.
func RulesEmpty(opsDir string) (bool, error) {
	dir := filepath.Join(opsDir, config.DirRules)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, fmt.Errorf("rules: %s: %w", dir, err)
	}

	for _, e := range entries {
		if e.IsDir() {
			// Recurse: rules split across subdirectories are still rules.
			sub, err := dirHasContent(filepath.Join(dir, e.Name()))
			if err != nil {
				return false, err
			}
			if sub {
				return false, nil
			}
			continue
		}
		info, err := e.Info()
		if err != nil {
			return false, fmt.Errorf("rules: %s: %w", e.Name(), err)
		}
		if info.Size() > 0 {
			return false, nil
		}
	}
	return true, nil
}

// dirHasContent reports whether a directory holds any non-empty file, at any
// depth.
func dirHasContent(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, fmt.Errorf("rules: %s: %w", dir, err)
	}
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		if e.IsDir() {
			sub, err := dirHasContent(p)
			if err != nil {
				return false, err
			}
			if sub {
				return true, nil
			}
			continue
		}
		info, err := e.Info()
		if err != nil {
			return false, fmt.Errorf("rules: %s: %w", p, err)
		}
		if info.Size() > 0 {
			return true, nil
		}
	}
	return false, nil
}
