package proxy

// The rules that make the proxy the only way out.
//
// Without them the proxy is decoration: the agent does not have to defeat the
// allowlist, it can open a socket. And this is the kind of thing that fails
// permissive and silent - everything works, the traffic log fills up with the
// requests that happened to go through the proxy, and nothing anywhere reports
// the ones that did not.
//
// So every property below was demonstrated against a real nftables, with a
// network namespace standing in for the VM, rather than reasoned about. What
// was verified, and what was not, is marked.
//
// Verified: a request from the VM to the proxy port succeeds; to any other
// port on the host it is dropped; UDP to port 53 is dropped; applying the
// rules three times leaves the same three rules; deleting our table restores
// access and leaves another table untouched.
//
// NOT verified, and reasoned only: the IPv6 case. `ip saddr` is an IPv4 match,
// so a packet with an IPv6 header does not match it and passes under the
// chain's accept policy - which would be a hole. `iifname` matches regardless
// of family and closes it. Both of those are documented nftables behaviour,
// but the container this was developed in has no IPv6 at all (socket(AF_INET6)
// fails outright), so neither the hole nor the fix was observed here. The test
// covers it and skips where IPv6 is unavailable.

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
)

// FirewallTable is the nftables table pixelgo-ops owns.
//
// A table of our own, with accept policies on its chains, so that nothing here
// changes the default for anyone else's traffic: only what these rules match is
// dropped, and `nft delete table` removes exactly ours.
const FirewallTable = "pixelgo-ops"

// maxIfaceName is IFNAMSIZ - 1: the kernel's limit on an interface name.
const maxIfaceName = 15

// Firewall is the rule set that confines the VM's network to the proxy.
type Firewall struct {
	// Network is the VM's CIDR. Required.
	Network string

	// Interface is the bridge the VM is attached to - "virbr0" and the like.
	//
	// Optional, and worth setting as soon as the hypervisor work knows it:
	// rules matched on the interface cover IPv6 as well as IPv4, while rules
	// matched on an IPv4 network do not match IPv6 packets at all. Until then
	// the IPv4 rules are what there is, and the gap is stated rather than
	// quietly left.
	Interface string

	// ProxyPort is the one port the VM may reach.
	ProxyPort int

	// Table is the nftables table to own. Empty means FirewallTable; a test
	// sets it so a run cannot disturb a real one.
	Table string
}

// NewFirewall returns the rules for a VM network and a proxy port.
func NewFirewall(network string, proxyPort int) *Firewall {
	return &Firewall{Network: network, ProxyPort: proxyPort, Table: FirewallTable}
}

// ErrNoNftables is returned when the tool is missing.
var ErrNoNftables = errors.New("nft is not installed")

func (f *Firewall) table() string {
	if f.Table == "" {
		return FirewallTable
	}
	return f.Table
}

// validate checks the inputs, and is the only reason they are safe to put in a
// generated script.
//
// The network and the interface both end up inside an nft ruleset, which makes
// them input that can add rules rather than only fill them in: a network
// reading "10.0.0.0/8; drop" or carrying a newline would be two statements. So
// the network is parsed and RE-SERIALISED from the parsed form - the string
// that reaches the script is produced by net, never by the caller - and the
// interface is checked against a character whitelist.
func (f *Firewall) validate() (network string, err error) {
	if f.ProxyPort < 1 || f.ProxyPort > 65535 {
		return "", fmt.Errorf("proxy port %d is not a port", f.ProxyPort)
	}

	_, ipnet, err := net.ParseCIDR(strings.TrimSpace(f.Network))
	if err != nil {
		return "", fmt.Errorf("%q is not a network in CIDR form: %w", f.Network, err)
	}
	if ipnet.IP.To4() == nil {
		// An IPv6 VM network would need ip6 saddr rules, which nothing asks
		// for yet. Refusing is better than generating rules that match nothing.
		return "", fmt.Errorf("%q is an IPv6 network, which is not supported yet",
			f.Network)
	}

	if f.Interface != "" {
		if len(f.Interface) > maxIfaceName {
			return "", fmt.Errorf("interface name %q is longer than %d characters",
				f.Interface, maxIfaceName)
		}
		for i := 0; i < len(f.Interface); i++ {
			c := f.Interface[i]
			ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
				(c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.'
			if !ok {
				return "", fmt.Errorf(
					"interface name %q contains %q - only letters, digits, "+
						"hyphens, underscores and dots are accepted, because "+
						"this name goes into a generated rule set",
					f.Interface, string(c))
			}
		}
	}

	return ipnet.String(), nil
}

// Ruleset returns the nftables script these rules amount to.
//
// Exposed so it can be read, tested and pasted into `nft -c -f -` by hand.
// A firewall nobody can inspect is one people turn off.
func (f *Firewall) Ruleset() (string, error) {
	network, err := f.validate()
	if err != nil {
		return "", err
	}

	// What each rule matches on. The interface form covers every address
	// family; the network form is IPv4 only.
	match := fmt.Sprintf("ip saddr %s", network)
	coverage := "IPv4 only - set Interface to cover IPv6 as well"
	if f.Interface != "" {
		match = fmt.Sprintf("iifname %q", f.Interface)
		coverage = "all address families, because the match is on the interface"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# Generated by pixelgo-ops. Do not edit.\n")
	fmt.Fprintf(&b, "#\n")
	fmt.Fprintf(&b, "# The VM may reach exactly one thing: the proxy, on port %d.\n", f.ProxyPort)
	fmt.Fprintf(&b, "# Everything else from %s is dropped, including DNS - the proxy\n", network)
	fmt.Fprintf(&b, "# resolves names itself for CONNECT, so an open port 53 would be an\n")
	fmt.Fprintf(&b, "# exfiltration channel and a way to learn what is reachable.\n")
	fmt.Fprintf(&b, "#\n")
	fmt.Fprintf(&b, "# Coverage: %s.\n", coverage)
	fmt.Fprintf(&b, "\n")

	// Create-then-delete, so the delete cannot fail when the table is absent.
	// This is what makes applying twice a no-op rather than stacking
	// duplicates - verified by applying three times and counting the rules.
	fmt.Fprintf(&b, "table inet %s\n", f.table())
	fmt.Fprintf(&b, "delete table inet %s\n", f.table())
	fmt.Fprintf(&b, "\n")

	fmt.Fprintf(&b, "table inet %s {\n", f.table())

	// policy accept, not drop: these chains sit at the same hooks as everyone
	// else's, and a drop policy here would decide the fate of unrelated
	// traffic. A drop in any chain is terminal, so matching what we mean to
	// block is enough.
	fmt.Fprintf(&b, "\tchain input {\n")
	fmt.Fprintf(&b, "\t\ttype filter hook input priority filter; policy accept;\n")
	fmt.Fprintf(&b, "\t\t%s tcp dport %d accept\n", match, f.ProxyPort)
	fmt.Fprintf(&b, "\t\t%s drop\n", match)
	fmt.Fprintf(&b, "\t}\n")

	// The VM must not route anywhere through the host either, which is a
	// different hook and the one an address-only input rule would miss.
	fmt.Fprintf(&b, "\tchain forward {\n")
	fmt.Fprintf(&b, "\t\ttype filter hook forward priority filter; policy accept;\n")
	fmt.Fprintf(&b, "\t\t%s drop\n", match)
	fmt.Fprintf(&b, "\t}\n")

	fmt.Fprintf(&b, "}\n")
	return b.String(), nil
}

// Available reports whether these rules can be applied at all.
func (f *Firewall) Available() error {
	if _, err := exec.LookPath("nft"); err != nil {
		return fmt.Errorf("%w - install it with: sudo apt install nftables   "+
			"(or the equivalent for your system)", ErrNoNftables)
	}
	if os.Geteuid() != 0 {
		// Said plainly rather than discovered from nft's own error, which
		// talks about permissions on a netlink socket.
		return fmt.Errorf("applying firewall rules needs root")
	}
	return nil
}

// Apply loads the rules, then reads them back.
//
// The read-back is the point. A rule set that was generated but not loaded
// looks exactly like one that works, right up until it matters, and that is
// the failure this whole file exists to prevent - so Apply does not report
// success on the strength of an exit code.
func (f *Firewall) Apply() error {
	if err := f.Available(); err != nil {
		return err
	}

	rules, err := f.Ruleset()
	if err != nil {
		return err
	}

	if err := nftRun(rules, "-c", "-f", "-"); err != nil {
		return fmt.Errorf("the generated rule set is not valid: %w", err)
	}
	if err := nftRun(rules, "-f", "-"); err != nil {
		return fmt.Errorf("loading the rule set: %w", err)
	}
	return f.Verify()
}

// Remove deletes our table. Called by "down".
//
// Deleting a table that is not there is not an error: "down" may run twice, and
// the desired state afterwards is the same either way.
func (f *Firewall) Remove() error {
	if err := f.Available(); err != nil {
		return err
	}

	applied, err := f.Applied()
	if err != nil {
		return err
	}
	if !applied {
		return nil
	}
	return nftRun("", "delete", "table", "inet", f.table())
}

// Applied reports whether our table is loaded.
func (f *Firewall) Applied() (bool, error) {
	if _, err := exec.LookPath("nft"); err != nil {
		return false, fmt.Errorf("%w", ErrNoNftables)
	}
	cmd := exec.Command("nft", "list", "table", "inet", f.table())
	if err := cmd.Run(); err != nil {
		// nft exits non-zero for a table that does not exist, which is the
		// answer rather than a failure.
		return false, nil
	}
	return true, nil
}

// Verify reads the loaded rules back and checks they say what was generated.
func (f *Firewall) Verify() error {
	network, err := f.validate()
	if err != nil {
		return err
	}

	out, err := exec.Command("nft", "list", "table", "inet", f.table()).CombinedOutput()
	if err != nil {
		return fmt.Errorf("the rules are not loaded: %s",
			strings.TrimSpace(string(out)))
	}
	loaded := string(out)

	match := fmt.Sprintf("ip saddr %s", network)
	if f.Interface != "" {
		match = fmt.Sprintf("iifname %q", f.Interface)
	}

	want := []string{
		fmt.Sprintf("%s tcp dport %d accept", match, f.ProxyPort),
		fmt.Sprintf("%s drop", match),
	}
	for _, w := range want {
		if !strings.Contains(loaded, w) {
			return fmt.Errorf("the loaded rules do not contain %q - "+
				"they were accepted and are not doing what was asked", w)
		}
	}

	// Both hooks, because an input rule alone leaves the VM able to route out
	// through the host.
	for _, hook := range []string{"hook input", "hook forward"} {
		if !strings.Contains(loaded, hook) {
			return fmt.Errorf("the loaded rules have no %s chain", hook)
		}
	}
	return nil
}

// nftRun runs nft, feeding stdin when there is any, and turns its complaint
// into the error rather than hiding it behind an exit status.
func nftRun(stdin string, args ...string) error {
	cmd := exec.Command("nft", args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return errors.New(msg)
	}
	return nil
}
