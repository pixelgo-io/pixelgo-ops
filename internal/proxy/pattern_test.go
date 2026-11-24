package proxy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The pattern generator is the whole of what makes the allowlist a whitelist
// rather than the appearance of one, so these cases are the specification.
func TestDomainPattern(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"github.com", `^([a-z0-9-]+\.)*github\.com$`},
		{"api.anthropic.com", `^([a-z0-9-]+\.)*api\.anthropic\.com$`},

		// Written the way people write "and its subdomains". Both forms mean
		// what the pattern already does, so they are accepted and dropped
		// rather than refused for being redundant.
		{"*.github.com", `^([a-z0-9-]+\.)*github\.com$`},
		{".github.com", `^([a-z0-9-]+\.)*github\.com$`},

		// Case is normalised, because domain names are case-insensitive and a
		// pattern that is not would refuse GitHub.com.
		{"GitHub.COM", `^([a-z0-9-]+\.)*github\.com$`},
		{"  github.com  ", `^([a-z0-9-]+\.)*github\.com$`},
	}

	for _, c := range cases {
		got, err := DomainPattern(c.in)
		if err != nil {
			t.Errorf("%q: unexpected error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("%q:\n  got  %s\n  want %s", c.in, got, c.want)
		}
	}
}

/*
 * An allowlist entry becomes a regular expression, which makes it input that
 * can change what the filter MEANS rather than only what it contains. These
 * are refused rather than escaped: escaping ".*" yields a pattern for the
 * literal domain ".*", which matches nothing - safe, but it leaves a person
 * with traffic refused for a reason recorded nowhere.
 */
func TestDomainPatternRefusesAnythingThatCouldBeAPattern(t *testing.T) {
	bad := []string{
		".*",            // would allow everything if it reached the filter
		"*",             //
		"github.*",      //
		"^github\\.com", // already a pattern; double anchoring would be wrong
		"(github|evil).com",
		"github.com|evil.net",
		"github.com$",
		"githu?b.com",
		"github.com\nevil.net", // a second entry smuggled into one line
		"github com",           // a space
		"github..com",          // an empty label
		"github.com.",          // trailing dot
		"localhost",            // no dot: a whole top-level domain's worth
		"",
		"   ",
	}

	for _, b := range bad {
		if got, err := DomainPattern(b); err == nil {
			t.Errorf("%q was accepted and became %s - it should have been refused",
				b, got)
		}
	}
}

// One unusable entry fails the whole filter rather than being skipped. With
// FilterDefaultDeny, skipping it silently narrows what is permitted, and the
// person sees unrelated traffic refused with nothing to connect it to.
func TestBuildFilterRefusesTheWholeListOnOneBadEntry(t *testing.T) {
	_, err := buildFilter([]string{"github.com", ".*", "api.anthropic.com"})
	if err == nil {
		t.Fatal("a bad entry in the middle was tolerated")
	}
	// The message has to point at the line, or finding it in a long list is a
	// search rather than a fix.
	if !strings.Contains(err.Error(), "entry 2") {
		t.Errorf("the error does not say which entry: %v", err)
	}
}

func TestReadAllowlistSkipsCommentsAndBlanks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "allowlist")
	content := "# what the agent may reach\n" +
		"\n" +
		"github.com\n" +
		"   \n" +
		"  api.anthropic.com  \n" +
		"# nothing below this line\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := ReadAllowlist(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"github.com", "api.anthropic.com"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if strings.TrimSpace(got[i]) != want[i] {
			t.Errorf("entry %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

// A missing allowlist is the state init creates, and "nothing is allowed yet"
// is a legitimate answer rather than a fault.
func TestReadAllowlistMissingFileIsEmptyNotAnError(t *testing.T) {
	got, err := ReadAllowlist(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatalf("a missing allowlist should not be an error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v", got)
	}
}

// An interrupted write must not leave a half-written allowlist: with
// FilterDefaultDeny that is a proxy refusing most of what it should permit.
func TestWriteAllowlistIsAtomicAndReadsBack(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "allowlist")

	want := []string{"github.com", "api.anthropic.com"}
	if err := WriteAllowlist(path, want); err != nil {
		t.Fatal(err)
	}

	got, err := ReadAllowlist(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("got %v, want %v", got, want)
	}

	// No temporary files left behind: they are created in the same directory
	// so that the rename cannot cross a filesystem, which means a leak would
	// land next to the real file.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Errorf("a temporary file was left behind: %s", e.Name())
		}
	}
}
