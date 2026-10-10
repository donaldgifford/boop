// Package main is the entry point for boopd.
//
// Usage:
//
//	boop                          print the version
//	boop version                  print the version
//	boop config validate <file>   load and validate a config file; exit 1 on errors
package main

import (
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/donaldgifford/boop/internal/config"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// Exit codes.
const (
	exitOK      = 0
	exitInvalid = 1
	exitUsage   = 2
)

const usage = `usage:
  boopd [version]
  boopd config validate <file>
  boopd worker --config <file>
`

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, nil)))
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	switch {
	case len(args) == 0, len(args) == 1 && args[0] == "version":
		return write(stdout, exitOK, "boopd %s (%s, %s)\n", version, commit, date)
	case len(args) == 3 && args[0] == "config" && args[1] == "validate":
		return validateConfig(args[2], stdout, stderr)
	case len(args) >= 1 && args[0] == "worker":
		return runWorker(args[1:], stdout, stderr)
	default:
		return write(stderr, exitUsage, "%s", usage)
	}
}

// write prints to w and returns code, or exitInvalid if the write fails.
func write(w io.Writer, code int, format string, args ...any) int {
	if _, err := fmt.Fprintf(w, format, args...); err != nil {
		return exitInvalid
	}
	return code
}

// validateConfig loads and validates path without reading secrets or
// starting anything. Diagnostics go to stderr, GCC-style, all of them.
func validateConfig(path string, stdout, stderr io.Writer) int {
	cfg, diags := config.Load(path)
	if _, err := diags.WriteTo(stderr); err != nil {
		return exitInvalid
	}
	if diags.HasErrors() {
		return exitInvalid
	}
	if _, err := cfg.Resolver(); err != nil {
		return write(stderr, exitInvalid, "%s: %v\n", path, err)
	}
	return write(stdout, exitOK, "%s: ok\n", path)
}
