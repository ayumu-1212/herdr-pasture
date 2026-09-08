// Command pasture is the herdr-pasture plugin binary.
//
//	pasture ui        run the docked TUI (inside a herdr pane)
//	pasture ensure    make sure the current tab has a pasture pane (event hook)
//	pasture toggle    open/close the pasture pane in the current tab (action)
//	pasture redeploy  close every pasture pane so they respawn on the next focus
//	pasture version   print the plugin version
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ayumu-1212/herdr-pasture/internal/config"
	"github.com/ayumu-1212/herdr-pasture/internal/dock"
	"github.com/ayumu-1212/herdr-pasture/internal/group"
	"github.com/ayumu-1212/herdr-pasture/internal/herdr"
	"github.com/ayumu-1212/herdr-pasture/internal/ui"
)

const version = "0.1.0"

const usage = "usage: pasture <ui|ensure|toggle|redeploy|version>"

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	// version and help answer without touching the config file, so a broken
	// config can never stop a user from finding out which build they have.
	switch os.Args[1] {
	case "version":
		fmt.Println("pasture", version)
		return
	case "-h", "--help", "help":
		fmt.Println(usage)
		return
	}

	cfg, warnings := config.Load(filepath.Join(config.Dir(), "config.toml"))
	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, "pasture:", w)
	}
	client := herdr.New(herdr.NewExecRunner())

	var err error
	switch os.Args[1] {
	case "ui":
		err = runUI(cfg, client)
	case "ensure":
		err = dock.Ensure(deps(cfg, client), os.Getenv("HERDR_TAB_ID"))
	case "toggle":
		err = dock.Toggle(deps(cfg, client), os.Getenv("HERDR_TAB_ID"))
	case "redeploy":
		err = dock.Redeploy(deps(cfg, client))
	default:
		fmt.Fprintf(os.Stderr, "pasture: unknown subcommand %q\n%s\n", os.Args[1], usage)
		os.Exit(2)
	}
	if err != nil {
		// Every error reaching here means the requested work did not happen:
		// herdr was unreachable, or (for toggle/redeploy) another pasture
		// process held the tab's lock. dock's messages already name the busy
		// tabs, and the recovery is simply to invoke the action again in a
		// moment. Both cases exit 1 so herdr reports the action as failed
		// rather than silently doing nothing; dock.Ensure deliberately returns
		// nil for its own expected conditions (auto_open off, snoozed, locked)
		// so ordinary event hooks stay quiet.
		fmt.Fprintln(os.Stderr, "pasture:", err)
		os.Exit(1)
	}
}

// stateDir is where locks and snooze markers live. herdr sets
// HERDR_PLUGIN_STATE_DIR for plugin commands, and dock forwards it into the
// pane it spawns; the fallback keeps the binary usable when run by hand.
func stateDir() string {
	if d := os.Getenv("HERDR_PLUGIN_STATE_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	return filepath.Join(home, ".local", "state", "herdr-pasture")
}

func deps(cfg config.Config, client *herdr.Client) dock.Deps {
	bin, err := os.Executable()
	if err != nil {
		bin = os.Args[0]
	}
	return dock.Deps{
		Client:   client,
		StateDir: stateDir(),
		Bin:      bin,
		Cfg:      cfg,
		Now:      time.Now,
		Sleep:    time.Sleep,
		Log:      os.Stderr,
	}
}

// teaModel adapts ui.Model, whose Update returns the concrete Model, to
// tea.Model. Composition rather than embedding, so nothing of ui.Model is
// promoted onto the adapter by accident.
type teaModel struct{ m ui.Model }

func (t teaModel) Init() tea.Cmd { return t.m.Init() }

func (t teaModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := t.m.Update(msg)
	return teaModel{next}, cmd
}

func (t teaModel) View() string { return t.m.View() }

// runUI starts the TUI. A pane started by `herdr pane run` only inherits
// HERDR_PANE_ID / HERDR_TAB_ID / HERDR_WORKSPACE_ID / HERDR_ENV, so the plugin
// dirs arrive via the --env flags dock passes to `pane split`, and
// HERDR_BIN_PATH is absent: herdr.NewExecRunner then falls back to "herdr" on
// PATH, which is how a pane reaches herdr anyway. config.Dir and stateDir have
// their own fallbacks, so `pasture ui` still runs with none of them set.
func runUI(cfg config.Config, client *herdr.Client) error {
	// Stamp the token that tells dock this pane is alive. A pane carrying
	// dock.Label but no token is a corpse dock will reap, so failing to stamp
	// is worth reporting, but it must not stop the UI from running.
	if paneID := os.Getenv("HERDR_PANE_ID"); paneID != "" {
		if err := client.ReportToken(paneID, dock.Token, strconv.Itoa(os.Getpid())); err != nil {
			fmt.Fprintln(os.Stderr, "pasture: report token:", err)
		}
	}
	opt := group.Options{SelfToken: dock.Token, Exclude: cfg.Excluded}
	model := ui.New(client, group.NewGitResolver(), opt, time.Duration(cfg.PollIntervalMs)*time.Millisecond)
	_, err := tea.NewProgram(teaModel{model}, tea.WithMouseCellMotion()).Run()
	return err
}
