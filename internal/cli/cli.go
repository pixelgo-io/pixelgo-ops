// Package cli holds the commands exposed to the user.
// The commands are the ones described in the README, Usage section.
//
// SKELETON - handlers return "not implemented".
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/pixelgo-io/pixelgo-ops/internal/config"
	"github.com/pixelgo-io/pixelgo-ops/internal/engine"
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

// cmdCheck reports on the environment and changes nothing.
//
// What it checks is in ops.RunChecks. The hypervisor and proxy checks are
// listed as pending rather than run, because the functions behind them panic -
// see the backend issues. They are listed anyway: a report that silently drops
// the checks it cannot do is a report that passes on a machine where nothing
// could start.
//
// Prints every line, including the ones that failed, instead of stopping at the
// first problem. Someone running this has usually just installed the thing and
// would rather see all three missing pieces at once than discover them one run
// at a time.
//
// The failure is reported by the exit code, not by returning an error: an error
// would print "error: ..." after a report that has already said what is wrong,
// in more detail and with the remedy.
func cmdCheck(args []string) error {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	dir := fs.String("dir", config.DefaultOpsDir(), "where the structure is")
	if err := fs.Parse(args); err != nil {
		return err
	}

	results := ops.RunChecks(*dir)

	fmt.Println()
	failed := 0
	pending := 0
	for _, r := range results {
		switch {
		case r.OK:
			fmt.Printf("  ok    %s\n", r.Name)
		case r.Pending:
			pending++
			fmt.Printf("  --    %s (%s)\n", r.Name, r.Remedy)
		default:
			failed++
			fmt.Printf("  FAIL  %s\n", r.Name)
			fmt.Printf("          %s\n", r.Remedy)
		}
	}
	fmt.Println()

	if failed > 0 {
		fmt.Printf("%d check(s) failed.\n", failed)
		os.Exit(1)
	}
	if pending > 0 {
		fmt.Printf("Everything that can be checked passed; %d check(s) are not written yet.\n",
			pending)
		return nil
	}
	fmt.Println("All checks passed.")
	return nil
}

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

// cmdStatus reports what the engine says about itself.
//
// Two questions, not one. Alive and ready are different states, and the gap
// between them is where the useful answers live: an engine that answers but is
// not ready is draining, or cannot write where it records work, and neither of
// those looks like anything from the outside.
//
// What it does NOT report yet is the VM. That needs the hypervisor backends,
// which are skeletons; printing nothing about them is better than printing a
// guess, so this says what it knows and names what it does not.
func cmdStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	dir := fs.String("dir", config.DefaultOpsDir(), "where the structure is")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(*dir)
	if err != nil {
		return err
	}

	c := engine.New(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), engine.DefaultTimeout)
	defer cancel()

	fmt.Println()
	fmt.Printf("  engine socket:  %s\n", c.Addr())

	h, err := c.Connect(ctx)
	switch {
	case errors.Is(err, engine.ErrUnreachable):
		fmt.Printf("  engine:         NOT ANSWERING\n")
		fmt.Printf("\n  %v\n", err)
		fmt.Printf("\n  Start pixelgo with PIXELGO_UNIX_SOCKET=%s\n\n", c.Addr())
		os.Exit(1)
	case errors.Is(err, engine.ErrContract):
		// Printed as a refusal rather than a warning: ops serves its own copy
		// of pixelgo's UI, and serving it against an engine that disagrees
		// about the shape of the API is the failure this check exists to stop.
		fmt.Printf("  engine:         REFUSED\n")
		fmt.Printf("\n  %v\n\n", err)
		os.Exit(1)
	case err != nil:
		fmt.Println()
		return err
	}

	fmt.Printf("  engine:         RUNNING\n")
	fmt.Printf("  version:        %s\n", h.Version)
	fmt.Printf("  contract:       %d\n", h.Contract)

	r, err := c.Ready(ctx)
	if err != nil {
		fmt.Println()
		return err
	}
	if r.Ready {
		fmt.Printf("  accepting work: yes\n")
	} else {
		fmt.Printf("  accepting work: no (%s)\n", r.Reason)
	}

	fmt.Printf("  vm:             not reported yet - the hypervisor backends are skeletons\n")
	fmt.Println()

	// Not ready is a real state the caller may want to act on, and it is not an
	// error: the report printed correctly, the engine is simply not available
	// for work. Exit 2 distinguishes it from unreachable (1) and from success.
	if !r.Ready {
		os.Exit(2)
	}
	return nil
}

// cmdLogs prints the end of the engine's log.
//
// Control characters are stripped before anything reaches the terminal. The
// lines come from an agent's output and from pages it fetched, so a line can
// carry terminal escape sequences; pixelgo returns the record as logged, and
// says in its docs/API.md that filtering is the client's job. This is that job,
// and it is not optional - printing one of those to a terminal unfiltered is
// how it becomes the agent's to exploit.
func cmdLogs(args []string) error {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	dir := fs.String("dir", config.DefaultOpsDir(), "where the structure is")
	lines := fs.Int("lines", 0, "how many lines (0 = the engine's default)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(*dir)
	if err != nil {
		return err
	}

	c := engine.New(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), engine.DefaultTimeout)
	defer cancel()

	if _, err := c.Connect(ctx); err != nil {
		return err
	}

	l, err := c.Logs(ctx, *lines)
	if err != nil {
		return err
	}

	if len(l.Lines) == 0 {
		fmt.Println("(the engine has no log - a pixelgo started without --daemon writes none)")
		return nil
	}

	if l.Truncated {
		fmt.Println("... (earlier lines were not returned)")
	}
	for _, line := range l.Lines {
		fmt.Println(sanitize(line))
	}
	return nil
}

// sanitize removes control characters from a log line, keeping tabs.
//
// Everything below 0x20 except tab goes, and so does 0x7f: that range is where
// the escape sequences live that move a cursor, rewrite earlier output or
// change a terminal's mode. Tab survives because log lines use it for alignment
// and it cannot do any of that.
//
// Invalid UTF-8 is replaced rather than passed through, so one corrupt byte
// cannot leave a terminal in a strange state for the rest of the output.
func sanitize(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\t':
			b.WriteRune(r)
		case r == utf8.RuneError:
			b.WriteRune('�')
		case r < 0x20 || r == 0x7f:
			// dropped
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

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
