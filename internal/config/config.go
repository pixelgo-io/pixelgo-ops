// Package config reads and validates the configuration.
//
// The format is not decided yet - see README, Open questions. For now a flat
// KEY=value file, because it stays easy to read with grep and easy to use from
// a shell. If that proves insufficient, it moves to YAML.
package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// FileName is the configuration file inside OpsDir.
const FileName = "config"

// Directory names under OpsDir. Kept here rather than in ops/ so that config
// can name the audit directory without importing it.
const (
	DirRules    = "rules"
	DirPublic   = "public"
	DirApproved = "approved"
	DirRejected = "rejected"
	DirPending  = "pending"
	DirLogs     = "logs"
	DirAudit    = "audit"

	FileAllowlist = "allowlist"
)

// Config is what the tool reads from the configuration file.
type Config struct {
	// VM
	VMName   string // default: pixelgo-vm
	MemoryMB int    // default: 4096
	CPUs     int    // default: 2
	DiskGB   int    // default: 40
	Image    string // the Debian base image

	// Where the structure lives on the host.
	//   Linux:   /srv/pixelgo-ops
	//   Windows: C:\pixelgo-ops
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

// DefaultOpsDir is where the structure lives when nothing says otherwise.
func DefaultOpsDir() string {
	if runtime.GOOS == "windows" {
		return `C:\pixelgo-ops`
	}
	return "/srv/pixelgo-ops"
}

// Default returns the configuration written by "pixelgo-ops init".
func Default() *Config {
	return &Config{
		VMName:   "pixelgo-vm",
		MemoryMB: 4096,
		CPUs:     2,
		DiskGB:   40,
		Image:    "",

		OpsDir: DefaultOpsDir(),

		WorkspaceMode: "clone",
		WorkspaceRepo: "https://github.com/pixelgo-io/pixelgo.git",

		ProxyPort: 8888,
		ProxyKind: "tinyproxy",
	}
}

// Path returns the location of the configuration file inside opsDir.
func Path(opsDir string) string { return filepath.Join(opsDir, FileName) }

// AuditDir is where records the agent must not be able to rewrite live: the
// proxy's traffic log now, the budget log if the gateway is ever built.
//
// Deliberately NOT under logs/, which is mounted read-write into the VM. An
// audit log the agent can truncate is not evidence of anything - the README's
// own rule, that a rule it can delete is not a rule, applies to the records too.
func (c *Config) AuditDir() string { return filepath.Join(c.OpsDir, DirAudit) }

// AllowlistPath is the file of permitted domains. Never mounted in the VM: the
// agent does not need to know what is permitted, it finds out by trying.
func (c *Config) AllowlistPath() string { return filepath.Join(c.OpsDir, FileAllowlist) }

// TrafficLogPath is the proxy's log, under AuditDir for the reason above.
func (c *Config) TrafficLogPath() string { return filepath.Join(c.AuditDir(), "traffic") }

// Save writes the configuration to opsDir, in the flat KEY=value form.
func (c *Config) Save(opsDir string) error {
	var b strings.Builder
	b.WriteString("# pixelgo-ops configuration\n")
	b.WriteString("# KEY=value, one per line. Lines starting with # are ignored.\n\n")

	write := func(k, v string) {
		fmt.Fprintf(&b, "%s=%s\n", k, v)
	}
	write("VM_NAME", c.VMName)
	write("MEMORY_MB", strconv.Itoa(c.MemoryMB))
	write("CPUS", strconv.Itoa(c.CPUs))
	write("DISK_GB", strconv.Itoa(c.DiskGB))
	write("IMAGE", c.Image)
	b.WriteString("\n")
	write("WORKSPACE_MODE", c.WorkspaceMode)
	write("WORKSPACE_REPO", c.WorkspaceRepo)
	write("WORKSPACE_PATH", c.WorkspacePath)
	b.WriteString("\n")
	write("PROXY_PORT", strconv.Itoa(c.ProxyPort))
	write("PROXY_KIND", c.ProxyKind)

	return os.WriteFile(Path(opsDir), []byte(b.String()), 0o644)
}

// Load reads the configuration from opsDir.
//
// Values absent from the file keep their default, so an older configuration
// still loads after a new setting is added. An UNKNOWN key is an error rather
// than being ignored: a typo in a setting would otherwise look exactly like the
// default, and the difference would only show up as behaviour nobody asked for.
func Load(opsDir string) (*Config, error) {
	c := Default()
	c.OpsDir = opsDir

	f, err := os.Open(Path(opsDir))
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for line := 1; sc.Scan(); line++ {
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}

		key, value, found := strings.Cut(text, "=")
		if !found {
			return nil, fmt.Errorf("config: line %d: expected KEY=value, got %q", line, text)
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)

		if err := c.set(key, value); err != nil {
			return nil, fmt.Errorf("config: line %d: %w", line, err)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	return c, nil
}

// set applies one KEY=value pair.
func (c *Config) set(key, value string) error {
	num := func(dst *int) error {
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("%s: expected a number, got %q", key, value)
		}
		*dst = n
		return nil
	}

	switch key {
	case "VM_NAME":
		c.VMName = value
	case "MEMORY_MB":
		return num(&c.MemoryMB)
	case "CPUS":
		return num(&c.CPUs)
	case "DISK_GB":
		return num(&c.DiskGB)
	case "IMAGE":
		c.Image = value
	case "WORKSPACE_MODE":
		c.WorkspaceMode = value
	case "WORKSPACE_REPO":
		c.WorkspaceRepo = value
	case "WORKSPACE_PATH":
		c.WorkspacePath = value
	case "PROXY_PORT":
		return num(&c.ProxyPort)
	case "PROXY_KIND":
		c.ProxyKind = value
	default:
		return fmt.Errorf("unknown setting %q", key)
	}
	return nil
}

// Minimums below which the VM is not worth starting. Not tuned - they exist to
// catch a misplaced digit, not to express a recommendation.
const (
	MinMemoryMB = 1024
	MinCPUs     = 1
	MinDiskGB   = 10
)

// Validate checks the values before "up".
//
// Every message names the setting it is about: this runs before anything
// touches a hypervisor, so it is the last place a mistake can be reported in
// terms the person recognises rather than as a libvirt error.
func (c *Config) Validate() error {
	if c.VMName == "" {
		return fmt.Errorf("VM_NAME is empty")
	}
	if c.OpsDir == "" {
		return fmt.Errorf("OpsDir is empty")
	}
	if fi, err := os.Stat(c.OpsDir); err != nil {
		return fmt.Errorf("OpsDir %q: %w (run \"pixelgo-ops init\" first)", c.OpsDir, err)
	} else if !fi.IsDir() {
		return fmt.Errorf("OpsDir %q is not a directory", c.OpsDir)
	}

	if c.MemoryMB < MinMemoryMB {
		return fmt.Errorf("MEMORY_MB is %d, below the %d minimum", c.MemoryMB, MinMemoryMB)
	}
	if c.CPUs < MinCPUs {
		return fmt.Errorf("CPUS is %d, below the %d minimum", c.CPUs, MinCPUs)
	}
	if c.DiskGB < MinDiskGB {
		return fmt.Errorf("DISK_GB is %d, below the %d minimum", c.DiskGB, MinDiskGB)
	}

	switch c.WorkspaceMode {
	case "clone":
		if c.WorkspaceRepo == "" {
			return fmt.Errorf("WORKSPACE_MODE is \"clone\" but WORKSPACE_REPO is empty")
		}
	case "mount":
		if c.WorkspacePath == "" {
			return fmt.Errorf("WORKSPACE_MODE is \"mount\" but WORKSPACE_PATH is empty")
		}
		if fi, err := os.Stat(c.WorkspacePath); err != nil {
			return fmt.Errorf("WORKSPACE_PATH %q: %w", c.WorkspacePath, err)
		} else if !fi.IsDir() {
			return fmt.Errorf("WORKSPACE_PATH %q is not a directory", c.WorkspacePath)
		}
	default:
		return fmt.Errorf("WORKSPACE_MODE is %q, expected \"clone\" or \"mount\"", c.WorkspaceMode)
	}

	if c.ProxyPort < 1 || c.ProxyPort > 65535 {
		return fmt.Errorf("PROXY_PORT is %d, outside 1-65535", c.ProxyPort)
	}
	switch c.ProxyKind {
	case "tinyproxy", "squid":
	default:
		return fmt.Errorf("PROXY_KIND is %q, expected \"tinyproxy\" or \"squid\"", c.ProxyKind)
	}

	return nil
}
