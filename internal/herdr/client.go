package herdr

import (
	"fmt"
	"sort"
	"strconv"

	"github.com/ayumu-1212/herdr-pasture/internal/snapshot"
)

// Client exposes the handful of herdr commands pasture needs.
type Client struct {
	r Runner
}

// New wraps a Runner.
func New(r Runner) *Client {
	return &Client{r: r}
}

// Snapshot runs `herdr api snapshot`.
func (c *Client) Snapshot() (snapshot.Snapshot, error) {
	out, err := c.r.Run("api", "snapshot")
	if err != nil {
		return snapshot.Snapshot{}, err
	}
	return snapshot.Decode(out)
}

// PaneList runs `herdr pane list` across all workspaces.
func (c *Client) PaneList() ([]snapshot.Pane, error) {
	out, err := c.r.Run("pane", "list")
	if err != nil {
		return nil, err
	}
	return snapshot.DecodePaneList(out)
}

// Layout runs `herdr pane layout --pane <id>` for the tab containing paneID.
func (c *Client) Layout(paneID string) (snapshot.Layout, error) {
	out, err := c.r.Run("pane", "layout", "--pane", paneID)
	if err != nil {
		return snapshot.Layout{}, err
	}
	return snapshot.DecodeLayout(out)
}

// FocusAgent focuses the agent pane paneID, switching workspace and tab as needed.
func (c *Client) FocusAgent(paneID string) error {
	_, err := c.r.Run("agent", "focus", paneID)
	return err
}

// Split splits paneID to the right without focusing the new pane. ratio is the
// share kept by paneID. env is passed as --env KEY=VALUE in sorted key order.
// It returns the new pane id.
func (c *Client) Split(paneID string, ratio float64, cwd string, env map[string]string) (string, error) {
	args := []string{"pane", "split", paneID, "--direction", "right",
		"--ratio", strconv.FormatFloat(ratio, 'f', 2, 64), "--no-focus"}
	if cwd != "" {
		args = append(args, "--cwd", cwd)
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "--env", k+"="+env[k])
	}
	out, err := c.r.Run(args...)
	if err != nil {
		return "", err
	}
	return snapshot.DecodeSplit(out)
}

// Swap exchanges the positions of two panes.
func (c *Client) Swap(source, target string) error {
	_, err := c.r.Run("pane", "swap", "--source-pane", source, "--target-pane", target)
	return err
}

// RunInPane types command + Enter into paneID's shell.
func (c *Client) RunInPane(paneID, command string) error {
	_, err := c.r.Run("pane", "run", paneID, command)
	return err
}

// Rename sets the pane label shown in herdr's UI and `pane list`.
func (c *Client) Rename(paneID, label string) error {
	_, err := c.r.Run("pane", "rename", paneID, label)
	return err
}

// Close closes a pane.
func (c *Client) Close(paneID string) error {
	_, err := c.r.Run("pane", "close", paneID)
	return err
}

// ReportToken stamps a display-metadata token on paneID under source "pasture".
func (c *Client) ReportToken(paneID, name, value string) error {
	_, err := c.r.Run("pane", "report-metadata", paneID, "--source", "pasture",
		"--token", fmt.Sprintf("%s=%s", name, value))
	return err
}
