//go:build linux

package hypervisor

// KVM uses libvirt and virtiofs.
//
// Read-only mounts are configured in the domain XML, on the host.
// From inside the VM they cannot be remounted read-write.
type KVM struct {
	// TODO: libvirt connection, or calls out to virsh
}

func (k *KVM) Available() error {
	// TODO:
	//   - /proc/cpuinfo contains vmx or svm
	//   - libvirtd is running
	//   - the user is in the libvirt group
	panic("not implemented")
}

func (k *KVM) Create(cfg VMConfig) error {
	// TODO:
	//   - copy the base image or create an overlay
	//   - generate XML from templates/libvirt-domain.xml
	//   - every Mount becomes <filesystem type="mount" accessmode="passthrough">
	//     with <readonly/> where applicable
	//   - virsh define
	panic("not implemented")
}

func (k *KVM) Start(name string) error   { panic("not implemented") } // virsh start
func (k *KVM) Stop(name string) error    { panic("not implemented") } // virsh shutdown
func (k *KVM) Destroy(name string) error { panic("not implemented") } // virsh destroy

func (k *KVM) Exists(name string) (bool, error)  { panic("not implemented") }
func (k *KVM) Running(name string) (bool, error) { panic("not implemented") }
