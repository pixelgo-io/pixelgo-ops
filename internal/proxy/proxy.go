// Package proxy starts and stops the proxy that runs on the host.
//
// The proxy is mandatory: without it the VM would have direct outbound access,
// and isolation on its own stops no action at all. See README, Networking.
//
// What makes it a whitelist rather than the appearance of one is in pattern.go.
// This file is the process around it.
package proxy

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"text/template"
	"time"

	"github.com/pixelgo-io/pixelgo-ops/templates"
)

// KindTinyproxy and KindSquid are the values ProxyKind may take.
const (
	KindTinyproxy = "tinyproxy"
	KindSquid     = "squid"
)

// DefaultVMNetwork is libvirt's default network. It lives here rather than in
// the configuration because nothing creates a network yet; it moves into
// config when the hypervisor work decides what the VM is attached to.
const DefaultVMNetwork = "192.168.122.0/24"

// stopGrace is how long Stop waits for the proxy to exit before killing it. A
// proxy has no work to finish - there is nothing to drain - so this is short.
const stopGrace = 3 * time.Second

// Proxy is the process that runs on the host and filters the VM's traffic.
type Proxy struct {
	Kind      string // "tinyproxy" or "squid"
	Port      int
	Allowlist string // path to the file of domains, one per line
	LogPath   string // audit/traffic

	// Generated, and derived from Allowlist's directory. They sit in the ops
	// directory root, which is not mounted into the VM - only the named
	// subdirectories are - so the agent cannot read the filter that governs
	// it, and a person can.
	ConfigPath string
	FilterPath string
	PidPath    string

	ListenAddr string // the host address the VM reaches; default 0.0.0.0
	VMNetwork  string // which network may use the proxy at all
}

// New returns a Proxy configured from the ops directory.
func New(opsDir, kind string, port int, logPath string) *Proxy {
	return &Proxy{
		Kind:       kind,
		Port:       port,
		Allowlist:  filepath.Join(opsDir, "allowlist"),
		LogPath:    logPath,
		ConfigPath: filepath.Join(opsDir, "proxy.conf"),
		FilterPath: filepath.Join(opsDir, "proxy.filter"),
		PidPath:    filepath.Join(opsDir, "proxy.pid"),
		ListenAddr: "0.0.0.0",
		VMNetwork:  DefaultVMNetwork,
	}
}

// ErrUnsupportedKind is returned for a proxy this package cannot drive.
var ErrUnsupportedKind = errors.New("unsupported proxy")

// ErrNotRunning is returned when there is no proxy to act on.
var ErrNotRunning = errors.New("the proxy is not running")

func (p *Proxy) checkKind() error {
	if p.Kind != KindTinyproxy {
		return fmt.Errorf("%w: %q - only %s is implemented so far",
			ErrUnsupportedKind, p.Kind, KindTinyproxy)
	}
	return nil
}

// Available checks whether the binary exists on the system.
func (p *Proxy) Available() error {
	if err := p.checkKind(); err != nil {
		return err
	}
	if _, err := exec.LookPath(p.Kind); err != nil {
		// The remedy belongs in the message: the person running this is
		// usually installing pixelgo-ops for the first time and the useful
		// answer is the command, not the fact.
		return fmt.Errorf("%s is not installed - install it with: "+
			"sudo apt install %s   (or the equivalent for your system)",
			p.Kind, p.Kind)
	}
	return nil
}

// WriteConfig generates the configuration from templates/.
// The user does not edit proxy files by hand.
//
// Both files are generated: the proxy configuration, and the filter file that
// the allowlist is turned into. The allowlist itself is the only one a person
// edits.
func (p *Proxy) WriteConfig() error {
	if err := p.checkKind(); err != nil {
		return err
	}

	domains, err := ReadAllowlist(p.Allowlist)
	if err != nil {
		return fmt.Errorf("reading %s: %w", p.Allowlist, err)
	}

	filter, err := buildFilter(domains)
	if err != nil {
		return err
	}

	raw, err := templates.FS.ReadFile("tinyproxy.conf")
	if err != nil {
		return fmt.Errorf("reading the embedded template: %w", err)
	}

	// Missingkey=error so that a field renamed here and not there fails at
	// generation rather than producing a configuration with the word "<no
	// value>" where a path should be.
	t, err := template.New("tinyproxy.conf").Option("missingkey=error").Parse(string(raw))
	if err != nil {
		return fmt.Errorf("parsing the template: %w", err)
	}

	var b strings.Builder
	if err := t.Execute(&b, map[string]any{
		"Port":       p.Port,
		"ListenAddr": p.ListenAddr,
		"VMNetwork":  p.VMNetwork,
		"FilterFile": p.FilterPath,
		"LogPath":    p.LogPath,
		"PidFile":    p.PidPath,
	}); err != nil {
		return fmt.Errorf("generating the configuration: %w", err)
	}

	// The filter first. If the configuration is written and the filter is not,
	// a proxy started in between has a Filter line pointing at nothing - and
	// tinyproxy with FilterDefaultDeny and no filter file refuses everything,
	// which is safe but looks like a different fault.
	if err := writeFileAtomic(p.FilterPath, filter, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", p.FilterPath, err)
	}
	if err := writeFileAtomic(p.ConfigPath, b.String(), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", p.ConfigPath, err)
	}
	return nil
}

// Start starts the process. Called by "up", before the VM starts.
//
// tinyproxy daemonises itself and writes the PID file named in the generated
// configuration, so ops does not hold the child: `up` returns and the proxy
// keeps running, which is what is wanted. Stop finds it through that file.
func (p *Proxy) Start() error {
	if err := p.Available(); err != nil {
		return err
	}

	if pid, ok := p.runningPid(); ok {
		return fmt.Errorf("the proxy is already running (pid %d)", pid)
	}

	// Something else on the port is reported, never killed: it may be another
	// pixelgo-ops, or it may be unrelated and someone else's.
	if err := p.portFree(); err != nil {
		return err
	}

	if err := p.WriteConfig(); err != nil {
		return err
	}

	out, err := exec.Command(p.Kind, "-c", p.ConfigPath).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("%s did not start: %s", p.Kind, msg)
	}

	// It daemonises, so a successful exit does not mean it is listening yet.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := p.runningPid(); ok {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("%s exited without writing %s - check %s",
		p.Kind, p.PidPath, p.LogPath)
}

// Stop stops the process. Called by "down".
func (p *Proxy) Stop() error {
	pid, ok := p.runningPid()
	if !ok {
		return ErrNotRunning
	}

	if err := signalPid(pid, syscall.SIGTERM); err != nil {
		return fmt.Errorf("signalling the proxy (pid %d): %w", pid, err)
	}

	deadline := time.Now().Add(stopGrace)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			os.Remove(p.PidPath)
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}

	_ = signalPid(pid, syscall.SIGKILL)
	os.Remove(p.PidPath)
	return nil
}

// Allow adds a domain to the allowlist and reloads the proxy.
func (p *Proxy) Allow(domain string) error {
	// Validated before it is written, not after: an allowlist that holds an
	// entry the generator will reject is one where the next Start fails for a
	// reason recorded in a file nobody is looking at.
	if _, err := DomainPattern(domain); err != nil {
		return err
	}

	domains, err := ReadAllowlist(p.Allowlist)
	if err != nil {
		return err
	}
	want := strings.ToLower(strings.TrimSpace(domain))
	for _, d := range domains {
		if strings.ToLower(strings.TrimSpace(d)) == want {
			return nil // already there; adding it twice is not an error
		}
	}

	if err := WriteAllowlist(p.Allowlist, append(domains, want)); err != nil {
		return err
	}
	return p.reload()
}

// Deny removes a domain.
func (p *Proxy) Deny(domain string) error {
	domains, err := ReadAllowlist(p.Allowlist)
	if err != nil {
		return err
	}

	want := strings.ToLower(strings.TrimSpace(domain))
	var kept []string
	found := false
	for _, d := range domains {
		if strings.ToLower(strings.TrimSpace(d)) == want {
			found = true
			continue
		}
		kept = append(kept, d)
	}
	if !found {
		return fmt.Errorf("%q is not on the allowlist", domain)
	}

	if err := WriteAllowlist(p.Allowlist, kept); err != nil {
		return err
	}
	return p.reload()
}

// Allowed returns the domains allowed right now.
//
// Read from the allowlist, which is the file a person edits. Note that the
// proxy enforces the generated filter, so the two can differ if the allowlist
// is edited by hand without a reload - Allow and Deny always regenerate, so
// they cannot cause that drift themselves.
func (p *Proxy) Allowed() ([]string, error) {
	return ReadAllowlist(p.Allowlist)
}

// reload regenerates the filter and asks a running proxy to re-read it.
//
// SIGHUP rather than a restart, on purpose. A restart drops the connections
// the agent currently holds open, which reaches it as a network fault rather
// than as a policy change - and an agent that concludes the network is
// unreliable behaves differently from one told that a domain is not permitted.
//
// ASKS. The signal is a request, not a guarantee: tinyproxy re-reads the filter
// when it gets to it, so there is a window after Allow returns in which the old
// filter is still in force. Nothing can close that window from out here -
// there is no acknowledgement to wait for - so anything that needs to observe
// the new rule in force has to retry rather than assume, and the command line
// says "asked to reload" rather than "reloaded".
//
// A proxy that is not running is not an error here: changing the allowlist
// before starting anything is a normal thing to do.
func (p *Proxy) reload() error {
	if err := p.WriteConfig(); err != nil {
		return err
	}
	pid, ok := p.runningPid()
	if !ok {
		return nil
	}
	if err := signalPid(pid, syscall.SIGHUP); err != nil {
		return fmt.Errorf("reloading the proxy (pid %d): %w", pid, err)
	}
	return nil
}

// Running reports the proxy's pid, and whether it is running at all.
func (p *Proxy) Running() (int, bool) { return p.runningPid() }

// runningPid reads the PID file and reports whether that process exists.
//
// A PID file left by a crash names a process that is gone, and is
// indistinguishable from a live one by looking at it - so it is checked rather
// than trusted, the same way the engine socket is.
func (p *Proxy) runningPid() (int, bool) {
	b, err := os.ReadFile(p.PidPath)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 1 {
		return 0, false
	}
	if !processAlive(pid) {
		return 0, false
	}
	return pid, true
}

// portFree reports an error if something is already listening where the proxy
// would.
func (p *Proxy) portFree() error {
	addr := net.JoinHostPort(p.ListenAddr, strconv.Itoa(p.Port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("port %d is already in use by something else - "+
			"pixelgo-ops will not take it: %v", p.Port, err)
	}
	return ln.Close()
}

// Firewall applies the rules that block any outbound path other than the proxy.
//
// Without them, the agent could ignore the proxy and talk directly over IP.
//
// TODO: nftables on Linux, New-NetFirewallRule on Windows.
func Firewall(vmNetwork string, proxyPort int) error { panic("not implemented") }
