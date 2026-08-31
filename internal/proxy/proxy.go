// Package proxy starts and stops the proxy that runs on the host.
//
// The proxy is mandatory: without it the VM would have direct outbound access,
// and isolation on its own stops no action at all. See README, Networking.
//
// SKELETON.
package proxy

// Proxy is the process that runs on the host and filters the VM's traffic.
type Proxy struct {
	Kind      string // "tinyproxy" or "squid"
	Port      int
	Allowlist string // path to the file of domains, one per line
	LogPath   string // logs/traffic
}

// Available checks whether the binary exists on the system.
func (p *Proxy) Available() error { panic("not implemented") }

// WriteConfig generates the configuration from templates/.
// The user does not edit proxy files by hand.
func (p *Proxy) WriteConfig() error { panic("not implemented") }

// Start starts the process. Called by "up", before the VM starts.
func (p *Proxy) Start() error { panic("not implemented") }

// Stop stops the process. Called by "down".
func (p *Proxy) Stop() error { panic("not implemented") }

// Allow adds a domain to the allowlist and reloads the proxy.
func (p *Proxy) Allow(domain string) error { panic("not implemented") }

// Deny removes a domain.
func (p *Proxy) Deny(domain string) error { panic("not implemented") }

// Allowed returns the domains allowed right now.
func (p *Proxy) Allowed() ([]string, error) { panic("not implemented") }

// Firewall applies the rules that block any outbound path other than the proxy.
//
// Without them, the agent could ignore the proxy and talk directly over IP.
//
// TODO: nftables on Linux, New-NetFirewallRule on Windows.
func Firewall(vmNetwork string, proxyPort int) error { panic("not implemented") }
