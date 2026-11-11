package ops

// The checks behind "pixelgo-ops check".
//
// They live here rather than in cli/ for one reason: they are the part worth
// testing, and a function that prints as it goes cannot be tested without
// capturing output. RunChecks decides, cmdCheck prints.
//
// Nothing here modifies anything. "check" is the command a person runs when
// something is already wrong, and a diagnostic that changes state is one you
// cannot run twice to see whether the change helped.

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/pixelgo-io/pixelgo-ops/internal/config"
)

// Result is one line of the report.
//
// Pending is separate from OK=false on purpose. "libvirt is missing" is the
// person's problem and they can fix it; "this check is not written yet" is
// ours, and presenting the two the same way would either send someone looking
// for a fault that is not theirs, or hide the fact that the list is short.
type Result struct {
	Name    string // what was checked, in the present tense
	OK      bool
	Pending bool   // not implementable yet - neither pass nor fail
	Remedy  string // what to do about it; empty when OK
}

// Failed reports whether this result should make "check" exit non-zero.
func (r Result) Failed() bool { return !r.OK && !r.Pending }

// RunChecks verifies the environment and returns one Result per check, in a
// fixed order: the structure first, then the configuration, then what the
// structure is for, then the external tools. That order is also the order in
// which things go wrong - a missing directory makes every later check
// meaningless - so reading the report from the top gives the first real cause
// rather than the loudest symptom.
func RunChecks(opsDir string) []Result {
	var out []Result

	// 1. The structure.
	//
	// dirOK gates everything after it, and nothing else does. Only a missing
	// opsDir makes the later checks meaningless; a missing subdirectory does
	// not - the config file is still readable and still worth validating. The
	// first version of this gated on the subdirectories too, and the result was
	// a report that hid a broken config behind one absent directory, which is
	// the opposite of what someone running "check" after an install needs.
	dirOK := true

	fi, err := os.Stat(opsDir)
	switch {
	case err != nil:
		dirOK = false
		out = append(out, Result{
			Name:   fmt.Sprintf("%s exists", opsDir),
			Remedy: fmt.Sprintf("run: pixelgo-ops init --dir %s", opsDir),
		})
	case !fi.IsDir():
		dirOK = false
		out = append(out, Result{
			Name:   fmt.Sprintf("%s exists", opsDir),
			Remedy: "it exists but is not a directory",
		})
	default:
		out = append(out, Result{Name: fmt.Sprintf("%s exists", opsDir), OK: true})
	}

	// Every directory init creates. Reported as one line rather than seven:
	// if several are missing the cause is the same, and the remedy is the same.
	if dirOK {
		var missing []string
		for _, d := range AllDirs() {
			if fi, err := os.Stat(filepath.Join(opsDir, d)); err != nil || !fi.IsDir() {
				missing = append(missing, d+"/")
			}
		}
		if len(missing) > 0 {
			out = append(out, Result{
				Name:   "all directories are present",
				Remedy: fmt.Sprintf("missing: %v - run: pixelgo-ops init --dir %s", missing, opsDir),
			})
		} else {
			out = append(out, Result{Name: "all directories are present", OK: true})
		}
	}

	// 2. The configuration.
	if dirOK {
		cfg, err := config.Load(opsDir)
		if err != nil {
			out = append(out, Result{
				Name:   "config loads",
				Remedy: err.Error(),
			})
		} else {
			out = append(out, Result{Name: "config loads", OK: true})

			if err := cfg.Validate(); err != nil {
				out = append(out, Result{
					Name:   "config is valid",
					Remedy: err.Error(),
				})
			} else {
				out = append(out, Result{Name: "config is valid", OK: true})
			}
		}

		// The allowlist is a file, not a directory, so the loop above misses it.
		// An absent one is not the same as an empty one: empty is the documented
		// starting state, absent means init never finished.
		//
		// The path is built from opsDir rather than from cfg, because cfg is nil
		// when the config failed to load and this check still has an answer.
		if _, err := os.Stat(filepath.Join(opsDir, config.FileAllowlist)); err != nil {
			out = append(out, Result{
				Name: "the allowlist file exists",
				Remedy: fmt.Sprintf("%s is missing - run: pixelgo-ops init --dir %s",
					config.FileAllowlist, opsDir),
			})
		} else {
			out = append(out, Result{Name: "the allowlist file exists", OK: true})
		}
	}

	// 3. What the structure is for.
	//
	// This is the check that stops "up", and the only one whose remedy is work
	// rather than a command: an agent with no written rules has nothing to
	// follow, and nothing the tool does afterwards can substitute for it.
	if dirOK {
		empty, err := RulesEmpty(opsDir)
		switch {
		case err != nil:
			out = append(out, Result{Name: "rules/ is not empty", Remedy: err.Error()})
		case empty:
			out = append(out, Result{
				Name: "rules/ is not empty",
				Remedy: fmt.Sprintf("write the document that governs the agent into %s - see docs/root-task.md",
					filepath.Join(opsDir, config.DirRules)),
			})
		default:
			out = append(out, Result{Name: "rules/ is not empty", OK: true})
		}
	}

	// 4. The external tools.
	//
	// Not called, because they are not written: hypervisor.Detect and
	// Proxy.Available both panic. Listing them as pending keeps the report
	// honest - the alternative is a report that passes cleanly on a machine
	// where nothing could actually start.
	out = append(out,
		Result{
			Name:    "the hypervisor is usable",
			Pending: true,
			Remedy:  "not implemented yet - the KVM and Hyper-V backends are skeletons",
		},
		Result{
			Name:    "the proxy is installed",
			Pending: true,
			Remedy:  "not implemented yet - Proxy.Available is a skeleton",
		},
	)

	return out
}
