// Package config reads and validates the configuration.
//
// The format is not decided yet - see README, Open questions. For now a flat
// KEY=value file, because it stays easy to read with grep and easy to use from
// a shell. If that proves insufficient, it moves to YAML.
//
// SKELETON.
package config

// Config is what the tool reads from the configuration file.
type Config struct {
	// VM
	VMName   string // default: pixelgo-vm
	MemoryMB int    // default: 4096
	CPUs     int    // default: 2
	DiskGB   int    // default: 40
	Image    string // the Debian base image

	// Where the structure lives on the host.
	// Linux:   /srv/pixelgo-ops
	// Windows: C:\pixelgo-ops
	OpsDir string

	// Where the agent's code comes from. Two modes, see README.
	//   "clone" - pulled from git at startup
	//   "mount" - a local directory mounted read-write
	WorkspaceMode string
	WorkspaceRepo string // for clone
	WorkspacePath string // for mount

	// Network
	ProxyPort int    // default: 8888
	ProxyKind string // "tinyproxy" or "squid"
}

// Load reads the configuration from OpsDir.
func Load(opsDir string) (*Config, error) { panic("not implemented") }

// Default returns the configuration written by "pixelgo-ops init".
func Default() *Config { panic("not implemented") }

// Validate checks the values before "up".
//
// TODO: refuse if WorkspaceMode is neither "clone" nor "mount"; if memory is
// below a sensible threshold; if OpsDir does not exist.
func (c *Config) Validate() error { panic("not implemented") }
