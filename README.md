# herdr-pasture

A [herdr](https://herdr.dev) plugin that docks a narrow pane on the left edge of
every tab listing your running agent panes, grouped by git repository. Click a
row to jump to that agent; click a group header to collapse it.

```
 herdr-pasture
›▾ global-sourcing-tool
   ◐ watchdog techcrunch ingestion…
   ○ Notion-research plist設定
 ▾ polala
   ✓ [feat/email] email feature #206
 ▸ herdr-pasture
```

- `◐` working, `○` idle, `✓` done, `●` blocked, `?` unknown.
- The focused agent's row is drawn in reverse video.
- Worktrees are folded into their main repository and show their branch as
  `[branch]` before the title.
- Panes without an agent are not listed individually, and neither are pasture's
  own panes.
- A workspace with no agent running in it gets one `◦` row so it stays
  reachable; clicking it switches to that workspace.
- The title bar shows `disconnected` when `herdr api snapshot` is failing; the
  last known list stays on screen until herdr answers again.

## Install

```bash
herdr plugin install ayumu-1212/herdr-pasture
```

Requires herdr >= 0.8.0 and Go >= 1.27 (the build step compiles the binary).
macOS and Linux.

### Updating

herdr 0.8.0 has no `plugin update`, so reinstall:

```bash
herdr plugin uninstall herdr-pasture
herdr plugin install ayumu-1212/herdr-pasture
herdr plugin action invoke herdr-pasture.redeploy
```

`uninstall` removes the plugin's code, not its config: your `config.toml` lives
in the herdr-managed config directory and is kept. The `redeploy` at the end
matters — panes opened by the old version keep running the old binary until they
are closed, and redeploy closes them all so the next focus event respawns them.

Check what you have with `herdr plugin list`, which reports the manifest's
version. `herdr plugin install --ref <tag>` pins a specific release.

### Local development

```bash
git clone https://github.com/ayumu-1212/herdr-pasture
cd herdr-pasture
sh scripts/build.sh          # -> bin/pasture
herdr plugin link "$PWD"
```

`scripts/build.sh` is what the manifest's `[[build]]` step runs, so a linked
checkout and an installed plugin behave identically. After rebuilding, run the
`redeploy` action so the panes respawn on the new binary.

## Usage

The pane appears automatically when a tab or workspace is created, and when a
tab or pane is focused. Two actions are exposed:

- `herdr-pasture.toggle` — close the pane in the current tab (it stays closed,
  "snoozed", until you toggle again) or open it. This works even with
  `auto_open = false`.
- `herdr-pasture.redeploy` — close every pasture pane and clear every snooze so
  they respawn on the latest build.

Bind the toggle in `~/.config/herdr/config.toml`:

```toml
[[keys.command]]
key = "prefix+g"
type = "shell"
command = "herdr plugin action invoke herdr-pasture.toggle"
description = "toggle pasture"
```

Both actions exit non-zero when the work did not happen — herdr unreachable, or
another pasture process still holding the tab's lock while it starts a pane. The
message on stderr names the busy tabs; the fix is to invoke the action again a
moment later. Automatic docking (`ensure`) runs from an event hook, so it always
exits 0: expected conditions (auto_open off, snoozed tab, contended lock) pass
silently, and a real failure prints the reason on stderr and leaves the retry to
the next event rather than filling the plugin log with failed commands.

### Keys inside the pane

| Key | Action |
| --- | --- |
| `↑` / `k`, `↓` / `j` | move the cursor |
| `Enter` | focus the selected agent, or collapse/expand a group header |
| `Space` | collapse/expand the selected group header |
| `r` | refresh now |
| `q` / `Ctrl-C` | quit the pane |
| left click | focus that agent, or collapse/expand that header |
| wheel | scroll the list |

## Configuration

`$(herdr plugin config-dir herdr-pasture)/config.toml` — the file is optional
and so is every key:

```toml
auto_open = true          # false: never auto-dock; use the toggle action
width_ratio = 0.25        # share of the tab width (0.1–0.5)
width_columns = 0         # >0: fix the dock at this many columns, ignoring width_ratio
poll_interval_ms = 1000   # how often to read `herdr api snapshot` (min 100)
exclude = ["~/tmp/**"]    # hide panes whose cwd matches
```

`width_columns` wins over `width_ratio` when it is 1 or more. The dock is
re-measured on every focus event and nudged back to that column count, so it
keeps its width when the terminal is resized. It is clamped to at least 22
columns and at most half the tab.

`exclude` patterns expand a leading `~` and `$VARS`. A trailing `/**` matches the
directory and everything under it; anything else is a `filepath.Match` glob
against the whole cwd. Out-of-range or unparseable values fall back to the
defaults above and print a warning on stderr rather than failing the command.

Outside herdr (no `HERDR_PLUGIN_CONFIG_DIR`) the config is read from
`~/.config/herdr-pasture/config.toml`.

## Replacing the herdr sidebar

Every workspace appears in the list, so pasture can stand in for the standard
sidebar. Turn that off in `~/.config/herdr/config.toml`:

    [ui]
    sidebar_start_collapsed = true
    sidebar_collapsed_mode = "hidden"

`hidden` gives the collapsed sidebar zero width, so the pasture pane becomes the
leftmost column. The change takes effect on the next herdr launch.

## Known limitations

- Snooze state is keyed by tab id and shared across named herdr sessions, so
  two sessions that reuse a tab id share its snooze.
- If the leftmost column of a tab is stacked, the pane takes the tallest pane's
  height rather than the full tab height.
- If the tab layout is unavailable at the moment an event fires, that tab is
  left undocked until the next event rather than docked in the wrong place.
- Running `pasture toggle` by hand outside herdr uses its own state directory
  (`~/.local/state/herdr-pasture`), so it does not see snoozes set through the
  plugin action.
- Repository lookups are cached for the life of the pane process; moving or
  re-pointing a worktree is picked up after a `redeploy`.
- A pane manually renamed `pasture` is adopted (and closed) as one of ours.
- With `width_columns` set, a width you change by hand is reset to the target on
  the next focus event.
- A herdr server restart (`herdr update` forces one) leaves each dock as an empty
  shell, because the pane comes back but the process in it does not. The
  `[[startup]]` hook runs the UI in those panes again straight away, so the dock
  is back before you touch anything.

## Tested with

herdr 0.8.0 on macOS (Darwin 25.4, arm64), Go 1.27.1. Verified end to end in an
isolated `herdr --session pasture-dev`: docking at the left edge of a tab at the
configured share of its width, grouping by repository across workspaces,
collapsing a group, focusing an agent in another workspace, toggle closing and
snoozing a tab, `ensure` skipping a snoozed tab, `ensure` being idempotent,
reaping a pane whose token is gone and replacing it, and `redeploy` clearing
every pane.

v0.2 was verified the same way: a workspace with no agent showing as a `◦` row
under its own directory's group, pressing Enter on that row switching to the
workspace, `width_columns = 30` clamping to 27 on a 54-column tab (half the
tab), and a dock widened by hand to 38 columns snapping back to 27 on the next
`ensure`.

Also verified against herdr **0.9.0**, using its release binary against a copy
of a real 0.8.0 config: the plugin stays registered across the version change,
the event hooks fire, docking and the fixed width behave the same, and a server
restart, which leaves every dock as an empty shell and fires no event at all, is
recovered in place by the `[[startup]]` hook.
