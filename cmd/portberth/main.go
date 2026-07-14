// Command portberth is a local port registry: it assigns stable ports
// per project and explains, with provenance, why a port is not available.
package main

import (
	"os"

	"github.com/JaydenCJ/portberth/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], cli.Env{}))
}
