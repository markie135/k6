// Command preflight validates an own-use profile and prints a dry-run plan.
// It does not start k6.
package main

import (
	"os"

	"go.k6.io/k6/v2/ownuse/preflight"
)

func main() {
	os.Exit(preflight.Run(os.Args[1:], os.Stdout, os.Stderr)) //nolint:forbidigo // process exit only
}
