package main

import (
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

const version = "0.1.0"

const usage = `oshiete — anonymous pageview tracking, in a terminal

usage:
  oshiete            open the dashboard; it syncs in the background
  oshiete sync       pull new events into the local mirror and exit
  oshiete sites      list the sites the mirror knows about
  oshiete version

environment:
  OSHIETE_ENDPOINT    collector base URL  (default https://o.5seg.top)
  OSHIETE_TOKEN       sync token          (default: ~/oshiete/data/token)
  OSHIETE_DB          mirror path         (default: ~/oshiete/data/mirror.db)

keys:
  1-5 views   tab next   t period   s site   / filter   enter drill   r resync   q quit
`

func main() {
	cmd := "tui"
	rest := os.Args[1:]
	if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		cmd, rest = rest[0], rest[1:]
	}
	if len(rest) > 0 {
		fmt.Fprintf(os.Stderr, "unexpected argument %q\n\n%s", rest[0], usage)
		os.Exit(2)
	}

	switch cmd {
	case "help", "-h", "--help":
		fmt.Print(usage)
		return
	case "version", "-v", "--version":
		fmt.Printf("oshiete %s\n", version)
		return
	}

	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "oshiete:", err)
		os.Exit(1)
	}

	store, err := OpenStore(cfg.DBPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "oshiete:", err)
		os.Exit(1)
	}
	defer store.Close()

	sy := NewSyncer(cfg)

	switch cmd {
	case "tui":
		m, err := newModel(cfg, store, sy)
		if err != nil {
			fmt.Fprintln(os.Stderr, "oshiete:", err)
			os.Exit(1)
		}
		if _, err := tea.NewProgram(m, tea.WithAltScreen()).Run(); err != nil {
			fmt.Fprintln(os.Stderr, "oshiete:", err)
			os.Exit(1)
		}
	case "sync":
		if err := runSync(sy, store); err != nil {
			fmt.Fprintln(os.Stderr, "oshiete:", err)
			os.Exit(1)
		}
	case "sites":
		sites, err := store.Sites()
		if err != nil {
			fmt.Fprintln(os.Stderr, "oshiete:", err)
			os.Exit(1)
		}
		if len(sites) == 0 {
			fmt.Println("mirror is empty — run `oshiete sync`")
			return
		}
		for _, s := range sites {
			fmt.Println(s)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
}
