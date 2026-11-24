package proxy

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

/*
 * The filter, against the real binary.
 *
 * pattern_test.go asserts what patterns are generated. This asserts what
 * tinyproxy does with them, which is the question that matters: the first
 * version of the template produced patterns that read like a whitelist and
 * behaved like a substring match, and no amount of testing the generator would
 * have shown that.
 *
 * No outbound network, and none wanted - a test that reaches the internet fails
 * for reasons that are not the code's. Every hostname here is under .invalid or
 * .test, which are reserved and never resolve, so the proxy answers at once and
 * the two answers are distinct:
 *
 *	refused by the filter              403
 *	permitted, then failed to resolve  500
 *
 * Verified against tinyproxy 1.11.1. The filter's decision is the only thing
 * that separates those two, which is exactly what is being measured.
 *
 * The hostnames preserve the attack shapes: notgithub.invalid and
 * evil-github.invalidate both contain the literal string "github.invalid", so
 * they are what an unanchored pattern lets through.
 */

// refusedStatus is what tinyproxy returns when its filter refuses a request.
const refusedStatus = http.StatusForbidden

func skipWithoutTinyproxy(t *testing.T) {
	t.Helper()
	if err := (&Proxy{Kind: KindTinyproxy}).Available(); err != nil {
		t.Skipf("tinyproxy is not installed: %v", err)
	}
}

func TestFilterAgainstRealTinyproxy(t *testing.T) {
	skipWithoutTinyproxy(t)

	px := startTestProxy(t, []string{"github.invalid"})

	cases := []struct {
		host  string
		allow bool
		why   string
	}{
		{"github.invalid", true, "the domain on the list"},
		{"api.github.invalid", true, "a subdomain - a list that refused this would not mean what it reads"},
		{"raw.x.github.invalid", true, "a subdomain at depth"},

		// The three an unanchored pattern lets through. The first two need no
		// wildcard at all: they contain the literal string "github.invalid",
		// so anyone registering a domain with yours inside it would be on your
		// allowlist.
		{"notgithub.invalid", false, "contains the allowed domain as a substring"},
		{"evil-github.invalidate", false, "contains it too"},
		{"github.invalid.attacker.test", false, "the suffix attack"},

		{"example.test", false, "not on the list at all"},
	}

	for _, c := range cases {
		allowed, err := filterAllows(px.Port, c.host)
		if err != nil {
			t.Errorf("%s: %v", c.host, err)
			continue
		}
		if allowed != c.allow {
			verb := "refused"
			if allowed {
				verb = "allowed"
			}
			t.Errorf("%s was %s (%s)", c.host, verb, c.why)
		}
	}
}

// Domain names are case-insensitive, so a filter that is not would refuse
// GitHub.com while allowing github.com - and the person who wrote one line
// would have no way to know which spelling mattered.
func TestFilterIsCaseInsensitive(t *testing.T) {
	skipWithoutTinyproxy(t)

	px := startTestProxy(t, []string{"github.invalid"})

	allowed, err := filterAllows(px.Port, "GITHUB.INVALID")
	if err != nil {
		t.Fatal(err)
	}
	if !allowed {
		t.Error("GITHUB.INVALID was refused while github.invalid is allowed")
	}
}

// Allow and Deny must take effect on a running proxy without restarting it. A
// restart drops the connections the agent holds open, which reaches the agent
// as a network fault rather than as a policy change - and an agent that
// concludes the network is unreliable behaves differently from one told that a
// domain is not permitted.
func TestAllowAndDenyReloadRatherThanRestart(t *testing.T) {
	skipWithoutTinyproxy(t)

	px := startTestProxy(t, []string{"github.invalid"})
	pidBefore, _ := px.Running()

	if allowed, _ := filterAllows(px.Port, "example.test"); allowed {
		t.Fatal("example.test was allowed before it was added")
	}

	if err := px.Allow("example.test"); err != nil {
		t.Fatalf("allow: %v", err)
	}
	if !eventually(t, px.Port, "example.test", true) {
		t.Error("example.test was still refused well after allow")
	}

	if err := px.Deny("example.test"); err != nil {
		t.Fatalf("deny: %v", err)
	}
	if !eventually(t, px.Port, "example.test", false) {
		t.Error("example.test was still allowed well after deny")
	}

	pidAfter, running := px.Running()
	if !running {
		t.Fatal("the proxy stopped somewhere in there")
	}
	if pidBefore != pidAfter {
		t.Errorf("the proxy was restarted (pid %d -> %d) instead of reloaded",
			pidBefore, pidAfter)
	}
}

// An empty allowlist must refuse everything rather than permit everything. It
// is the state init creates on purpose, so it is also the state a first run is
// in, and getting it the wrong way round would be the worst possible default.
func TestAnEmptyAllowlistRefusesEverything(t *testing.T) {
	skipWithoutTinyproxy(t)

	px := startTestProxy(t, nil)

	for _, h := range []string{"github.invalid", "example.test"} {
		allowed, err := filterAllows(px.Port, h)
		if err != nil {
			t.Errorf("%s: %v", h, err)
			continue
		}
		if allowed {
			t.Errorf("%s was allowed by an empty allowlist", h)
		}
	}
}

// Start must not take a port something else is using, whether that is another
// pixelgo-ops or an unrelated program.
func TestStartRefusesAPortInUse(t *testing.T) {
	skipWithoutTinyproxy(t)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	px := New(t.TempDir(), KindTinyproxy, ln.Addr().(*net.TCPAddr).Port, "")
	px.ListenAddr = "127.0.0.1"
	px.LogPath = px.PidPath + ".log"
	if err := WriteAllowlist(px.Allowlist, []string{"github.invalid"}); err != nil {
		t.Fatal(err)
	}

	if err := px.Start(); err == nil {
		_ = px.Stop()
		t.Fatal("the proxy started on a port that was already in use")
	} else if !strings.Contains(err.Error(), "already in use") {
		t.Errorf("the error does not explain the problem: %v", err)
	}
}

// Stop on a proxy that is not running is reported as that, not as a success and
// not as a crash: "down" may well run twice.
func TestStopWhenNotRunning(t *testing.T) {
	px := New(t.TempDir(), KindTinyproxy, 1, "")
	if err := px.Stop(); err == nil {
		t.Error("stopping nothing reported success")
	}
}

// What the proxy logs is what "traffic" reads, so the parser is held to a log
// the real binary wrote rather than to a fixture of what it is believed to
// write.
func TestTrafficReadsWhatTheProxyActuallyWrote(t *testing.T) {
	skipWithoutTinyproxy(t)

	px := startTestProxy(t, []string{"github.invalid"})

	_, _ = filterAllows(px.Port, "github.invalid")
	_, _ = filterAllows(px.Port, "example.test")
	time.Sleep(300 * time.Millisecond) // it writes its log as it goes

	all, err := ReadTraffic(px.LogPath, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) == 0 {
		t.Fatal("nothing was parsed out of a log the proxy had just written")
	}

	denied, err := ReadTraffic(px.LogPath, true)
	if err != nil {
		t.Fatal(err)
	}

	foundRefusal := false
	for _, e := range denied {
		if !e.Refused {
			t.Error("--denied returned an entry that was not refused")
		}
		if e.Host == "example.test" {
			foundRefusal = true
		}
		// The host must come out bare. A scheme or a port left on it makes the
		// output impossible to group or compare, which is most of what someone
		// reading a traffic log is trying to do.
		if strings.Contains(e.Host, ":") || strings.Contains(e.Host, "/") {
			t.Errorf("the host was not reduced to a bare name: %q", e.Host)
		}
		if e.Time == "" {
			t.Error("an entry came back with no timestamp")
		}
	}
	if !foundRefusal {
		t.Errorf("the refusal of example.test is not in the parsed log; got %+v", denied)
	}
	if len(denied) >= len(all) {
		t.Errorf("--denied (%d) should be a subset of everything (%d)",
			len(denied), len(all))
	}
}

// The generated configuration must be the one the binary actually accepts.
// tinyproxy rejects a configuration it cannot parse, so a successful start is
// the assertion - but checking it explicitly says what went wrong when a
// directive is renamed upstream.
func TestGeneratedConfigIsAcceptedByTheBinary(t *testing.T) {
	skipWithoutTinyproxy(t)

	px := startTestProxy(t, []string{"github.invalid"})
	if _, running := px.Running(); !running {
		t.Fatal("the proxy is not running after a successful Start")
	}
}

// --- helpers ----------------------------------------------------------

// startTestProxy generates a configuration, starts the real binary on a free
// port bound to loopback, and stops it when the test ends.
func startTestProxy(t *testing.T, allowlist []string) *Proxy {
	t.Helper()
	dir := t.TempDir()

	// A port the kernel just handed out and we released: racy in principle,
	// reliable here, and better than a fixed port that collides with whatever
	// else the machine happens to be running.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	px := New(dir, KindTinyproxy, port, dir+"/traffic")
	px.ListenAddr = "127.0.0.1"
	px.VMNetwork = "127.0.0.1/32"

	if err := WriteAllowlist(px.Allowlist, allowlist); err != nil {
		t.Fatal(err)
	}
	if err := px.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = px.Stop() })
	return px
}

/*
 * eventually waits for the filter to reach a verdict, up to a few seconds.
 *
 * Polling rather than a single check, because a reload is a SIGHUP: the proxy
 * re-reads the filter when it gets to it, and there is no acknowledgement to
 * wait for. A single check here passed by luck of timing and then failed, which
 * is how this was found - and anything else that needs to see a new rule in
 * force has to do the same, so the test does what a caller would.
 */
func eventually(t *testing.T, port int, host string, want bool) bool {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		got, err := filterAllows(port, host)
		if err == nil && got == want {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// filterAllows reports whether the proxy's filter let a request for host
// through.
//
// 403 is the filter refusing. Anything else means it did not: the hostnames
// used here never resolve, so a permitted request comes back 500 from the
// proxy's failed lookup. Those two statuses are the decision and nothing in
// between can produce either.
func filterAllows(port int, host string) (bool, error) {
	c := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			Proxy: func(*http.Request) (*url.URL, error) {
				return url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
			},
		},
	}
	resp, err := c.Get("http://" + host + "/")
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	return resp.StatusCode != refusedStatus, nil
}
