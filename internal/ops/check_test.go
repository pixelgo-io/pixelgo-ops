package ops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pixelgo-io/pixelgo-ops/internal/config"
)

// find returns the result whose name contains sub, and fails if there is none.
// Matching on a fragment rather than the whole string keeps these tests from
// breaking every time a message is reworded.
func find(t *testing.T, results []Result, sub string) Result {
	t.Helper()
	for _, r := range results {
		if contains(r.Name, sub) {
			return r
		}
	}
	t.Fatalf("no check mentioning %q; got %v", sub, names(results))
	return Result{}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

func names(results []Result) []string {
	var out []string
	for _, r := range results {
		out = append(out, r.Name)
	}
	return out
}

func anyFailed(results []Result) bool {
	for _, r := range results {
		if r.Failed() {
			return true
		}
	}
	return false
}

// A structure straight out of init, with rules written, is the case that has to
// pass - otherwise "check" tells a correctly installed person that they are
// wrong, and after that they stop reading it.
func TestChecksPassOnACompleteStructure(t *testing.T) {
	dir := t.TempDir()
	if err := Init(dir); err != nil {
		t.Fatalf("init: %v", err)
	}
	writeRule(t, dir, "rules.md", "do not push to main")

	results := RunChecks(dir)

	if anyFailed(results) {
		for _, r := range results {
			if r.Failed() {
				t.Errorf("unexpected failure: %s - %s", r.Name, r.Remedy)
			}
		}
	}
}

// Nothing there at all: the first line must say so, and must say to run init
// rather than leaving the person to guess.
func TestChecksReportAMissingStructure(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "not-created")

	results := RunChecks(dir)

	first := results[0]
	if first.OK {
		t.Fatalf("a missing directory passed the first check")
	}
	if !contains(first.Remedy, "init") {
		t.Errorf("the remedy does not mention init: %q", first.Remedy)
	}

	// The later checks must not be invented for a structure that is not there.
	for _, r := range results {
		if contains(r.Name, "config") || contains(r.Name, "rules/") {
			t.Errorf("checked %q despite there being no structure", r.Name)
		}
	}
}

// The check that stops "up". A rules/ holding only an empty file counts as
// empty - the same rule RulesEmpty applies - so this must fail, not pass.
func TestChecksReportEmptyRules(t *testing.T) {
	dir := t.TempDir()
	if err := Init(dir); err != nil {
		t.Fatalf("init: %v", err)
	}
	writeRule(t, dir, "placeholder.md", "")

	results := RunChecks(dir)

	r := find(t, results, "rules/")
	if r.OK {
		t.Error("empty rules/ passed")
	}
	if !contains(r.Remedy, "root-task") {
		t.Errorf("the remedy does not point at the guidance: %q", r.Remedy)
	}
}

// A config with a bad value must be reported as invalid rather than as
// unloadable: the two have different causes and different fixes.
func TestChecksReportAnInvalidConfig(t *testing.T) {
	dir := t.TempDir()
	if err := Init(dir); err != nil {
		t.Fatalf("init: %v", err)
	}
	writeRule(t, dir, "rules.md", "stop on repeated failures")

	cfg := config.Default()
	cfg.OpsDir = dir
	cfg.MemoryMB = 64 // below the minimum
	if err := cfg.Save(dir); err != nil {
		t.Fatalf("save: %v", err)
	}

	results := RunChecks(dir)

	if r := find(t, results, "config loads"); !r.OK {
		t.Error("a config with a valid syntax should still load")
	}
	if r := find(t, results, "config is valid"); r.OK {
		t.Error("MEMORY_MB below the minimum passed validation")
	}
}

// An unparseable config must not take the rules check down with it: they are
// independent problems and a person fixing one wants to know about the other.
func TestChecksSurviveAnUnloadableConfig(t *testing.T) {
	dir := t.TempDir()
	if err := Init(dir); err != nil {
		t.Fatalf("init: %v", err)
	}
	writeRule(t, dir, "rules.md", "ask before deleting anything")

	if err := os.WriteFile(config.Path(dir), []byte("NOT_A_SETTING=1\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	results := RunChecks(dir)

	if r := find(t, results, "config loads"); r.OK {
		t.Error("an unknown setting should fail the load")
	}
	if r := find(t, results, "rules/"); !r.OK {
		t.Errorf("the rules check was lost: %s", r.Remedy)
	}
	// And the nil config must not have taken the process with it.
	find(t, results, "allowlist")
}

// A directory removed after init is the state a half-finished or
// hand-edited install is in, and it has to be named rather than inferred.
func TestChecksReportAMissingDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := Init(dir); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(dir, config.DirPending)); err != nil {
		t.Fatalf("remove: %v", err)
	}

	results := RunChecks(dir)

	r := find(t, results, "directories")
	if r.OK {
		t.Error("a missing directory passed")
	}
	if !contains(r.Remedy, config.DirPending) {
		t.Errorf("the remedy does not name the missing directory: %q", r.Remedy)
	}
}

// Two independent faults must both be reported.
//
// This is the regression test for a cascade that was too wide: a missing
// subdirectory used to stop the config from being checked at all, so a report
// could name one problem and hide another that had nothing to do with it. Only
// a missing opsDir may gate the later checks.
func TestChecksReportIndependentFaultsTogether(t *testing.T) {
	dir := t.TempDir()
	if err := Init(dir); err != nil {
		t.Fatalf("init: %v", err)
	}
	writeRule(t, dir, "rules.md", "a rule")

	// Fault one: a directory is gone.
	if err := os.RemoveAll(filepath.Join(dir, config.DirPending)); err != nil {
		t.Fatalf("remove: %v", err)
	}
	// Fault two: unrelated, and still readable.
	cfg := config.Default()
	cfg.OpsDir = dir
	cfg.CPUs = 0
	if err := cfg.Save(dir); err != nil {
		t.Fatalf("save: %v", err)
	}

	results := RunChecks(dir)

	if r := find(t, results, "directories"); r.OK {
		t.Error("the missing directory was not reported")
	}
	if r := find(t, results, "config is valid"); r.OK {
		t.Error("the invalid config was hidden by the missing directory")
	}
	if r := find(t, results, "rules/"); !r.OK {
		t.Errorf("the rules check was lost: %s", r.Remedy)
	}
}

// The checks that cannot be written yet are pending, not failures: they are our
// problem, not the person's, and reporting them as failures would send someone
// hunting for a fault that is not theirs.
func TestUnimplementedChecksArePendingNotFailures(t *testing.T) {
	dir := t.TempDir()
	if err := Init(dir); err != nil {
		t.Fatalf("init: %v", err)
	}
	writeRule(t, dir, "rules.md", "a rule")

	results := RunChecks(dir)

	pending := 0
	for _, r := range results {
		if r.Pending {
			pending++
			if r.Failed() {
				t.Errorf("%q is both pending and failed", r.Name)
			}
			if r.Remedy == "" {
				t.Errorf("%q is pending with no explanation", r.Name)
			}
		}
	}
	if pending == 0 {
		t.Error("the hypervisor and proxy checks are not written yet and should say so")
	}
}

func writeRule(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, config.DirRules, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
}
