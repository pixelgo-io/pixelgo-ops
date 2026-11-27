package proxy

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// There is no way to install an AppArmor profile from a test, and no way to
// compile one into a kernel that lacks it, so the two directories the detection
// reads are variables and these tests point them at temporary ones.
//
// What that buys and what it does not: it tests the decisions - that a profile
// on a machine without AppArmor is ignored, that an override suppresses the
// warning, that the remedy names the right files - against a filesystem laid
// out the way Debian lays it out. It does not test that an AppArmor kernel
// behaves as described. That part is reasoned from Debian's packaging and has
// not been observed; see the comment at the top of confinement.go.
func withFakeApparmor(t *testing.T, active bool) string {
	t.Helper()
	root := t.TempDir()

	profiles := filepath.Join(root, "apparmor.d")
	if err := os.MkdirAll(filepath.Join(profiles, "local"), 0o755); err != nil {
		t.Fatal(err)
	}

	security := filepath.Join(root, "security", "apparmor")
	if active {
		if err := os.MkdirAll(security, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	oldSec, oldProf := apparmorSecurityDir, apparmorProfileDir
	apparmorSecurityDir, apparmorProfileDir = security, profiles
	t.Cleanup(func() { apparmorSecurityDir, apparmorProfileDir = oldSec, oldProf })

	return profiles
}

func testProxy(t *testing.T) *Proxy {
	t.Helper()
	dir := t.TempDir()
	return New(dir, KindTinyproxy, 8888, filepath.Join(dir, "traffic"))
}

// A profile file on a machine with no AppArmor confines nothing. Reporting it
// would invent a problem, and a diagnostic that invents problems stops being
// read.
func TestAProfileWithoutApparmorIsIgnored(t *testing.T) {
	profiles := withFakeApparmor(t, false)
	writeFile(t, filepath.Join(profiles, KindTinyproxy), "profile tinyproxy {}")

	c := testProxy(t).Confinement()

	if c.Active {
		t.Error("apparmor reported active with no security directory")
	}
	if c.Suspect() {
		t.Error("a profile was treated as suspect on a machine without apparmor")
	}
	if c.ProfilePath != "" {
		t.Errorf("the profile was looked up anyway: %q", c.ProfilePath)
	}
}

// AppArmor present and no profile for this proxy: also nothing to say.
func TestApparmorWithoutAProfileIsIgnored(t *testing.T) {
	withFakeApparmor(t, true)

	c := testProxy(t).Confinement()

	if !c.Active {
		t.Error("apparmor should be reported active")
	}
	if c.Suspect() {
		t.Error("suspect with no profile for this proxy")
	}
}

// Both present: this is the Ubuntu case, and the one that has to be reported.
func TestAProfileUnderApparmorIsSuspect(t *testing.T) {
	profiles := withFakeApparmor(t, true)
	writeFile(t, filepath.Join(profiles, KindTinyproxy), "profile tinyproxy {}")

	p := testProxy(t)
	c := p.Confinement()

	if !c.Suspect() {
		t.Fatal("a profile under apparmor was not reported")
	}
	if c.ProfilePath == "" {
		t.Error("the profile path is empty")
	}

	// The remedy has to name both files, or it is not actionable.
	r := c.Remedy(filepath.Dir(p.ConfigPath))
	for _, want := range []string{
		filepath.Join(profiles, "local", KindTinyproxy), // where to write
		c.ProfilePath,              // what to reload
		"apparmor_parser -r",       // how to reload
		filepath.Dir(p.ConfigPath), // what to allow
	} {
		if !strings.Contains(r, want) {
			t.Errorf("the remedy does not mention %q:\n%s", want, r)
		}
	}

	// And it must not tell anyone to edit the profile itself, which a package
	// upgrade would undo.
	if strings.Contains(r, "tee "+c.ProfilePath) {
		t.Error("the remedy writes into the packaged profile instead of the override")
	}
}

// An override that already names our directory means somebody has done this.
// Warning anyway would train people to ignore the warning.
func TestAnOverrideNamingTheDirectorySuppressesTheWarning(t *testing.T) {
	profiles := withFakeApparmor(t, true)
	writeFile(t, filepath.Join(profiles, KindTinyproxy), "profile tinyproxy {}")

	p := testProxy(t)
	dir := filepath.Dir(p.ConfigPath)
	writeFile(t, filepath.Join(profiles, "local", KindTinyproxy),
		"  owner "+dir+"/** r,\n")

	c := p.Confinement()

	if !c.OverrideMentions {
		t.Fatalf("the override naming %q was not noticed", dir)
	}
	if c.Suspect() {
		t.Error("still suspect despite an override for the directory")
	}
}

// An override for some other directory is not an override for ours.
func TestAnUnrelatedOverrideDoesNotSuppressTheWarning(t *testing.T) {
	profiles := withFakeApparmor(t, true)
	writeFile(t, filepath.Join(profiles, KindTinyproxy), "profile tinyproxy {}")
	writeFile(t, filepath.Join(profiles, "local", KindTinyproxy),
		"  owner /srv/something-else/** r,\n")

	c := testProxy(t).Confinement()

	if c.OverrideMentions {
		t.Error("an unrelated override was taken for ours")
	}
	if !c.Suspect() {
		t.Error("the warning was suppressed by an override that does not cover us")
	}
}

// --- the message Start produces -------------------------------------------

// The case this exists for: the proxy says it cannot open a file that we can
// read. The message must say so and give a next step.
func TestOpenFailureIsExplainedWhenTheFileIsReadable(t *testing.T) {
	profiles := withFakeApparmor(t, true)
	writeFile(t, filepath.Join(profiles, KindTinyproxy), "profile tinyproxy {}")

	p := testProxy(t)
	writeFile(t, p.ConfigPath, "Port 8888\n")

	raw := `tinyproxy: Could not open config file "` + p.ConfigPath + `".`
	got := p.explainOpenFailure(raw)

	if got == raw {
		t.Fatal("the message was passed through unexplained")
	}
	for _, want := range []string{
		"this process can read it",
		filepath.Join(profiles, "local", KindTinyproxy),
		"apparmor_parser -r",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the explanation does not mention %q:\n%s", want, got)
		}
	}
}

// If we cannot read the file either, the proxy's message is accurate and the
// fault is ours. Blaming the kernel there would send someone editing a
// security policy to fix a missing file.
func TestOpenFailureIsNotExplainedWhenTheFileIsMissing(t *testing.T) {
	profiles := withFakeApparmor(t, true)
	writeFile(t, filepath.Join(profiles, KindTinyproxy), "profile tinyproxy {}")

	p := testProxy(t) // nothing written

	raw := `tinyproxy: Could not open config file "` + p.ConfigPath + `".`
	if got := p.explainOpenFailure(raw); got != raw {
		t.Errorf("a genuinely missing file was blamed on confinement:\n%s", got)
	}
}

// Every other failure has to survive untouched. A proxy that refuses to start
// for its own reasons must still say so in its own words.
func TestOtherFailuresArePassedThrough(t *testing.T) {
	withFakeApparmor(t, true)
	p := testProxy(t)
	writeFile(t, p.ConfigPath, "Port 8888\n")

	for _, raw := range []string{
		"tinyproxy: Could not create log file.",
		"tinyproxy: bind: Address already in use",
		"exit status 1",
		"",
	} {
		if got := p.explainOpenFailure(raw); got != raw {
			t.Errorf("%q was rewritten to:\n%s", raw, got)
		}
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// --- against the real binary ------------------------------------------------

// The mechanism, observed rather than reasoned.
//
// AppArmor cannot be installed from a test, but its failure has a shape that
// can be reproduced exactly: a configuration file that this process can read
// and that the proxy cannot. File permissions produce it, and the real
// tinyproxy answers with the identical message it gives under a profile:
//
//	tinyproxy: Could not open config file "/path".
//
// So this test proves the two things that were otherwise taken on trust - that
// cannotOpenConfig matches what the binary actually prints, and that
// explainOpenFailure fires on a refusal that is not about the file itself.
//
// It does not prove AppArmor behaves as described. That remains reasoned from
// Debian's packaging. What it proves is that when a refusal of this shape
// arrives, we recognise it and say something useful.
//
// Needs root: only root can read a mode 000 file, which is what makes the two
// perspectives differ at all.
func TestARealRefusalIsRecognised(t *testing.T) {
	skipWithoutTinyproxy(t)
	if os.Geteuid() != 0 {
		t.Skip("needs root: the point is a file we can read and the proxy cannot")
	}
	if _, err := exec.LookPath("setpriv"); err != nil {
		t.Skip("setpriv is not installed")
	}

	profiles := withFakeApparmor(t, true)
	writeFile(t, filepath.Join(profiles, KindTinyproxy), "profile tinyproxy {}")

	p := testProxy(t)
	writeFile(t, p.ConfigPath, "Port 8888\nFilter \"/dev/null\"\n")

	// The denial has to be about the FILE, not about the path to it.
	//
	// Under sudo, t.TempDir gives a directory owned by root with mode 0700, so
	// an unprivileged proxy cannot even traverse it. That produces the same
	// message, which means the test would pass while measuring something else.
	// Opening the directories and closing the file is what makes the refusal
	// specific.
	for d := filepath.Dir(p.ConfigPath); len(d) > 1; d = filepath.Dir(d) {
		if err := os.Chmod(d, 0o755); err != nil {
			break // above the temporary tree; nothing to open there
		}
	}
	if err := os.Chmod(p.ConfigPath, 0o000); err != nil {
		t.Fatal(err)
	}

	// Prove the setup before trusting the result: the directory is traversable
	// and only the file is closed.
	if out, err := exec.Command("setpriv",
		"--reuid=65534", "--regid=65534", "--clear-groups",
		"test", "-x", filepath.Dir(p.ConfigPath)).CombinedOutput(); err != nil {
		t.Fatalf("the directory is still not traversable by the unprivileged user: %v %s",
			err, out)
	}

	out, err := exec.Command("setpriv",
		"--reuid=65534", "--regid=65534", "--clear-groups",
		KindTinyproxy, "-c", p.ConfigPath).CombinedOutput()
	if err == nil {
		t.Fatal("the proxy started despite being refused the file")
	}
	msg := strings.TrimSpace(string(out))

	// 1. The constant matches the real binary.
	if !strings.Contains(msg, cannotOpenConfig) {
		t.Fatalf("the real message does not contain %q: %q", cannotOpenConfig, msg)
	}

	// 2. We can still read it, so the explanation must fire.
	if _, err := os.ReadFile(p.ConfigPath); err != nil {
		t.Fatalf("this process cannot read the file either: %v", err)
	}
	got := p.explainOpenFailure(msg)
	if got == msg {
		t.Fatal("a refusal of exactly the shape we look for was not explained")
	}
	if !strings.Contains(got, "apparmor_parser -r") {
		t.Errorf("the explanation carries no remedy:\n%s", got)
	}
}
