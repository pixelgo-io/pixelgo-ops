# First boot

How to boot the guest by hand, once, before any of it is automated.

The point of this document is to find out what `cloud-init.yaml` actually does
on a real VM. Everything in `templates/` is marked unvalidated, and it stays
that way until someone has watched it boot. Do this with plain QEMU rather than
libvirt: fewer moving parts, and a failure here is a failure of the image or of
cloud-init, not of a domain XML we have also never tested.

libvirt comes after this works.

## 0. The machine

KVM needs Linux. There is no macOS backend and there will not be one in this
repository, so this cannot be done on a Mac.

```sh
ls -l /dev/kvm
```

A missing `/dev/kvm` means either the host is not Linux, or virtualisation is
off in the firmware, or you are inside a VM without nested virtualisation.

```sh
sudo apt-get update
sudo apt-get install -y qemu-system-x86 qemu-utils cloud-image-utils
```

`cloud-image-utils` is for `cloud-localds`, which builds the seed image
cloud-init reads on first boot.

## 1. The base image

The official Debian genericcloud image for amd64. Take the filename from the
directory listing rather than from this document - the release and the point
release both appear in it, and a stale name here would be worse than no name.

```sh
mkdir -p ~/pixelgo-vm && cd ~/pixelgo-vm
# List what is published, pick the genericcloud amd64 qcow2, note the name:
#   https://cloud.debian.org/images/cloud/
# Then, with IMG set to that filename and BASEURL to its directory:
curl -fLO "$BASEURL/$IMG"
curl -fLO "$BASEURL/SHA512SUMS"
sha512sum -c SHA512SUMS --ignore-missing
```

`--ignore-missing` is what makes this check one file instead of failing on the
dozen others in the sums file.

**Record what you downloaded.** The decision in the README says the image is
pinned in this repository, and this is the moment that happens:

```sh
sha512sum "$IMG" | tee -a image-pin.txt
echo "$BASEURL/$IMG" >> image-pin.txt
```

If `sha512sum -c` fails, stop. Do not boot it.

## 2. The overlay

The base image is never written to. The VM gets a qcow2 overlay on top, which
is what makes `kill` cheap and what guarantees the next agent starts from the
same place as the last one.

```sh
qemu-img create -f qcow2 -F qcow2 -b "$PWD/$IMG" overlay.qcow2 20G
qemu-img info overlay.qcow2     # confirm: backing file, and a tiny actual size
```

`-F qcow2` names the backing file's format explicitly. Without it newer
qemu-img refuses, and older ones guess - which is the kind of difference that
makes a script work on one machine and not another.

The backing path is stored in the overlay, so the base cannot be moved or
renamed afterwards without breaking it.

## 3. The seed

A cloud image has no password and no user. Everything about first boot comes
from the seed, so to see what happens you need a way in:

```sh
cat > user-data <<'YAML'
#cloud-config
hostname: pxtest
password: pxtest
chpasswd: { expire: false }
ssh_pwauth: true
packages:
  - curl
runcmd:
  - [ sh, -c, "curl --version > /var/log/px-curl.txt 2>&1" ]
YAML

cat > meta-data <<'YAML'
instance-id: pxtest-001
local-hostname: pxtest
YAML

cloud-localds seed.img user-data meta-data
```

This is deliberately **not** `templates/cloud-init.yaml`. Boot a minimal seed
first and confirm the plumbing works - image boots, cloud-init runs, the
console is reachable, a package can be installed. Only then swap in the real
template, so that when it fails you know the failure is the template's.

A password on the console is acceptable for a throwaway test VM and nowhere
else. The real guest gets no password.

## 4. Boot

```sh
qemu-system-x86_64 \
  -enable-kvm \
  -m 2048 -smp 2 \
  -cpu host \
  -drive file=overlay.qcow2,if=virtio,format=qcow2 \
  -drive file=seed.img,if=virtio,format=raw \
  -netdev user,id=n0 -device virtio-net-pci,netdev=n0 \
  -nographic
```

`-nographic` puts the serial console in the terminal, which is what you want:
cloud-init logs there and you can read a failure instead of guessing at a blank
graphical screen. **Quit with `Ctrl-a` then `x`** - `Ctrl-c` goes to the guest.

`-netdev user` is QEMU's own NAT. It needs no root and no bridge, and it is
enough to prove networking. It is not what the real VM will use, because the
firewall rules in `internal/proxy/firewall.go` match on a source network and
an interface that only exist with a real bridge. Testing the proxy enforcement
needs libvirt's network; this step is not that.

## 5. What to look at

```sh
cloud-init status --long       # done, or error
sudo cat /var/log/cloud-init-output.log
cat /var/log/px-curl.txt       # proof a package installed and ran
```

Then, with that working, the real questions this exercise exists to answer:

- Does `templates/cloud-init.yaml` apply at all - is it valid YAML to
  cloud-init, which is stricter than a YAML parser?
- Do the `virtiofs` entries it writes to `/etc/fstab` mount? `mount -a` in
  `runcmd` fails silently if the tags do not match what the domain XML exports,
  and the appended fstab lines are generated from `.Mounts`.
- Does `http_proxy` in `/etc/environment` actually reach the processes that
  matter? It is read at login, so a systemd service started on boot does not
  see it. That is a real gap if pixelgo is to run as a unit, and the answer
  determines whether the proxy settings belong in the unit instead.
- Which package provides libcurl's runtime on this release. The name changed
  with the time64 transition, and `cloud-init.yaml` has to name it exactly.

The third one is the one most likely to be wrong, and it is invisible until the
agent makes its first outbound call.

## 6. Afterwards

Write down what the boot showed, replace the skeleton markers in
`templates/cloud-init.yaml` with what was observed, and only then move to
`templates/libvirt-domain.xml` and a real bridged network.

Keep `image-pin.txt`.
