// Package hypervisor abstracts the differences between KVM (Linux) and
// Hyper-V (Windows).
//
// The rest of the code calls these operations without knowing what it runs on.
// When VirtualBox is added for Windows Home, it is just one new file
// implementing the same interface.
//
// SKELETON - no implementation is complete.
package hypervisor

// Mount describes a host directory made visible inside the VM.
//
// ReadOnly is not a convention, it is a guarantee enforced by the hypervisor:
// from inside the VM it cannot be lifted, not even by root. See README.
type Mount struct {
	HostPath  string // path on the host
	GuestPath string // where it shows up in the VM
	Tag       string // label used at mount time
	ReadOnly  bool
}

// VMConfig describes the machine about to be created.
type VMConfig struct {
	Name     string
	MemoryMB int
	CPUs     int
	DiskGB   int
	Image    string // the base image
	Mounts   []Mount
	Network  string // name of the isolated network
}

// Hypervisor is what every backend needs to know.
type Hypervisor interface {
	// Available reports whether the hypervisor can be used on this system.
	// Used by "pixelgo-ops check".
	Available() error

	// Create defines the machine. Does not start it.
	Create(cfg VMConfig) error

	// Start starts an already defined machine.
	Start(name string) error

	// Stop requests a clean shutdown and waits.
	Stop(name string) error

	// Destroy stops immediately, whatever state the inside is in.
	// This is the kill switch. It cannot be blocked from inside the VM.
	Destroy(name string) error

	// Exists reports whether the machine is defined.
	Exists(name string) (bool, error)

	// Running reports whether the machine is running right now.
	Running(name string) (bool, error)
}

// Detect picks the right backend for the current system.
//
// TODO: on Windows, if Hyper-V is missing (Home edition), fall back to
// VirtualBox.
func Detect() (Hypervisor, error) {
	panic("not implemented")
}
