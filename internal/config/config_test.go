package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultMatchesREADME(t *testing.T) {
	c := Default()
	if c.VMName != "pixelgo-vm" {
		t.Errorf("VMName = %q, want pixelgo-vm", c.VMName)
	}
	if c.MemoryMB != 4096 {
		t.Errorf("MemoryMB = %d, want 4096", c.MemoryMB)
	}
	if c.CPUs != 2 {
		t.Errorf("CPUs = %d, want 2", c.CPUs)
	}
	if c.DiskGB != 40 {
		t.Errorf("DiskGB = %d, want 40", c.DiskGB)
	}
	if c.ProxyPort != 8888 {
		t.Errorf("ProxyPort = %d, want 8888", c.ProxyPort)
	}
	if c.ProxyKind != "tinyproxy" {
		t.Errorf("ProxyKind = %q, want tinyproxy", c.ProxyKind)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()

	want := Default()
	want.OpsDir = dir
	want.VMName = "other-vm"
	want.MemoryMB = 8192
	want.WorkspaceMode = "mount"
	want.WorkspacePath = dir
	want.ProxyKind = "squid"

	if err := want.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got.VMName != want.VMName || got.MemoryMB != want.MemoryMB ||
		got.WorkspaceMode != want.WorkspaceMode || got.WorkspacePath != want.WorkspacePath ||
		got.ProxyKind != want.ProxyKind {
		t.Errorf("round trip lost values:\n got %+v\nwant %+v", got, want)
	}
}

// A setting absent from the file keeps its default, so an older configuration
// still loads after a new setting is added.
func TestLoadMissingKeyKeepsDefault(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "VM_NAME=only-this\n")

	c, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.VMName != "only-this" {
		t.Errorf("VMName = %q", c.VMName)
	}
	if c.MemoryMB != Default().MemoryMB {
		t.Errorf("MemoryMB = %d, want the default %d", c.MemoryMB, Default().MemoryMB)
	}
}

// The point of the acceptance criterion: a typo must not silently behave as the
// default, because then the setting appears to have been ignored for no reason.
func TestLoadUnknownKeyIsAnError(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "MEMROY_MB=8192\n")

	_, err := Load(dir)
	if err == nil {
		t.Fatal("Load accepted an unknown key")
	}
	if !strings.Contains(err.Error(), "MEMROY_MB") {
		t.Errorf("error does not name the offending key: %v", err)
	}
}

func TestLoadRejectsMalformedLine(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "VM_NAME\n")

	if _, err := Load(dir); err == nil {
		t.Fatal("Load accepted a line with no =")
	}
}

func TestLoadRejectsNonNumber(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "CPUS=plenty\n")

	_, err := Load(dir)
	if err == nil {
		t.Fatal("Load accepted a non-numeric CPUS")
	}
	if !strings.Contains(err.Error(), "CPUS") {
		t.Errorf("error does not name the setting: %v", err)
	}
}

func TestLoadIgnoresCommentsAndBlanks(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "# a comment\n\n   \nVM_NAME=fine\n")

	c, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.VMName != "fine" {
		t.Errorf("VMName = %q", c.VMName)
	}
}

func TestValidate(t *testing.T) {
	base := func(t *testing.T) *Config {
		c := Default()
		c.OpsDir = t.TempDir()
		return c
	}

	t.Run("default is valid", func(t *testing.T) {
		if err := base(t).Validate(); err != nil {
			t.Errorf("default configuration rejected: %v", err)
		}
	})

	cases := []struct {
		name   string
		break_ func(*Config)
		wants  string
	}{
		{"memory too low", func(c *Config) { c.MemoryMB = 256 }, "MEMORY_MB"},
		{"no cpus", func(c *Config) { c.CPUs = 0 }, "CPUS"},
		{"disk too small", func(c *Config) { c.DiskGB = 1 }, "DISK_GB"},
		{"empty vm name", func(c *Config) { c.VMName = "" }, "VM_NAME"},
		{"bad workspace mode", func(c *Config) { c.WorkspaceMode = "borrow" }, "WORKSPACE_MODE"},
		{"clone without repo", func(c *Config) { c.WorkspaceRepo = "" }, "WORKSPACE_REPO"},
		{"mount without path", func(c *Config) {
			c.WorkspaceMode = "mount"
			c.WorkspacePath = ""
		}, "WORKSPACE_PATH"},
		{"mount with missing path", func(c *Config) {
			c.WorkspaceMode = "mount"
			c.WorkspacePath = "/no/such/directory/here"
		}, "WORKSPACE_PATH"},
		{"port out of range", func(c *Config) { c.ProxyPort = 70000 }, "PROXY_PORT"},
		{"unknown proxy", func(c *Config) { c.ProxyKind = "nginx" }, "PROXY_KIND"},
		{"missing opsdir", func(c *Config) { c.OpsDir = "/no/such/directory/here" }, "OpsDir"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := base(t)
			tc.break_(c)
			err := c.Validate()
			if err == nil {
				t.Fatal("Validate accepted it")
			}
			// Every message must name the setting: this is the last place a
			// mistake can be reported in terms the person recognises.
			if !strings.Contains(err.Error(), tc.wants) {
				t.Errorf("message does not name %s: %v", tc.wants, err)
			}
		})
	}
}

// The audit log must not be inside anything mounted read-write into the VM.
// This is the whole reason audit/ exists - see the issue.
func TestTrafficLogIsNotUnderLogs(t *testing.T) {
	c := Default()
	c.OpsDir = "/srv/pixelgo-ops"

	traffic := c.TrafficLogPath()
	logs := filepath.Join(c.OpsDir, DirLogs)

	if strings.HasPrefix(traffic, logs+string(filepath.Separator)) {
		t.Errorf("traffic log %q is inside the writable %q", traffic, logs)
	}
	if !strings.HasPrefix(traffic, c.AuditDir()+string(filepath.Separator)) {
		t.Errorf("traffic log %q is not under audit/", traffic)
	}
}

func write(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(Path(dir), []byte(content), 0o644); err != nil {
		t.Fatalf("writing config: %v", err)
	}
}
