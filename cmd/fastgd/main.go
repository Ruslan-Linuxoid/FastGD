package main

import (
	"flag"
	"fmt"
	"maps"
	"os"
	"runtime/debug"
	"slices"
	"strings"

	"github.com/Ruslan-Linuxoid/FastGD/internal/config"
)

type command struct {
	summary string
	run     func(args []string) error

	hidden bool
}

func commands() map[string]command {
	return map[string]command{
		"auth": {summary: "sign in with the Google account in a browser and write the token file", run: authCmd},
		"mount": {summary: "bind folders of My Drive to places elsewhere, list and remove the bindings",
			run: mountCmd},
		helperCommand: {run: helperCmd, hidden: true},
		"run":         {summary: "mount My Drive and serve it until SIGTERM", run: runDaemon},
		"status": {summary: "show dirty files, failing uploads, cache use and the use of Drive's quota",
			run: statusCmd},
		"sync": {summary: "make the running daemon send everything that waits for Drive now", run: syncCmd},
	}
}

func usage() string {
	cmds := commands()
	var b strings.Builder
	b.WriteString("usage: fastgd <command> [flags]\n       fastgd --version\n\ncommands:\n")
	for _, name := range slices.Sorted(maps.Keys(cmds)) {
		if cmds[name].hidden {
			continue
		}
		fmt.Fprintf(&b, "  %-8s %s\n", name, cmds[name].summary)
	}
	return b.String()
}

const exitUsage = 2

var version = "dev"

func buildVersion() string {
	bi, _ := debug.ReadBuildInfo()
	return versionOf(version, bi)
}

func versionOf(linked string, bi *debug.BuildInfo) string {
	if linked != "dev" || bi == nil || bi.Main.Version == "" || bi.Main.Version == "(devel)" {
		return linked
	}
	for _, s := range bi.Settings {
		if s.Key == "vcs.revision" {
			return linked
		}
	}
	return bi.Main.Version
}

func main() {
	if len(os.Args) == 2 && (os.Args[1] == "--version" || os.Args[1] == "version") {
		fmt.Fprintln(os.Stdout, "fastgd", buildVersion())
		return
	}
	if len(os.Args) <= 1 {
		fmt.Fprint(os.Stderr, usage())
		os.Exit(exitUsage)
	}
	cmd, ok := commands()[os.Args[1]]
	if !ok {
		fmt.Fprintf(os.Stderr, "fastgd: unknown command %q\n\n%s", os.Args[1], usage())
		os.Exit(exitUsage)
	}
	if err := cmd.run(os.Args[2:]); err != nil {
		fmt.Fprintf(os.Stderr, "fastgd %s: %v\n", os.Args[1], err)
		os.Exit(1)
	}
}

func loadConfig(name string, args []string) (*config.Config, error) {
	cfg, _, err := loadConfigArgs(name, args, nil)
	return cfg, err
}

func loadConfigArgs(name string, args []string, more func(*flag.FlagSet)) (*config.Config, []string, error) {
	fl := flag.NewFlagSet(name, flag.ContinueOnError)
	cfgPath := fl.String("config", defaultConfig, "config file")
	if more != nil {
		more(fl)
	}
	if err := fl.Parse(args); err != nil {
		return nil, nil, err
	}
	cfg, err := config.Load(*cfgPath)
	return cfg, fl.Args(), err
}
