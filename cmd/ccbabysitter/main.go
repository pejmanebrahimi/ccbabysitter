// Command ccbabysitter shows every Claude Code session on this machine on
// a local web page and keeps chosen sessions alive.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"

	"ccbabysitter.dev/ccbabysitter/internal/buildinfo"
	"ccbabysitter.dev/ccbabysitter/internal/hosts"
	"ccbabysitter.dev/ccbabysitter/internal/state"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

// runCommands are the one-shot commands that are not control commands.
var runCommands = map[string]bool{"version": true, "install": true, "uninstall": true, "reset": true}

// run is the whole program's testable entry point: it parses the command
// line and either runs a one-shot subcommand or starts serving the
// page until it is told to stop.
func run(args []string) int {
	if len(args) > 0 {
		switch {
		case args[0] == "help" || args[0] == "--help" || args[0] == "-help" || args[0] == "-h":
			return runHelp(args[1:])
		case isControlCommand(args[0]):
			return runControl(args[0], args[1:], realControlEnv())
		}
	}
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		// Asking for a command's page never runs it.
		if runCommands[args[0]] && wantsHelp(args[1:]) {
			return runHelp(args[:1])
		}
		switch args[0] {
		case "version":
			return runVersion(args[1:])
		case "install", "uninstall", "reset":
			// These take no arguments: a stray word is a usage error, and
			// nothing is installed, removed or deleted.
			if len(args) > 1 {
				printUsage(os.Stderr)
				return 2
			}
			switch args[0] {
			case "install":
				return runInstall(os.Stdout)
			case "uninstall":
				return runUninstall(os.Stdout)
			}
			return runReset()
		default:
			printUsage(os.Stderr)
			return 2
		}
	}

	// A run the system's service manager started is the background copy,
	// whatever its flags: it serves, prints no key and opens no browser.
	system := startedByServiceManager()
	opts, err := parseServeFlags(args, stdoutToJournal() || system)
	if err != nil {
		printUsage(os.Stderr)
		return 2
	}
	// A plain run hands CC Babysitter to the system's service manager and
	// gives the terminal back, where there is one to hand it to. Elsewhere,
	// and with --foreground, it serves in this terminal.
	if launches(opts, system) {
		if sc := newServiceControl(); sc != nil {
			if opts.PortSet {
				fmt.Fprintln(os.Stderr, "--port only applies with --foreground, since the background copy picks its own port.")
				return 2
			}
			headless := hosts.Headless()
			o := launchOptions{Headless: headless, Server: runtime.GOOS == "linux" && headless, NoOpen: opts.NoOpen}
			if rc, foreground := startInBackground(os.Stdout, sc, state.DefaultDir(), o, realLaunchDeps()); !foreground {
				return rc
			}
		}
	}
	return runServe(opts)
}

// runVersion prints the name and the version, or with --json one JSON
// document. Anything else after version is a usage error.
func runVersion(args []string) int {
	switch {
	case len(args) == 0:
		fmt.Printf("%s %s\n", buildinfo.Name, buildinfo.Version)
		return 0
	case len(args) == 1 && (args[0] == "--json" || args[0] == "-json"):
		doc, err := json.Marshal(struct {
			Schema  int    `json:"schema"`
			Name    string `json:"name"`
			Version string `json:"version"`
		}{1, buildinfo.Name, buildinfo.Version})
		if err != nil {
			return 1
		}
		fmt.Printf("%s\n", doc)
		return 0
	}
	printUsage(os.Stderr)
	return 2
}

// runHelp prints the usage, or the page of the command it is asked about.
func runHelp(args []string) int {
	if len(args) == 0 {
		printUsage(os.Stdout)
		return 0
	}
	if len(args) == 1 && printCommandHelp(os.Stdout, args[0]) {
		return 0
	}
	printUsage(os.Stderr)
	return 2
}

// parseServeFlags reads the flags of the default command. --service is
// what the systemd unit passes: serve in the foreground and never open a
// browser. It is left out of the usage, since nobody needs to type it.
// journal is whether this process's output goes to the systemd journal: a
// unit written by an older build runs --no-open, or nothing, rather than
// --service, and its output goes to the journal all the same, so it is
// service output too, never carries the key and opens no browser.
func parseServeFlags(args []string, journal bool) (serveOptions, error) {
	fs := flag.NewFlagSet("ccbabysitter", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	noOpen := fs.Bool("no-open", false, "do not open a browser")
	foreground := fs.Bool("foreground", false, "serve in this terminal")
	service := fs.Bool("service", false, "run as the systemd user service")
	demo := fs.Bool("demo", false, "scripted sessions that touch nothing real")
	port := fs.Int("port", defaultPort, "preferred port to serve the page on")
	if err := fs.Parse(args); err != nil {
		return serveOptions{}, err
	}
	if fs.NArg() > 0 {
		return serveOptions{}, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	portSet := false
	fs.Visit(func(f *flag.Flag) { portSet = portSet || f.Name == "port" })
	asService := *service || journal
	return serveOptions{NoOpen: *noOpen || asService, Service: asService, Demo: *demo, Port: *port, Foreground: *foreground, PortSet: portSet}, nil
}

// runServe starts serve and stops it on SIGINT or SIGTERM: the first
// signal cancels the root context so everything shuts down cleanly, and a
// second signal, arriving before that finishes, exits immediately instead
// of waiting any longer.
func runServe(opts serveOptions) int {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	go func() {
		if _, ok := <-sigCh; !ok {
			return
		}
		cancel()
		if _, ok := <-sigCh; ok {
			os.Exit(1)
		}
	}()

	return serve(ctx, opts, os.Stdout)
}
