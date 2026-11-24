package proxy

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

/*
 * The firewall, against real nftables and real packets.
 *
 * Generating a rule set is the easy half. The half that matters is whether the
 * rules, once loaded, actually stop anything - and a rule set that was written
 * but not loaded looks exactly like one that works. So this builds a network
 * namespace standing in for the VM, connects it to the host with a veth pair,
 * and asks from inside it.
 *
 * Needs root and nftables, and skips without them. A skipped test says so; a
 * test that quietly checks nothing does not.
 *
 * The table and the network are test-only values, so a run cannot disturb a
 * real pixelgo-ops or anything else on the machine.
 */

const (
	testTable   = "pixelgo-ops-test"
	testNetwork = "10.99.0.0/24"
	testHostIP  = "10.99.0.1"
	testVMIP    = "10.99.0.2"
	testNS      = "pxfwtest"
	testVeth    = "pxfw0"
)

func needsRoot(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("nft"); err != nil {
		t.Skip("nft is not installed")
	}
	if _, err := exec.LookPath("ip"); err != nil {
		t.Skip("ip is not installed")
	}
	if os.Geteuid() != 0 {
		t.Skip("needs root: it loads nftables rules and creates a network namespace")
	}
}

// --- the generated rule set ------------------------------------------

func TestRulesetIsValidAndCoversBothHooks(t *testing.T) {
	f := NewFirewall("192.168.122.0/24", 8888)

	rules, err := f.Ruleset()
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"ip saddr 192.168.122.0/24 tcp dport 8888 accept",
		"ip saddr 192.168.122.0/24 drop",
		"hook input",
		"hook forward", // an input rule alone leaves the VM routing out
		"policy accept",
	} {
		if !strings.Contains(rules, want) {
			t.Errorf("the rule set is missing %q:\n%s", want, rules)
		}
	}

	// policy drop in our chains would decide the fate of unrelated traffic,
	// since they sit at the same hooks as everyone else's.
	if strings.Contains(rules, "policy drop") {
		t.Error("the chains must not have a drop policy")
	}

	// Everything above is string matching and runs anywhere. Asking nft
	// whether it would accept the set does not: -c is a dry run in the sense
	// that it loads nothing, but it still opens a netlink socket and
	// initialises the cache, so without root it fails with "Operation not
	// permitted" - a failure about the machine rather than about the rules.
	//
	// This was written as though -c needed no privileges, and it passed on a
	// machine where the tests happened to run as root. CI, as the ordinary
	// user, said otherwise. The privileged job runs this test too, so the
	// check still happens - see the -test.run pattern in ci.yml.
	if os.Geteuid() != 0 {
		t.Skip("the rules were checked as strings; asking nft needs root")
	}
	if _, err := exec.LookPath("nft"); err != nil {
		t.Skip("nft is not installed")
	}
	if err := nftRun(rules, "-c", "-f", "-"); err != nil {
		t.Errorf("nft does not accept the generated rule set: %v", err)
	}
}

// Setting the interface changes what the rules match on, which is what makes
// them cover IPv6: an `ip saddr` match does not apply to a packet with an IPv6
// header at all.
func TestInterfaceFormMatchesOnTheInterface(t *testing.T) {
	f := NewFirewall("192.168.122.0/24", 8888)
	f.Interface = "virbr0"

	rules, err := f.Ruleset()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rules, `iifname "virbr0" drop`) {
		t.Errorf("not matching on the interface:\n%s", rules)
	}
	if strings.Contains(rules, "ip saddr") {
		t.Error("the address match should be replaced, not added to")
	}
	// The generated file carries its own caveat, so someone reading it knows
	// which families it covers without reading this package.
	if !strings.Contains(rules, "all address families") {
		t.Error("the rule set does not say what it covers")
	}
}

/*
 * The network and the interface both end up inside a generated nft script,
 * which makes them input that can add rules rather than only fill them in.
 * These are refused rather than escaped, for the same reason allowlist entries
 * are: a rule set that silently means something else is worse than one that
 * fails to generate.
 */
func TestFirewallRefusesInputThatCouldBecomeRules(t *testing.T) {
	bad := []struct{ network, iface string }{
		{"10.0.0.0/8; drop", ""},
		{"10.0.0.0/8\naccept", ""},
		{"not-a-network", ""},
		{"10.0.0.1", ""}, // an address, not a network
		{"", ""},
		{"fd00::/64", ""}, // IPv6 network: refused rather than silently unmatched
		{"10.0.0.0/8", "virbr0; drop"},
		{"10.0.0.0/8", "eth0\naccept"},
		{"10.0.0.0/8", "averyveryverylonginterfacename"},
		{"10.0.0.0/8", `br"0`},
	}

	for _, c := range bad {
		f := NewFirewall(c.network, 8888)
		f.Interface = c.iface
		if rules, err := f.Ruleset(); err == nil {
			t.Errorf("network=%q iface=%q was accepted and generated:\n%s",
				c.network, c.iface, rules)
		}
	}

	for _, port := range []int{0, -1, 70000} {
		if _, err := NewFirewall("10.0.0.0/8", port).Ruleset(); err == nil {
			t.Errorf("port %d was accepted", port)
		}
	}
}

// A network written loosely is normalised through net, so what reaches the
// script is produced by the standard library rather than by the caller.
func TestNetworkIsReserialisedNotCopied(t *testing.T) {
	f := NewFirewall("10.99.0.57/24", 8888) // a host address with a mask
	rules, err := f.Ruleset()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rules, "ip saddr 10.99.0.0/24") {
		t.Errorf("the network was not normalised:\n%s", rules)
	}
	if strings.Contains(rules, "10.99.0.57") {
		t.Error("the caller's spelling reached the rule set")
	}
}

// --- real packets -----------------------------------------------------

func TestFirewallBlocksEverythingButTheProxy(t *testing.T) {
	needsRoot(t)

	// The namespace and the veth pair first: the host address the servers bind
	// to does not exist until the interface does.
	setupVM(t)

	proxyPort, otherPort := freePort(t), freePort(t)
	serveOn(t, testHostIP, proxyPort)
	serveOn(t, testHostIP, otherPort)

	f := &Firewall{Network: testNetwork, ProxyPort: proxyPort, Table: testTable}
	t.Cleanup(func() { _ = f.Remove() })

	// Before: the VM reaches both. Without this the test could pass on a
	// network that never worked.
	if !reachesFromVM(t, proxyPort) || !reachesFromVM(t, otherPort) {
		t.Fatal("the VM cannot reach the host before any rules are applied")
	}

	if err := f.Apply(); err != nil {
		t.Fatalf("apply: %v", err)
	}

	if !reachesFromVM(t, proxyPort) {
		t.Error("the proxy port is not reachable - the rules block what they must permit")
	}
	if reachesFromVM(t, otherPort) {
		t.Error("another port on the host is still reachable - the VM can go around the proxy")
	}

	/*
	 * UDP, which is where DNS lives.
	 *
	 * The issue names port 53 specifically - the proxy resolves for CONNECT
	 * itself, so an open 53 is both an exfiltration channel and a way to find
	 * out what is reachable. It is tested on an arbitrary port instead,
	 * because a resolver on the host usually holds 53 already and a test that
	 * skips is worth less than one that runs.
	 *
	 * That is not a weaker check, and the assertion below is why: the drop
	 * rule carries no protocol qualifier, so whatever it does to one UDP port
	 * it does to all of them. Proving it for one and proving the rule is
	 * unqualified together prove it for 53.
	 */
	if udpReachesFromVM(t, freePort(t)) {
		t.Error("UDP from the VM is still permitted")
	}

	rules, err := f.Ruleset()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rules, fmt.Sprintf("ip saddr %s drop", testNetwork)) {
		t.Errorf("the drop rule is qualified by something, so it does not "+
			"cover every port and protocol:\n%s", rules)
	}
}

// Apply must be idempotent: "up" can run twice, and rules that stack would
// grow a chain without changing what it does, until someone reads it and
// cannot tell what is in force.
func TestApplyTwiceChangesNothing(t *testing.T) {
	needsRoot(t)

	f := &Firewall{Network: testNetwork, ProxyPort: freePort(t), Table: testTable}
	t.Cleanup(func() { _ = f.Remove() })

	if err := f.Apply(); err != nil {
		t.Fatalf("apply: %v", err)
	}
	first := countRules(t, f)

	for i := 0; i < 2; i++ {
		if err := f.Apply(); err != nil {
			t.Fatalf("apply again: %v", err)
		}
	}
	if again := countRules(t, f); again != first {
		t.Errorf("rules stacked: %d after one apply, %d after three", first, again)
	}
}

// Remove has to leave nothing behind, and has to leave everyone else's rules
// alone - that is the whole reason for a table of our own.
func TestRemoveIsScopedAndRestoresAccess(t *testing.T) {
	needsRoot(t)

	setupVM(t)

	port := freePort(t)
	serveOn(t, testHostIP, port)

	// Somebody else's table, which must survive.
	const other = "pixelgo-ops-test-bystander"
	_ = nftRun("", "add", "table", "inet", other)
	t.Cleanup(func() { _ = nftRun("", "delete", "table", "inet", other) })

	f := &Firewall{Network: testNetwork, ProxyPort: freePort(t), Table: testTable}
	if err := f.Apply(); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if reachesFromVM(t, port) {
		t.Fatal("the port was reachable while the rules were applied")
	}

	if err := f.Remove(); err != nil {
		t.Fatalf("remove: %v", err)
	}

	applied, err := f.Applied()
	if err != nil {
		t.Fatal(err)
	}
	if applied {
		t.Error("the table is still there after Remove")
	}
	if !reachesFromVM(t, port) {
		t.Error("access was not restored after Remove")
	}

	out, _ := exec.Command("nft", "list", "tables").CombinedOutput()
	if !strings.Contains(string(out), other) {
		t.Error("Remove deleted somebody else's table")
	}

	// Twice is not an error: "down" may run twice and the desired state is the
	// same either way.
	if err := f.Remove(); err != nil {
		t.Errorf("removing twice reported an error: %v", err)
	}
}

// Verify is what makes Apply mean something. Rules deleted behind its back are
// the state a rule set that was never loaded is in, and it has to notice.
func TestVerifyNoticesRulesThatAreNotThere(t *testing.T) {
	needsRoot(t)

	f := &Firewall{Network: testNetwork, ProxyPort: freePort(t), Table: testTable}
	t.Cleanup(func() { _ = f.Remove() })

	if err := f.Apply(); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if err := f.Verify(); err != nil {
		t.Fatalf("verify right after apply: %v", err)
	}

	_ = nftRun("", "delete", "table", "inet", testTable)
	if err := f.Verify(); err == nil {
		t.Error("Verify reported success with no rules loaded at all")
	}
}

// --- helpers ----------------------------------------------------------

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// setupVM builds a namespace with a veth pair to the host, standing in for the
// VM on its own network.
func setupVM(t *testing.T) {
	t.Helper()

	run := func(args ...string) {
		t.Helper()
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%s: %v: %s", strings.Join(args, " "), err,
				strings.TrimSpace(string(out)))
		}
	}
	quiet := func(args ...string) {
		_ = exec.Command(args[0], args[1:]...).Run()
	}

	// Anything a previous crashed run left behind.
	quiet("ip", "netns", "del", testNS)
	quiet("ip", "link", "del", testVeth)

	run("ip", "netns", "add", testNS)
	run("ip", "link", "add", testVeth, "type", "veth", "peer", "name", testVeth+"p")
	run("ip", "link", "set", testVeth+"p", "netns", testNS)
	run("ip", "addr", "add", testHostIP+"/24", "dev", testVeth)
	run("ip", "link", "set", testVeth, "up")
	run("ip", "netns", "exec", testNS, "ip", "addr", "add", testVMIP+"/24", "dev", testVeth+"p")
	run("ip", "netns", "exec", testNS, "ip", "link", "set", testVeth+"p", "up")
	run("ip", "netns", "exec", testNS, "ip", "link", "set", "lo", "up")

	t.Cleanup(func() {
		quiet("ip", "netns", "del", testNS)
		quiet("ip", "link", "del", testVeth)
	})
}

// serveOn answers on the host, so that a reachable port is distinguishable
// from an unreachable one.
func serveOn(t *testing.T, addr string, port int) {
	t.Helper()

	// Bound after the veth exists, so it is retried briefly: the address only
	// exists once setupVM has run, and the order of helpers in a test should
	// not have to encode that.
	var ln net.Listener
	var err error
	for i := 0; i < 50; i++ {
		ln, err = net.Listen("tcp", fmt.Sprintf("%s:%d", addr, port))
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Skipf("cannot listen on %s:%d: %v", addr, port, err)
	}

	srv := &http.Server{Handler: http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "ok") })}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
}

// reachesFromVM asks from inside the namespace, which is the only place the
// question means anything.
func reachesFromVM(t *testing.T, port int) bool {
	t.Helper()
	out, _ := exec.Command("ip", "netns", "exec", testNS,
		"curl", "-s", "-o", "/dev/null", "-w", "%{http_code}",
		"--max-time", "3", fmt.Sprintf("http://%s:%d/", testHostIP, port)).CombinedOutput()
	return strings.Contains(string(out), "200")
}

// udpReachesFromVM sends one datagram and waits for an echo.
func udpReachesFromVM(t *testing.T, port int) bool {
	t.Helper()

	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP(testHostIP), Port: port})
	if err != nil {
		t.Skipf("cannot listen on UDP %s:%d: %v", testHostIP, port, err)
	}
	defer conn.Close()

	go func() {
		buf := make([]byte, 64)
		for {
			n, from, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			_, _ = conn.WriteToUDP(buf[:n], from)
		}
	}()

	out, _ := exec.Command("ip", "netns", "exec", testNS,
		"timeout", "2", "bash", "-c",
		fmt.Sprintf("exec 3<>/dev/udp/%s/%d && printf ping >&3 && head -c 4 <&3",
			testHostIP, port)).CombinedOutput()
	return strings.Contains(string(out), "ping")
}

func countRules(t *testing.T, f *Firewall) int {
	t.Helper()
	out, err := exec.Command("nft", "list", "table", "inet", f.table()).CombinedOutput()
	if err != nil {
		t.Fatalf("listing the table: %s", strings.TrimSpace(string(out)))
	}
	n := 0
	for _, line := range strings.Split(string(out), "\n") {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "ip saddr") || strings.HasPrefix(l, "iifname") {
			n++
		}
	}
	return n
}
