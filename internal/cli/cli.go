// Package cli holds the commands exposed to the user.
// The commands are the ones described in the README, Usage section.
//
// SKELETON - handlers return "not implemented".
package cli

import (
	"errors"
	"flag"
	"fmt"

	"github.com/pixelgo-io/pixelgo-ops/internal/config"
	"github.com/pixelgo-io/pixelgo-ops/internal/ops"
)

const usage = `pixelgo-ops - isolated virtual machine for the pixelgo agent

  init                creates the structure and the default configuration
  check               checks the environment before the first run
  up                  starts the VM, the proxy and the agent
  down                clean shutdown
  kill                immediate shutdown, no cleanup

  status              what is running, pending requests, budget spent
  logs [--follow]     the agent's log

  pending             approval requests
  show <id>           the contents of a request
  approve <id>        moves the request to approved/
  reject <id>         moves the request to rejected/

  allow <domain>      add to the whitelist
  deny <domain>       remove from the whitelist
  allowed             what is allowed right now
  traffic [--denied]  what the agent requested
`

// Run dispatches the command to the right handler.
func Run(args []string) error {
	if len(args) == 0 {
		fmt.Print(usage)
		return nil
	}

	switch args[0] {
	case "init":
		return cmdInit(args[1:])
	case "check":
		return cmdCheck(args[1:])
	case "up":
		return cmdUp(args[1:])
	case "down":
		return cmdDown(args[1:])
	case "kill":
		return cmdKill(args[1:])
	case "status":
		return cmdStatus(args[1:])
	case "logs":
		return cmdLogs(args[1:])
	case "pending", "show", "approve", "reject":
		return cmdApproval(args[0], args[1:])
	case "allow", "deny", "allowed", "traffic":
		return cmdNetwork(args[0], args[1:])
	case "help", "-h", "--help":
		fmt.Print(usage)
		return nil
	default:
		return fmt.Errorf("unknown command: %s", args[0])
	}
}

// cmdInit creates the structure described in the README and a default
// configuration. Runs once. Does not overwrite an existing structure.
func cmdInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	dir := fs.String("dir", config.DefaultOpsDir(), "where the structure goes")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if err := ops.Init(*dir); err != nil {
		if errors.Is(err, ops.ErrExists) {
			// Not a failure worth an exit code: the person ran init twice, and
			// the answer is that there is nothing to do.
			fmt.Printf("%s is already initialised - nothing changed.\n", *dir)
			return nil
		}
		return err
	}

	fmt.Printf("Created %s\n\n", *dir)
	for _, d := range ops.AllDirs() {
		note := "read-only in the VM"
		if !ops.Mounted(d) {
			note = "not mounted - the agent cannot reach it"
		} else if ops.Dirs[d] {
			note = "read-write in the VM"
		}
		fmt.Printf("  %-10s %s\n", d+"/", note)
	}

	fmt.Printf("\nNext: write the rules that govern the agent into %s/%s,\n",
		*dir, config.DirRules)
	fmt.Println("then run \"pixelgo-ops check\".")
	return nil
}

// cmdCheck verifies: virtualization support, hypervisor availability, the
// structure, permissions, and that rules/ is not empty.
// Changes nothing. Reports what is missing.
func cmdCheck(args []string) error { return errNotImplemented("check") }

// cmdUp: creates the VM if it does not exist, attaches the mounts with the
// correct flags, applies the firewall rules, starts the proxy, starts the VM,
// launches the agent.
//
// Refuses to start if rules/ is empty or if the proxy cannot start.
func cmdUp(args []string) error { return errNotImplemented("up") }

// cmdDown stops, in order: the agent, the VM, the proxy. Keeps the disk.
func cmdDown(args []string) error { return errNotImplemented("down") }

// cmdKill stops the VM immediately, whatever state it is in.
// The equivalent of "virsh destroy". Waits for nothing.
func cmdKill(args []string) error { return errNotImplemented("kill") }

func cmdStatus(args []string) error { return errNotImplemented("status") }
func cmdLogs(args []string) error   { return errNotImplemented("logs") }

// cmdApproval covers pending, show, approve, reject.
//
// approve moves the file from pending/ to approved/. The move IS the act of
// approval - see README. The agent has no write access to approved/.
func cmdApproval(name string, args []string) error { return errNotImplemented(name) }

// cmdNetwork covers allow, deny, allowed, traffic.
func cmdNetwork(name string, args []string) error { return errNotImplemented(name) }

func errNotImplemented(name string) error {
	return fmt.Errorf("%s: not implemented", name)
}
