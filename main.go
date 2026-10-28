package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"

	tea "charm.land/bubbletea/v2"
	"github.com/arg0net/pass-dups/internal/demo"
	"github.com/arg0net/pass-dups/internal/passcli"
	"github.com/arg0net/pass-dups/internal/tui"
)

func main() {
	offline := flag.Bool("demo", false, "use synthetic, in-memory data; never run pass-cli")
	binary := flag.String("pass-cli", "pass-cli", "path to the Proton Pass CLI")
	allURLs := flag.Bool("all-urls", false, "also match saved URLs (reads details of every Active and Trashed login)")
	flag.Parse()
	var runner passcli.Runner
	if *offline {
		runner = demo.New()
	} else {
		path, err := exec.LookPath(*binary)
		if err != nil {
			fmt.Fprintln(os.Stderr, "pass-cli not found. Install it or use --demo.")
			os.Exit(1)
		}
		runner = passcli.ExecRunner{Binary: path}
	}
	if _, err := tea.NewProgram(tui.New(passcli.Client{Runner: runner}, *offline, *allURLs)).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "TUI could not run; check that an interactive terminal is available.")
		os.Exit(1)
	}
}
