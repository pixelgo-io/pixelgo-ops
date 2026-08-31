//go:build windows

package hypervisor

// HyperV uses PowerShell cmdlets and SMB shares.
//
// Unlike virtiofs, sharing goes over SMB. Read-only is still enforced by the
// host, so the guarantee stays comparable.
//
// Hyper-V exists only on Windows Pro, Enterprise and Education.
type HyperV struct{}

func (h *HyperV) Available() error {
	// TODO:
	//   - Get-VMHost responds
	//   - the Windows edition supports Hyper-V
	//   - virtualization is enabled in BIOS
	panic("not implemented")
}

func (h *HyperV) Create(cfg VMConfig) error {
	// TODO:
	//   - New-VM
	//   - New-VHD or copy the base image
	//   - for each Mount: an SMB share on the host, with the right permissions
	//   - network: New-VMSwitch of type Internal, no direct outside access
	panic("not implemented")
}

func (h *HyperV) Start(name string) error   { panic("not implemented") } // Start-VM
func (h *HyperV) Stop(name string) error    { panic("not implemented") } // Stop-VM
func (h *HyperV) Destroy(name string) error { panic("not implemented") } // Stop-VM -TurnOff

func (h *HyperV) Exists(name string) (bool, error)  { panic("not implemented") }
func (h *HyperV) Running(name string) (bool, error) { panic("not implemented") }
