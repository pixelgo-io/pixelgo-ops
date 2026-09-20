// Command pixelgo-ops starts and manages the virtual machine the pixelgo
// agent runs in.
//
// Mostly still a skeleton: "init" works, the rest returns "not implemented".
package main

import (
	"fmt"
	"os"

	"github.com/pixelgo-io/pixelgo-ops/internal/cli"
)

func main() {
	if err := cli.Run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
