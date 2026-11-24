package proxy

// Turning the allowlist into filter patterns.
//
// This is the part of the proxy that decides what "github.com is allowed"
// actually means, and the first version of it was wrong in a way nobody would
// have noticed. Every claim below was checked against tinyproxy 1.11.1 rather
// than reasoned about, because the failure mode is permissive and silent.
//
// What the allowlist holds is domains, written by a person:
//
//	github.com
//	api.anthropic.com
//
// What tinyproxy's filter file holds is regular expressions, matched against
// the host - and matched ANYWHERE in it unless anchored. Copying the domains
// across unchanged produces this, with one domain on the list:
//
//	github.com                 allowed   (correct)
//	notgithub.com              allowed   (wrong - contains "github.com")
//	evil-github.community      allowed   (wrong - contains it too)
//	github.com.attacker.net    allowed   (wrong - the classic suffix attack)
//
// The wildcard is not even needed. "notgithub.com" contains the literal string
// "github.com", so anyone who registers a domain with yours as a substring is
// on your allowlist. That is what an unanchored pattern costs.
//
// So each domain is turned into an anchored pattern instead:
//
//	github.com  ->  ^([a-z0-9-]+\.)*github\.com$
//
// which allows the domain and its subdomains at any depth, and nothing else.
// Verified: github.com, api.github.com and raw.x.github.com pass; the three
// wrong cases above are refused; GITHUB.COM passes, because tinyproxy matches
// case-insensitively by default and domain names are case-insensitive.

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// The pattern around each domain. Subdomains are included deliberately: a
// person who writes "github.com" on one line and then finds api.github.com
// refused has been given a list that does not mean what it reads.
const (
	patternPrefix = `^([a-z0-9-]+\.)*`
	patternSuffix = `$`
)

/*
 * An allowlist entry becomes a regular expression, which makes it input that
 * can change what the filter means rather than only what it contains. A line
 * reading ".*" would allow everything.
 *
 * So entries are validated and refused, not escaped into submission. Escaping
 * ".*" produces a pattern for the literal domain ".*", which matches nothing -
 * fail-closed, but silently useless, and the person is left with a proxy that
 * refuses traffic they believe they permitted. Refusing it says so instead.
 *
 * The validation is a whitelist of characters, so anything that could act as a
 * metacharacter is rejected by construction rather than by a list of things to
 * remember to escape.
 */
func validDomainChar(c byte) bool {
	return (c >= 'a' && c <= 'z') ||
		(c >= '0' && c <= '9') ||
		c == '-' || c == '.'
}

// DomainPattern turns one allowlist entry into an anchored filter pattern.
//
// A leading "." or "*." is accepted and dropped: both are how people write
// "and its subdomains", which is what the pattern does anyway. Accepting them
// is kinder than refusing a line whose intent is exactly right.
func DomainPattern(entry string) (string, error) {
	d := strings.ToLower(strings.TrimSpace(entry))

	d = strings.TrimPrefix(d, "*.")
	d = strings.TrimPrefix(d, ".")

	if d == "" {
		return "", fmt.Errorf("empty domain")
	}
	if len(d) > 253 {
		return "", fmt.Errorf("domain is longer than 253 characters")
	}
	if !strings.Contains(d, ".") {
		// A bare label would allow a whole top-level domain's worth of hosts
		// through, and is far more likely to be a typo than an intention.
		return "", fmt.Errorf("%q is not a domain name - it has no dot", d)
	}
	if strings.HasPrefix(d, ".") || strings.HasSuffix(d, ".") ||
		strings.Contains(d, "..") {
		return "", fmt.Errorf("%q has an empty label", d)
	}

	for i := 0; i < len(d); i++ {
		if !validDomainChar(d[i]) {
			return "", fmt.Errorf(
				"%q contains %q, which is not allowed in a domain here - "+
					"an allowlist entry becomes a filter pattern, so only "+
					"letters, digits, hyphens and dots are accepted",
				d, string(d[i]))
		}
	}

	// Dots are the only character left that means something to a regular
	// expression, so escaping them is the whole job.
	return patternPrefix + strings.ReplaceAll(d, ".", `\.`) + patternSuffix, nil
}

// ReadAllowlist returns the domains in the allowlist file, in order, skipping
// blank lines and comments.
//
// A missing file is an empty list and not an error: that is the state init
// creates on purpose, and "nothing is allowed yet" is a legitimate answer.
func ReadAllowlist(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var out []string
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out, s.Err()
}

// WriteAllowlist replaces the allowlist with these domains, one per line.
//
// Written through a temporary file and renamed, so an interrupted write cannot
// leave a half-written allowlist - which, with FilterDefaultDeny, is a proxy
// that refuses most of what it should permit.
func WriteAllowlist(path string, domains []string) error {
	var b strings.Builder
	for _, d := range domains {
		b.WriteString(d)
		b.WriteString("\n")
	}
	return writeFileAtomic(path, b.String(), 0o644)
}

// buildFilter turns the allowlist into the contents of the filter file.
//
// One bad entry fails the whole thing rather than being skipped. With
// FilterDefaultDeny, skipping a line silently narrows what is permitted, and
// the person would see unrelated traffic refused with nothing to connect it to.
func buildFilter(domains []string) (string, error) {
	var b strings.Builder
	for i, d := range domains {
		p, err := DomainPattern(d)
		if err != nil {
			return "", fmt.Errorf("allowlist entry %d (%q): %w", i+1, d, err)
		}
		b.WriteString(p)
		b.WriteString("\n")
	}
	return b.String(), nil
}

// writeFileAtomic writes content to path via a temporary file in the same
// directory, then renames it. Same directory so the rename cannot cross a
// filesystem and stop being atomic.
func writeFileAtomic(path, content string, mode os.FileMode) error {
	dir := path[:strings.LastIndex(path, "/")+1]
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name) // a no-op once the rename below has succeeded

	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, mode); err != nil {
		return err
	}
	return os.Rename(name, path)
}
