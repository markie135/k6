// Package preflight is a small offline checker for this fork's own-use profile.
//
// It validates a per-app profile, classifies result states, and prints the
// existing k6 CLI arguments a person could run later. It does not start k6,
// open a socket, or write a result that claims a test passed.
//
// The executable wrapper is ownuse/preflight/cmd. Its only modes are inspect
// and dry-run. There is no run mode.
package preflight
