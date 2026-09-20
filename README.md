# pixelgo-ops

> **Status: early.** The structure is settled. A few implementation decisions
> are still open — see [Open questions](#open-questions).

## Contents

If you want to understand the idea, read **How it works**. If you want to
install it, skip to **Installation**. If you are wondering what it cannot do,
there is **What it does not solve** and **Open questions**.

- [**About**](#about) — what it is, what it is not, and the three ways to run
  pixelgo
- [**How it works**](#how-it-works) — why a VM, where the rules live, what the
  agent sees
  - [The structure](#the-structure) — what is on the host, what is in the VM
  - [Kill switch](#kill-switch--stopping-the-vm-instantly) — how you stop
    everything instantly
- [**The network**](#the-network) — why the VM has no direct internet
  - [Why a proxy](#why-a-proxy)
  - [Why a whitelist](#why-a-whitelist)
  - [Configuring the proxy](#configuring-the-proxy) — tinyproxy or Squid
  - [Traffic auditing](#traffic-auditing) — what the agent asked for
  - [What domain filtering does not cover](#what-domain-filtering-does-not-cover)
- [**Installation**](#installation) — requirements and the five steps
- [**Usage**](#usage) — the commands, where the code comes from
- [**What it does not solve**](#what-it-does-not-solve) — the real limits of
  the approach
- [**Open questions**](#open-questions) — what is not decided yet

---

## About

Starts a local virtual machine for [pixelgo](https://github.com/pixelgo-io/pixelgo).
Works on Linux and on Windows.

You point it at a directory, and it creates an isolated machine, mounts what
has to be writable as writable and what does not as read-only, and runs the
agent inside.

**This is not pixelgo.** pixelgo is the agent framework. *pixelgo-ops* is the
box you put it in.

And it is not mandatory. There are three ways to run pixelgo:

**Directly on your machine** — no VM, no restrictions. The agent has access to
everything you have. Simple, fast, no safety net at all.

**In a VM you build yourself** — with virt-manager or straight from libvirt.
You get isolation, but the configuration is your own responsibility: the
mounts with the right permissions, the network without NAT, the proxy, the
firewall rules. That is around twenty steps, and a missed setting produces no
error message.

**Through pixelgo-ops** — the same VM, but configured correctly the first
time, with one command.

That is all the tool does: it does not invent the isolation, it configures it.
Anyone can build the VM — the time-consuming part is getting every setting
right, and some of them only show up late. A share left without read-only
looks identical to a correct one, until the agent writes to it.

---

## How it works

An agent working autonomously needs freedom: to install packages, start
services, break things. That is fine — inside a VM.

The problem is the rules. An agent with root privileges can modify any file on
its own machine — including the document that governs it, the approvals
directory, or the shutdown command. A rule it can delete is not a rule.

*pixelgo-ops* moves the boundary to where the agent cannot reach: into the
hypervisor.

The hypervisor is the program on your machine that creates and runs the VM —
KVM on Linux, Hyper-V on Windows. It lives on the host, not inside the VM. The
virtual machine is a window the hypervisor opens, and nothing on the inside
can get back out through it.

Your rules live in */srv/pixelgo-ops/*, on the host. When the VM starts, the
hypervisor shows it those directories and decides, for each one, whether the
VM may only read them or also write to them. The decision is made on the host,
so from the inside it cannot be changed — not even by root.
*mount -o remount,rw* fails. *chmod* does not help. The configuration file
that defines the mount does not even exist inside the VM.

### The structure

```
HOST: /srv/pixelgo-ops/           (Windows: C:\pixelgo-ops\)
  rules/           # mounted read-only in the VM
  public/          # mounted read-only in the VM
  approved/        # mounted read-only in the VM
  rejected/        # mounted read-only in the VM
  pending/         # mounted read-write
  logs/            # mounted read-write — the agent's own journals
  audit/           # NOT mounted — records *of* the agent, it cannot reach them
    traffic        # what the agent asked for, allowed and denied

  allowlist        # the permitted domains, not mounted in the VM
  proxy:           # mandatory — the VM's only route to the internet

VM (Debian, cloud image or ISO)
  # isolated network, no NAT — the only way out is the proxy on the host
  /usr/local/bin/pixelgo    # the agent binary
  /var/run/pixelgo/         # PID, socket
  /workspace/               # your project — what the agent works on
                            # (pixelgo selfhost, an Android app, anything)
  /mnt/ops/                 # the host directories, mounted here:
    rules/                  # read-only
    public/                 # read-only
    approved/               # read-only
    rejected/               # read-only
    pending/                # read-write
    logs/                   # read-write
```

The agent is root in the VM, but the read-only directories stay read-only.

#### What is on the host

**rules/** — the document that governs the agent: the objective, the absolute
rules, what it may do freely, what needs approval, when it has to stop. You
write it, before the first start, and the agent reads it on every cycle.

Read-only because otherwise the first rule it would rewrite is the one that
inconveniences it. Not out of malice — a system optimising towards an
objective treats any obstacle as a problem to solve, and its own constraints
are an obstacle.

**public/** — your documents about the project intended for the public: what
the agent does, what limits it has, what it cannot do. The agent may read them,
so it knows what has been promised in its name, but it cannot modify them. If
it could, it could rewrite the public description of its own limits — exactly
the thing nobody would notice in time.

**pending/** — the only place the agent creates files through the approval
mechanism. When it reaches an action that requires your consent, it does not
execute it: it writes a request here with what it wants to do, why, what risk
it sees, what tests were run, and how to back out if it goes wrong. Then it
marks the task as blocked and moves on to the next one. It does not sit and
wait — otherwise you would become the bottleneck, and that leads to relaxing
the rules out of frustration.

**approved/** — this is where you move the files you approve. The move *is*
the act of approval. That is why it is read-only from the VM: if the agent
could write here, it could grant itself any permission, and the whole
mechanism would be decoration. A file that appears in *approved/* which you
did not move there is not an approval, it is an incident.

**rejected/** — the rejected requests. A rejected request is not resent, not
reworded, not broken into smaller pieces. Read-only so the agent cannot delete
a rejection and start over.

**logs/** — the agent's journals. The recommendation is two, written in
parallel: a complete one, which stays yours, and one written from the start as
intended for publication, with no keys, no paths containing your username, no
personal data of others. A complete journal filtered before publication
requires the filter to work perfectly every single time, forever. A journal
written as public cannot leak what was never written into it.

**audit/** — records *of* the agent, rather than *by* it: the proxy's traffic
log, and the budget log if the credential gateway is ever built.

Not mounted at all, unlike *logs/*. The distinction matters: *logs/* is the
agent's own journal and it has to be able to write there, but a record of what
it attempted is only evidence if it cannot edit it. The refusals in
*audit/traffic* are the most useful thing the proxy produces — an agent that
could truncate them would remove exactly the part worth reading.

**allowlist** — the domains the agent may reach. Not mounted in the VM. The
agent does not need to know what is permitted; it finds out by trying, and the
refusal appears to it as an ordinary network error.

**proxy** — not a directory, a process. It runs on the host, starts together
with the VM, and is the only route through which the VM reaches the internet.
Detailed below.

#### What is in the VM

**/usr/local/bin/pixelgo** — the agent binary, installed at provisioning. It
is not compiled here. If you want to develop pixelgo itself, its source code
goes into */workspace/*, like any other project.

**/var/run/pixelgo/** — the process PID and the socket through which
*pixelgo-ops* talks to the agent from the outside. Disappears on shutdown,
like everything in */var/run*.

**/workspace/** — the project the agent works on. Here it has complete
freedom: it writes, refactors, deletes, compiles, runs tests, breaks and
repairs. It is root in the VM, so it can install packages and configure the
system as it needs.

Nothing it does here requires approval, because nothing it does here leaves
for the outside world. The dividing line is not between what the agent can and
cannot do — it is between what stays in the VM and what leaves it.

**/mnt/ops/** — the window onto the host. The six directories appear here,
with exactly the permissions set at startup. It is the only point at which the
VM touches your system; beyond it, it sees nothing of the host.

#### What persists and what does not

The VM is something you can recreate at any time. The binary is reinstalled,
*/var/run* regenerates. What matters is either mounted from the host, or in
*/workspace/*.

*/workspace/* lives as long as the VM's disk. If you recreate the machine from
scratch, the project there disappears with it — so it is worth keeping in git,
like any project.

### Kill switch — stopping the VM instantly

```bash
virsh destroy pixelgo-vm
```

Runs on the host. Stops the VM instantly, whatever state it is in. The agent
cannot block it, disable it or anticipate it, because it executes outside the
machine it controls.

Without all this, any rules document is a request the agent honours only for
as long as it wants to. With read-only mounts, compliance no longer depends on
goodwill. The agent does not refrain from rewriting its rules. It simply
cannot.

---

## The network

### Why a proxy

Read-only mounts stop the agent from rewriting its rules. They do not stop it
from doing a *git push* — it has the binary and it has the network.

That is why the VM gets no direct outbound access. Its network is isolated,
without NAT, and the only route outwards is a process running on the host.

**The proxy is not optional.** *pixelgo-ops* does not start without it.
Without the proxy, the tool would offer isolation alone, and isolation alone
stops no action — the agent could publish, send messages, spend money. The
value lies in the combination of the two.

The constraints cannot be switched off one at a time until an ordinary VM is
left. If you use *pixelgo-ops*, you use it with everything that comes with it.

The agent cannot go around it: there is no other route, and the configuration
is not inside the VM. The same logic as with the mounts — the decision is made
on the host, where the agent cannot reach.

### Why a whitelist

You enumerate what is permitted, not what is forbidden.

With a blacklist you would try endlessly to enumerate the world, and something
would still be left out. With a whitelist, the list is short and you know it:
the model API, the package registries, maybe GitHub. The rest gets refused,
whatever it is — it does not matter whether it is a forum, a storage service
or something else. You need no categories and no content filters.

The question becomes "why does it need that?", and that has an answer.

**Start with the list almost empty.** Only the model API, without which the
agent does not work. Watch the log and add what is justified. The reverse —
permissive at the start, narrowed over time — does not work, because you can
no longer tell what is necessary from what slipped in.

The whitelist is a file with one domain per line, living on the host:

```bash
pixelgo-ops allow api.anthropic.com
pixelgo-ops allow deb.debian.org
pixelgo-ops allowed
pixelgo-ops deny <domain>
```

### Configuring the proxy

Which proxy exactly does not matter for the design. What matters is that it
runs on the host and that the agent cannot go around it. *pixelgo-ops*
generates the configuration and starts the process at *up*, stops it at
*down* — you do not edit proxy files by hand.

Two common options, both doing the same thing: permit what is on the list,
refuse the rest, write everything to the log.

**tinyproxy** — small, a few lines of configuration:

```
Port 8888
Allow 192.168.100.0/24
FilterURLs On
Filter /srv/pixelgo-ops/allowlist
FilterDefaultDeny Yes
LogFile "/srv/pixelgo-ops/audit/traffic"
```

**Squid** — larger, useful if you end up wanting a package cache or rate
limits:

```
acl vm src 192.168.100.0/24
acl allowed dstdomain "/srv/pixelgo-ops/allowlist"
http_access allow vm allowed
http_access deny all
access_log /srv/pixelgo-ops/audit/traffic
```

Alongside the proxy, *pixelgo-ops* also puts in firewall rules that block any
other way out. Without them, the agent could ignore the proxy and talk
directly over IP.

### Traffic auditing

Every request lands in *audit/traffic*: the time, the domain, allowed or
refused.

```bash
pixelgo-ops traffic              # everything
pixelgo-ops traffic --denied     # refusals only
```

The refusals are the interesting part. Not because they got anywhere — they
did not — but because they show you where the agent's reasoning went. An
unexpected request means its decisions took a direction you did not
anticipate, and that is worth reading.

The log is also the simplest way to build the whitelist: you start with the
minimal list, look at what was refused, add what is justified.

### What domain filtering does not cover

A domain whitelist does not distinguish between actions on the same domain. A
*clone* and a *push* both go to GitHub, on the same port, over HTTPS. The
proxy sees the hostname, not what happens inside.

The simple solution: **no credentials in the VM.** No SSH key, no token, and
*push* fails on its own, without any network rule being needed.

The complete solution would be a service on the host that understands the git
protocol, lets *fetch* through and sends *push* into *pending/*. It does not
exist yet — see [Open questions](#open-questions).

For research, a tight whitelist means the agent cannot browse freely. The
usual path is a search API on the list, through which web access is mediated
rather than direct.

---

## Installation

*pixelgo-ops* works on Linux and on Windows. The inside of the virtual machine
is identical in both cases — Debian, with the same structure and the same
agent. What differs is only who starts the machine: KVM on Linux, Hyper-V on
Windows.

The commands are the same everywhere. Only steps 1 and 2 fork.

### Requirements

A processor with virtualization support, ~8 GB free on disk and 4 GB of RAM
available.

On Linux, check the support:

```bash
grep -cE 'vmx|svm' /proc/cpuinfo
```

On Windows, in PowerShell:

```powershell
systeminfo | Select-String "Virtualization"
```

If it reports that virtualization is unavailable, it is disabled in BIOS/UEFI
and has to be enabled from there.

Tested on Debian 13 and Windows 11.

> **The steps below have not yet been tested on all systems.** They are
> derived from the official documentation, not from real installations. They
> will be corrected as concrete results come in. If something does not work,
> open an issue — that is exactly the information missing right now.

### Step 1 — dependencies

**Linux**

```bash
sudo apt install qemu-system-x86 qemu-utils \
                 libvirt-daemon-system libvirt-clients \
                 virtinst
sudo usermod -aG libvirt $USER
```

Log out and back in, otherwise the group membership does not apply.

Verify:

```bash
systemctl is-active libvirtd     # must be: active
virsh list --all                 # must run without a permissions error
```

**Windows**

Hyper-V, enabled from PowerShell as administrator:

```powershell
Enable-WindowsOptionalFeature -Online -FeatureName Microsoft-Hyper-V -All
```

Restart, then verify:

```powershell
Get-VMHost
```

Hyper-V exists only on Windows Pro, Enterprise and Education. On Home it is
unavailable — see below.

**Why Hyper-V and not VirtualBox:** if you use WSL2 or Docker Desktop, you
already have Hyper-V running. WSL2 runs the Linux kernel inside a Hyper-V VM
and does not work without it. And Hyper-V and VirtualBox cannot run at the
same time — VirtualBox requires Hyper-V disabled, which stops WSL2 and Docker.

In other words, if you have a development environment on Windows, Hyper-V is
probably already there, and VirtualBox would break what you have.

**On Windows Home**, where Hyper-V is missing, what remains is
[VirtualBox](https://www.virtualbox.org/). Install with the default options,
then:

```powershell
VBoxManage --version
```

If it does not respond, add the installation directory to PATH, usually
*C:\Program Files\Oracle\VirtualBox*.

One thing to know up front: VirtualBox shares files through shared folders,
where read-only is enforced by the driver inside the virtual machine. On Linux
and on Hyper-V, the restriction comes from the outside. For experiments it
does not matter, but the difference is real.

### Step 2 — pixelgo-ops

**Linux**

```bash
git clone https://github.com/pixelgo-io/pixelgo-ops.git
cd pixelgo-ops
sudo make install
```

**Windows**

```powershell
git clone https://github.com/pixelgo-io/pixelgo-ops.git
cd pixelgo-ops
.\install.ps1
```

### Step 3 — initialization

```bash
pixelgo-ops init
```

Creates the structure and a configuration file with default values. Run once.

The location differs by system:

```
Linux     /srv/pixelgo-ops/
Windows   C:\pixelgo-ops\
```

On Linux, the command needs *sudo* because it writes into */srv*.

### Step 4 — your rules

*rules/* is empty after *init*. Put the document that governs the agent there
— the objective, the limits, what requires approval.

**The agent does not start with rules/ empty.** An agent with no written rules
has no way to follow them.

### Step 5 — verification

```bash
pixelgo-ops check
```

Checks virtualization, the hypervisor, the structure, the permissions, and
whether *rules/* contains anything. It tells you what is missing before you
start anything.

---

## Usage

```bash
pixelgo-ops up               # creates and starts the VM, launches the agent
pixelgo-ops status           # what is running, pending requests, budget spent
pixelgo-ops logs             # follow the agent's journal
pixelgo-ops logs --follow

pixelgo-ops pending          # list the approval requests
pixelgo-ops show <id>        # the contents of a request
pixelgo-ops approve <id>     # moves the request into approved/
pixelgo-ops reject <id>      # moves the request into rejected/

pixelgo-ops down             # clean shutdown
pixelgo-ops kill             # immediate stop, no cleanup
```

*up* takes care of everything: creates the VM, adds the virtiofs shares with
the correct read-only flags, mounts them on the inside and starts pixelgo.

The manual clicking in virt-manager disappears. That is the point of the tool.

### Where the code comes from

Two modes, your choice.

**Clone** — *pixelgo-ops* brings pixelgo from git into the VM. Clean,
reproducible, good when you are running someone else's agent.

**Mount** — you point at a local directory and it is mounted read-write. Your
working copy, your editor, your git history. Good when you are developing
pixelgo itself.

The isolation is the same in both cases. Only the source differs.

---

## What it does not solve

Isolation plus the whitelist covers a lot, but not everything.

**It does not distinguish actions on the same domain.** A *push* and a *clone*
look identical to a proxy that only sees the hostname. In practice this is
solved by not having credentials in the VM, not by network rules.

**It does not stop the agent from making mistakes in the workspace.** It can
break the project it is working on. That is what git is for, and that is why
backups remain your job.

**It does not judge content.** If a domain is permitted, everything coming
through it is permitted. A search API on the list can return anything.

**It does not replace written rules.** An agent that has no way to publish can
still make bad decisions inside its limits. The technical constraints cover
what can happen; the document in *rules/* covers what should happen.

---

## Open questions

Not decided yet. Open an issue if you have an opinion.

**How the VM is created.** A cloud image plus cloud-init would make *up* fully
automatic — no ISO, no installer. The cost is that the guest system is the one
from the image. The alternative is a pre-built image, which starts faster, but
which somebody has to build and host.

**The configuration format.** YAML is expected, but adds a parser. A flat
*KEY=value* file stays easy to read with *grep* and to use from a shell.
Command-line arguments only is the simplest and the worst for reproducibility.

**Which proxy.** tinyproxy is enough for whitelist plus log and is configured
in a few lines. Squid offers more — a package cache, rate limits — but the
configuration is bulky. It can be swapped at any time; the design does not
depend on it.

**A gate for git.** A service on the host that understands the git protocol
would let *fetch* through and send *push* into *pending/*, instead of relying
on the absence of credentials. That is new software, which does not exist.
Possibly a separate component.

**Windows Home.** Hyper-V is missing there, so VirtualBox remains — with file
sharing that offers weaker guarantees than SMB on Hyper-V or virtiofs on
Linux. It remains to be seen whether two code paths for Windows are worth it,
or whether Home stays officially unsupported.

**The guest system.** Debian and Ubuntu are the obvious choices. Alpine would
boot faster and smaller, but pixelgo is written in C, and the musl/glibc
difference will matter at some point.

---

## Documentation

This README covers running the infrastructure. For the framework itself — what
pixelgo is, how workflows are defined, the API — see the
[pixelgo repository](https://github.com/pixelgo-io/pixelgo).

For writing the document that governs the agent, see [*docs/*](docs/)
*(work in progress)*.

---

## Licence

MIT.
