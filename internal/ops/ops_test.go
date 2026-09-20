package ops

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/pixelgo-io/pixelgo-ops/internal/config"
)

func TestInitCreatesEverything(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ops")

	if err := Init(dir); err != nil {
		t.Fatalf("Init: %v", err)
	}

	for _, d := range AllDirs() {
		p := filepath.Join(dir, d)
		fi, err := os.Stat(p)
		if err != nil {
			t.Errorf("%s missing: %v", d, err)
			continue
		}
		if !fi.IsDir() {
			t.Errorf("%s is not a directory", d)
		}
	}

	for _, f := range []string{config.FileAllowlist, config.FileName} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("%s missing: %v", f, err)
		}
	}
}

// audit/ is created but is not among the mounted directories: it holds records
// OF the agent, so the agent must not be able to reach them.
func TestAuditIsCreatedButNotMounted(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ops")
	if err := Init(dir); err != nil {
		t.Fatalf("Init: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, config.DirAudit)); err != nil {
		t.Fatalf("audit/ was not created: %v", err)
	}
	if Mounted(config.DirAudit) {
		t.Error("audit/ is listed as mounted; it must not be visible in the VM")
	}
	if _, ok := Dirs[config.DirAudit]; ok {
		t.Error("audit/ is in Dirs, so it would be given to the hypervisor as a mount")
	}
}

// The four directories that carry the guarantees must not be writable from the
// VM: rules it could rewrite, approvals it could grant itself, rejections it
// could delete.
func TestReadOnlyDirectories(t *testing.T) {
	for _, d := range []string{config.DirRules, config.DirPublic, config.DirApproved, config.DirRejected} {
		writable, ok := Dirs[d]
		if !ok {
			t.Errorf("%s is not in Dirs", d)
			continue
		}
		if writable {
			t.Errorf("%s is writable from the VM", d)
		}
	}
	for _, d := range []string{config.DirPending, config.DirLogs} {
		if !Dirs[d] {
			t.Errorf("%s should be writable from the VM", d)
		}
	}
}

func TestInitRefusesToOverwrite(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ops")
	if err := Init(dir); err != nil {
		t.Fatalf("first Init: %v", err)
	}

	// Somebody's rules are now in there.
	rules := filepath.Join(dir, config.DirRules, "rules.md")
	if err := os.WriteFile(rules, []byte("the objective\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := Init(dir)
	if !errors.Is(err, ErrExists) {
		t.Fatalf("second Init returned %v, want ErrExists", err)
	}

	content, err := os.ReadFile(rules)
	if err != nil || string(content) != "the objective\n" {
		t.Error("the second Init touched the existing rules")
	}
}

// An interrupted run must leave nothing a second run cannot finish. The config
// file is written last precisely so a half-done structure is not mistaken for a
// complete one.
func TestInitResumesAfterInterruption(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ops")

	// As if Init had been killed after creating some directories.
	if err := os.MkdirAll(filepath.Join(dir, config.DirRules), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, config.DirPending), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := Init(dir); err != nil {
		t.Fatalf("Init did not resume: %v", err)
	}
	for _, d := range AllDirs() {
		if _, err := os.Stat(filepath.Join(dir, d)); err != nil {
			t.Errorf("%s still missing after resume: %v", d, err)
		}
	}
}

func TestInitRejectsEmptyDir(t *testing.T) {
	if err := Init(""); err == nil {
		t.Error("Init accepted an empty path")
	}
}

func TestRulesEmpty(t *testing.T) {
	setup := func(t *testing.T) string {
		dir := filepath.Join(t.TempDir(), "ops")
		if err := Init(dir); err != nil {
			t.Fatalf("Init: %v", err)
		}
		return dir
	}

	t.Run("fresh structure is empty", func(t *testing.T) {
		empty, err := RulesEmpty(setup(t))
		if err != nil {
			t.Fatal(err)
		}
		if !empty {
			t.Error("a fresh rules/ reported as non-empty")
		}
	})

	t.Run("a real rules file counts", func(t *testing.T) {
		dir := setup(t)
		os.WriteFile(filepath.Join(dir, config.DirRules, "rules.md"), []byte("no pushing\n"), 0o644)

		empty, err := RulesEmpty(dir)
		if err != nil {
			t.Fatal(err)
		}
		if empty {
			t.Error("rules/ with a file reported as empty")
		}
	})

	// A file someone created and never filled in is not a set of rules. Letting
	// it pass would defeat the check at the moment it matters.
	t.Run("an empty file does not count", func(t *testing.T) {
		dir := setup(t)
		os.WriteFile(filepath.Join(dir, config.DirRules, "rules.md"), nil, 0o644)

		empty, err := RulesEmpty(dir)
		if err != nil {
			t.Fatal(err)
		}
		if !empty {
			t.Error("an empty file was taken for rules")
		}
	})

	t.Run("rules in a subdirectory count", func(t *testing.T) {
		dir := setup(t)
		sub := filepath.Join(dir, config.DirRules, "parts")
		os.MkdirAll(sub, 0o755)
		os.WriteFile(filepath.Join(sub, "limits.md"), []byte("no spending\n"), 0o644)

		empty, err := RulesEmpty(dir)
		if err != nil {
			t.Fatal(err)
		}
		if empty {
			t.Error("rules in a subdirectory were missed")
		}
	})

	t.Run("an empty subdirectory does not count", func(t *testing.T) {
		dir := setup(t)
		os.MkdirAll(filepath.Join(dir, config.DirRules, "parts"), 0o755)

		empty, err := RulesEmpty(dir)
		if err != nil {
			t.Fatal(err)
		}
		if !empty {
			t.Error("an empty subdirectory was taken for rules")
		}
	})

	t.Run("missing rules/ is an error", func(t *testing.T) {
		if _, err := RulesEmpty(t.TempDir()); err == nil {
			t.Error("RulesEmpty accepted a structure with no rules/")
		}
	})
}

// After Init, the configuration must load and validate - otherwise the first
// thing a new user does fails on the tool's own output.
func TestInitProducesValidConfig(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ops")
	if err := Init(dir); err != nil {
		t.Fatalf("Init: %v", err)
	}

	c, err := config.Load(dir)
	if err != nil {
		t.Fatalf("Load after Init: %v", err)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("the configuration written by Init does not validate: %v", err)
	}
}
