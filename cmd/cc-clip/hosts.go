package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"text/tabwriter"
	"time"

	"github.com/shunmei/cc-clip/internal/hosts"
)

// cmdHosts implements `cc-clip hosts <subcommand>`.
//
// The subcommands are intentionally few: list (what have I connected to?) and
// forget (stop tracking this host). Anything more ambitious — batch redeploy,
// status-all, update-all — belongs in a later PR; today this is just an
// inventory.
func cmdHosts() {
	if len(os.Args) < 3 {
		hostsUsage(os.Stderr)
		os.Exit(2)
	}
	switch os.Args[2] {
	case "list":
		hostsList()
	case "forget":
		hostsForget(os.Args[3:])
	case "-h", "--help", "help":
		hostsUsage(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: hosts %s\n", os.Args[2])
		hostsUsage(os.Stderr)
		os.Exit(2)
	}
}

func hostsUsage(w io.Writer) {
	fmt.Fprintln(w, `usage: cc-clip hosts <subcommand>

Subcommands:
  list               Print every host this machine has connected to.
  forget <host>      Stop tracking the given host (does not touch the remote).

The registry is a local cache at ~/.cache/cc-clip/hosts.json. It records
which hosts cc-clip has deployed to, so that update and status commands can
print per-host guidance. Hosts are keyed by the literal SSH target you
passed to connect/setup (e.g. "myserver", "user@venus") — no SSH-config
resolution is performed.`)
}

func hostsList() {
	reg, err := hosts.Load()
	if err != nil {
		log.Fatalf("failed to load host registry: %v", err)
	}
	entries := reg.Sorted()
	if len(entries) == 0 {
		fmt.Println("No known hosts. Run `cc-clip connect <host>` or `cc-clip setup <host>` first.")
		return
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "HOST\tVERSION\tCODEX\tLAST CONNECTED")
	for _, e := range entries {
		version := e.LastDeployedVersion
		if version == "" {
			version = "-"
		}
		codex := "no"
		if e.Codex {
			codex = "yes"
		}
		when := "-"
		if !e.LastConnected.IsZero() {
			when = e.LastConnected.Local().Format(time.RFC3339)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", e.Host, version, codex, when)
	}
	_ = tw.Flush()
}

func hostsForget(args []string) {
	fs := flag.NewFlagSet("hosts forget", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: cc-clip hosts forget <host>")
	}
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	if fs.NArg() != 1 {
		fs.Usage()
		os.Exit(2)
	}
	host := fs.Arg(0)

	reg, err := hosts.Load()
	if err != nil {
		log.Fatalf("failed to load host registry: %v", err)
	}
	if !reg.Forget(host) {
		fmt.Fprintf(os.Stderr, "host %q is not in the registry.\n", host)
		os.Exit(1)
	}
	if err := reg.Save(); err != nil {
		log.Fatalf("failed to save host registry: %v", err)
	}
	fmt.Printf("Forgot %s. The remote itself was not touched; the cc-clip shim is still installed there.\n", host)
}

// recordHostConnect is called by cmdConnect / cmdSetup AFTER every other step
// in the connect pipeline has returned without log.Fatal. We only record the
// host here because a failure earlier in the pipeline (bad token, SSH dead,
// remote rejected the binary, etc.) should not be remembered as a successful
// connect.
//
// The version argument is main.version from ldflags. If that value is not a
// real release tag (the normalizeVersion rules catch "dev" and git-describe
// builds), we pass "" so the registry keeps whatever version was recorded on
// the last real release.
func recordHostConnect(host, version string, codex bool) {
	if host == "" {
		return
	}
	reg, err := hosts.Load()
	if err != nil {
		log.Printf("warning: could not load host registry to record %s: %v", host, err)
		return
	}
	reg.UpsertConnect(host, version, codex)
	if err := reg.Save(); err != nil {
		log.Printf("warning: could not save host registry after recording %s: %v", host, err)
	}
}

// clearHostCodex is called after a successful `uninstall --codex --host <host>`
// to flip the sticky codex flag back off.
func clearHostCodex(host string) {
	if host == "" {
		return
	}
	reg, err := hosts.Load()
	if err != nil {
		log.Printf("warning: could not load host registry to clear codex on %s: %v", host, err)
		return
	}
	if !reg.ClearCodex(host) {
		return
	}
	if err := reg.Save(); err != nil {
		log.Printf("warning: could not save host registry after clearing codex on %s: %v", host, err)
	}
}

// registryVersionOrEmpty returns main.version if it is a clean release tag,
// otherwise "". Used by recordHostConnect to avoid polluting the registry
// with "dev" or "v0.6.2-1-gabc123-dirty" style ldflags values.
func registryVersionOrEmpty() string {
	return normalizeVersion(version)
}

// printPerHostRedeployReminders prints one `cc-clip connect ...` line per
// known host. Returns false (and writes nothing) on registry IO error or
// empty registry so the caller can fall back to the generic reminder. Never
// fails the update — the redeploy reminder is best-effort guidance.
func printPerHostRedeployReminders() bool {
	reg, err := hosts.Load()
	if err != nil {
		return false
	}
	return reg.FormatRedeployReminder(os.Stdout)
}
