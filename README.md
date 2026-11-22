# herdr-pasture

A [herdr](https://herdr.dev) plugin that docks a narrow pane on the left edge of
every tab listing your running agent panes, grouped by git repository. Click a
row to jump to that agent; click a group header to collapse it (the fold state is shared across workspaces).

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
  reachable; clicking it switches to that workspace. When that row is all a
  group holds, its header goes to the same place instead of collapsing, and
  loses its chevron to say so.
- A status mark herdr already put at the front of a pane's title is dropped, so
  the row shows one icon rather than two.
- The title bar shows `disconnected` when `herdr api snapshot` is failing; the
  last known list stays on screen until herdr answers again.
- On a phone the dock becomes a sidebar that opens and closes; see
  [On a phone](#on-a-phone).

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
are closed, and redeploy closes each one and builds its replacement straight
away, so when the action returns every dock is on the new build. Nothing else to
press.

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
tab or pane is focused **over the API** — see the note on focus events under
Known limitations, because moving around inside the herdr TUI is not that. Two
actions are exposed:

- `herdr-pasture.toggle` — close the pane in the current tab (it stays closed,
  "snoozed", until you toggle again) or open it. This works even with
  `auto_open = false`. On a phone it opens and closes the sidebar instead; see
  [On a phone](#on-a-phone).
- `herdr-pasture.redeploy` — clear every snooze, then close each pasture pane and
  build its replacement on the current binary. It leaves the focus in the tab you
  were in.

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

### On a phone

herdr keeps a tab's splits on a narrow screen, so on a phone the dock would take
a third of the width away from the agent. When the terminal is `mobile_width`
columns or narrower (64 by default, the same as herdr's own
`ui.mobile_width_threshold`), pasture works as a sidebar instead:

```
 open                          closed
┌ pasture ─────────────────┐  ┌ … ┐┌──────────────────────┐
│herdr-pasture             │  │›  ││ claude …             │
│›▾ polala                 │  │   ││                      │
│   ✓ [feat/email] email…  │  │   ││                      │
│   + worktree             │  │   ││                      │
└──────────────────────────┘  └───┘└──────────────────────┘
```

- **Open**: the dock is zoomed to the full screen. It opens by itself when you
  arrive on a tab pasture has not closed yet — attaching from the phone, say.
- **Closed**: the dock shrinks to a strip on the left edge, herdr's narrowest
  split (5 columns of a 45-column phone), and the pane you went to fills the
  rest. **Tap the strip anywhere to open the sidebar again.** A tab you closed
  stays closed when you come back to it, through herdr's own switch menu too, so
  the strip is what you see there.
- **Pick a row**: pasture closes the sidebar of the tab that row leads to onto
  that pane, then moves there. A workspace row goes to the focused pane of that
  workspace's active tab (never its dock).
- **`+ worktree`**: the last row of each git repository's group, shown on a phone
  only. It runs `herdr worktree create` in that repository, with the branch name
  herdr makes up (`worktree/brave-harbor-0760`), and takes you to the new
  worktree's shell with the sidebar closed. If herdr refuses, the title bar says
  `worktree failed` for a few seconds. On the desktop, herdr's own
  `prefix+shift+g` does the same.
- **`herdr-pasture.toggle`** (the `prefix+g` binding above) opens the sidebar, and
  pressed again with it open, closes it back onto the pane you came from. On a
  phone the toggle never closes the dock, so reopening is instant.

Zoom and split ratios are kept by the herdr server for every client, so both
would still be there on the desktop. pasture remembers which tabs it took over,
and once the session is looked at from a wider terminal again each dock gives
its own tab back: unzoomed, with the dock at `width_columns` (or the width it
had before the phone, without that setting). Tabs pasture never touched are left
alone, and so is a zoom you made yourself. If you unzoom an open sidebar by hand
on the phone, pasture does not zoom it again; the dock goes back to the strip.

`width_columns` is not reapplied while the terminal is phone-sized: herdr keeps
one split ratio for every client, so fitting 50 columns into a 45-column phone
would leave the desktop with a dock half the tab wide.

Activating an agent row focuses the pane's tab before the pane itself. herdr 0.9
moves the client that is looking at the session only for a fixed set of
navigation requests, and focusing an agent is not one of them: on its own it
moves the server's focus while the client keeps looking at the workspace it was
already on, so a pane in another workspace appeared to do nothing.

## Configuration

`$(herdr plugin config-dir herdr-pasture)/config.toml` — the file is optional
and so is every key:

```toml
auto_open = true          # false: never auto-dock; use the toggle action
width_ratio = 0.25        # share of the tab width (0.1–0.5)
width_columns = 0         # >0: fix the dock at this many columns, ignoring width_ratio
poll_interval_ms = 1000   # how often to read `herdr api snapshot` (min 100)
exclude = ["~/tmp/**"]    # hide panes whose cwd matches
mobile_width = 64         # at or below this many terminal columns, act as a phone sidebar (0: off)
```

`width_columns` wins over `width_ratio` when it is 1 or more. The dock is
re-measured on every focus event and nudged back to that column count, so it
keeps its width when the terminal is resized.

Both settings describe a share of the whole tab. The dock is carved out of the
tab's leftmost pane, so on a tab that is already split it takes a larger share
of that pane to land on the width you asked for. It never takes more than half
the pane it splits, and never renders below 22 columns.

`mobile_width` is compared with the terminal width, not the tab's: when herdr's
own sidebar is showing, a tab starts to the right of it. Set it to the same value
as `ui.mobile_width_threshold` if you changed that.

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

- Snooze state and the phone sidebar's marks are keyed by tab id and shared
  across named herdr sessions, so two sessions that reuse a tab id share them.
- herdr sizes the session to the client that attached most recently, so with a
  desktop and a phone attached at once the session flips between the two sizes,
  and so does pasture's phone behaviour: arriving back on the phone opens the
  sidebar of a tab the desktop had given back.
- On a phone a closed tab is not zoomed, so a tab with several panes besides the
  dock shows all of them side by side next to the strip. Zooming the one you
  want would hide the strip.
- herdr's switch menu (the `switch` at the top right of the phone layout) cannot
  be replaced by a plugin. Use the strip instead.
- A redeploy run from the phone focuses each tab it rebuilds, so every rebuilt
  dock opens its sidebar.
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
- On a tab whose leftmost pane is narrower than twice the target, the dock is
  capped at half that pane rather than the width you asked for.
- A herdr server restart (`herdr update` forces one) leaves each dock as an empty
  shell, because the pane comes back but the process in it does not. The
  `[[startup]]` hook runs the UI in those panes again straight away, so the dock
  is back before you touch anything.
- herdr 0.9 publishes no plugin event for the user moving around its TUI.
  Verified on 0.9.0: switching workspace or tab from the keyboard logs
  `workspace.focus` and `tab.focus` in the server and invokes no hook at all,
  while the same focus issued over the socket API (`herdr tab focus …`) invokes
  both `tab.focused` and `pane.focused`. So `auto_open` really means "on a tab or
  workspace being created, on an API focus, and on the actions" — walking to an
  undocked tab in the TUI will not dock it. `prefix`-bound `toggle` is the answer
  for a one-off; `redeploy` rebuilds the lot and is why it no longer waits for an
  event that may never arrive.
- Docking a tab moves the focus to the pane the dock is carved out of. On the
  tabs this happens on — a tab that has no dock yet, so almost always a fresh
  one with a single pane — that pane is the focused one already and nothing
  moves. On a tab with three panes where the focus is on none of the two being
  swapped, the focus lands on the leftmost pane: `pane swap` focuses its
  `--source-pane`, and herdr 0.9 has no focus-a-pane-by-id command to put it
  back.

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

v0.3 was verified on 0.9.0 too: on a 120-column tab already split in half, the
dock came out 30 columns, a quarter of the tab, where the old arithmetic gave
15; a workspace-only group rendered its header without a chevron; and a
synthetic click on that header moved the focused workspace.

v0.3.4 was verified on 0.9.0 against a live session of seven docked workspaces:
one `redeploy` closed and rebuilt all seven in place — fresh pane ids, every one
of them live — with no focus event in between, where the old behaviour left the
session with no docks at all until something created a tab. The focus stayed on
`w8:p1`, the pane it was on before, and the client's view stayed on w8 rather than
following the last tab rebuilt.

v0.5.0 was verified on 0.9.0 in the same way: closing a sidebar from a row,
the toggle or the strip left a 5-column strip beside a 40-column pane, and
tapping the strip opened it again; a tab closed on the phone was still the strip
when reached through herdr's switch menu; `+ worktree` made a worktree and its
workspace and moved there; a dock built in a tab marked closed came up at the
usual 22 columns and squeezed itself to the strip; and detaching the phone gave
every tab back at 50 columns, unzoomed, with no mark left. herdr stops a split at
a ratio of 0.1, and `agent focus` refuses a pane with no agent, which is why
pasture focuses a shell by zooming and unzooming it.

v0.4.0 was verified on 0.9.0 in an isolated session driven through a 45-column
and a 120-column pty: attaching at 45 columns opened the sidebar full screen;
tapping a workspace row closed it onto that workspace's shell; the toggle opened
and closed it again; detaching the phone unzoomed both tabs pasture had zoomed
and the 50-column dock was back on the desktop; and `ensure` at 45 columns left a
19-column dock alone where `width_columns = 50` would have resized it.

v0.3.3 was verified on 0.9.0 against the live installed plugin: `pane swap`
focuses whichever pane it is handed as `--source-pane` (an unfocused pane named
as the source took the focus; a focused one named as the source kept it after
being moved to the other slot), and with the anchor passed as the source a
freshly created workspace comes up with the dock 51 columns wide on the left,
unfocused, and `focused_pane_id` on the pane to its right — the one an agent is
started in. Before the change that same workspace came up with the dock focused.
