package proxy

// Mandatory access control, and the one failure it produces here.
//
// The Ubuntu and Debian tinyproxy packages ship /etc/apparmor.d/tinyproxy. The
// profile lets tinyproxy read a configuration under /etc/tinyproxy and nowhere
// else. We generate ours in the ops directory, on purpose - it is derived from
// the allowlist and rewritten on every allow - so the binary starts, the kernel
// refuses it the file, and tinyproxy reports the only thing it can see:
//
//	tinyproxy: Could not open config file "/home/you/.pixelgo-ops/proxy.conf"
//
// About a file that is there, readable, and was written successfully a
// millisecond earlier. Someone reading that message will check the permissions,
// find nothing wrong, and have no next step.
//
// Two things are done about it, and the split matters:
//
//   - Start explains the failure AFTER it happens. It fires only when the start
//     actually failed and the file is actually readable by us, so it cannot
//     raise a false alarm.
//   - check reports that a profile is present, without failing. It cannot tell
//     whether the profile would stop us, so it says what it knows and names the
//     remedy, rather than refusing to run on a machine that may be fine.
//
// The alternative - predicting the failure in Available and refusing to start -
// was rejected. Deciding "this profile would block us" needs the profile's mode,
// and /sys/kernel/security/apparmor/profiles is root-only, while check is meant
// to be safe to run as anyone. A guess there turns into an installation that
// refuses to start for a reason that may not exist.
//
// NOT OBSERVED, and worth knowing: none of this has been run against a live
// AppArmor. The machine it was written on has no AppArmor compiled into the
// kernel, which is exactly why the problem reached CI before it reached anyone
// here. The paths and the override mechanism are from Debian's packaging and
// AppArmor's own documentation; the failure string is from a real tinyproxy.
// Treat the detection as reasoned and the remedy as untested.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Where the system keeps this. Variables rather than constants so the tests can
// point them at a temporary directory - there is no way to install an AppArmor
// profile from a test, and a test that needed one would never run anywhere.
var (
	apparmorSecurityDir = "/sys/kernel/security/apparmor"
	apparmorProfileDir  = "/etc/apparmor.d"
)

// cannotOpenConfig is what tinyproxy prints when the open fails, for any
// reason. Matched as a fragment because the rest of the line is the path.
const cannotOpenConfig = "Could not open config file"

// Confinement is what can be learned about a profile without root.
//
// Deliberately not a yes/no. "A profile exists" and "that profile will stop us"
// are different claims, and only the first one is answerable here.
type Confinement struct {
	// Active reports whether the kernel has AppArmor at all. False on a
	// machine where none of this can apply.
	Active bool

	// ProfilePath is the profile shipped for this proxy, empty when there is
	// none.
	ProfilePath string

	// OverridePath is the local override file, empty when absent. A packaged
	// profile includes /etc/apparmor.d/local/<name> precisely so that a site
	// can extend it without editing the profile, and so that the change
	// survives a package upgrade.
	OverridePath string

	// OverrideMentions is true when the override file already names the
	// directory we would ask about.
	//
	// A heuristic, and named as one: an override could permit our directory
	// through a pattern this does not match, and could mention it in a comment
	// while permitting nothing. It is used only to soften what check prints,
	// never to decide whether to start.
	OverrideMentions bool
}

// Confinement inspects the system for a profile covering this proxy.
//
// It reads files and nothing else - no profile is loaded, parsed or changed.
// "check" must be safe to run twice.
func (p *Proxy) Confinement() Confinement {
	var c Confinement

	if fi, err := os.Stat(apparmorSecurityDir); err == nil && fi.IsDir() {
		c.Active = true
	}
	if !c.Active {
		// Nothing below can matter. A profile file left on a machine with no
		// AppArmor confines nothing, and reporting it would invent a problem.
		return c
	}

	profile := filepath.Join(apparmorProfileDir, p.Kind)
	if _, err := os.Stat(profile); err == nil {
		c.ProfilePath = profile
	}
	if c.ProfilePath == "" {
		return c
	}

	override := filepath.Join(apparmorProfileDir, "local", p.Kind)
	if b, err := os.ReadFile(override); err == nil {
		c.OverridePath = override
		dir := filepath.Dir(p.ConfigPath)
		if dir != "" && strings.Contains(string(b), dir) {
			c.OverrideMentions = true
		}
	}
	return c
}

// Suspect reports whether there is a profile that could stop the proxy from
// reading a configuration outside the profile's own directory.
//
// Could, not will. See the type's comment.
func (c Confinement) Suspect() bool {
	return c.Active && c.ProfilePath != "" && !c.OverrideMentions
}

// Remedy is the two commands that extend the profile for a directory.
//
// The override file rather than the profile itself: a packaged profile includes
// it for this, and editing the profile loses the change on the next package
// upgrade.
//
// pixelgo-ops does not run these. They loosen a security policy, and a tool
// that loosens one without being asked is a tool nobody should install.
func (c Confinement) Remedy(dir string) string {
	if c.ProfilePath == "" {
		return ""
	}
	name := filepath.Base(c.ProfilePath)
	return fmt.Sprintf(
		"if that is apparmor, allow the directory with a local override:\n"+
			"  echo '  owner %s/** r,' | sudo tee -a %s\n"+
			"  sudo apparmor_parser -r %s",
		strings.TrimSuffix(dir, "/"),
		filepath.Join(apparmorProfileDir, "local", name),
		c.ProfilePath)
}

// IndentLines puts prefix in front of every line after the first.
//
// A remedy is several lines of commands, and it is printed at different depths
// by Start and by check. Without this the first line sits at the caller's
// indentation and the rest at the string's own, which reads as if the commands
// belonged to something else.
func IndentLines(s, prefix string) string {
	return strings.ReplaceAll(s, "\n", "\n"+prefix)
}

// explainOpenFailure turns tinyproxy's bare "Could not open config file" into
// something with a next step, and leaves every other failure alone.
//
// The guard is the point. It fires only when the proxy said it could not open
// the file AND we have just read that same file ourselves - so the open failed
// for a reason that is not the file's existence or its permissions, which is
// what a confinement looks like from the outside. Anything else is reported as
// it came.
func (p *Proxy) explainOpenFailure(msg string) string {
	if !strings.Contains(msg, cannotOpenConfig) {
		return msg
	}
	if _, err := os.ReadFile(p.ConfigPath); err != nil {
		// We cannot read it either. Then the message is accurate and the
		// problem is ours, not the kernel's.
		return msg
	}

	c := p.Confinement()
	out := msg + fmt.Sprintf(
		"\n  the file exists and this process can read it, so the open was refused"+
			"\n  by something other than its permissions")
	if c.ProfilePath != "" {
		out += "\n  " + c.ProfilePath + " exists, and such a profile normally permits a" +
			"\n  configuration only under its own directory"
		out += "\n  " + IndentLines(c.Remedy(filepath.Dir(p.ConfigPath)), "  ")
	} else if c.Active {
		out += "\n  apparmor is active on this machine and may be the cause"
	}
	return out
}
