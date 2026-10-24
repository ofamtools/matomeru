package daemon

import (
	"context"
	"errors"
	"fmt"
)

// MinimumLaunchRows leaves room for a prompt, output, and editor chrome. A split
// needs twice this height plus its border; unknown/detached rectangles are not
// evidence that an occupied workspace can safely accept another pane.
const MinimumLaunchRows = 18

// LaunchWorkspace creates only a missing session, never a placeholder window.
// All new terminal, agent and Vim panes choose placement before starting their
// process. It never changes layout or moves an existing window.
func LaunchWorkspace(ctx context.Context, c interface {
	Call(context.Context, string, any, any) error
}, session string, cols, rows int) (int, error) {
	if rows > 0 && rows < MinimumLaunchRows {
		return 0, fmt.Errorf("new pane requested %d rows; at least %d rows are needed for a usable terminal, agent or Vim pane", rows, MinimumLaunchRows)
	}
	if host, _, raw := SplitIdentity(session); host != "" {
		ctx, session = WithHost(ctx, host), raw
	}
	var info struct {
		Current  int    `json:"current_workspace"`
		Attached bool   `json:"tui_attached"`
		Layout   string `json:"layout_mode"`
		Tiling   string `json:"tiling_mode"`
		Height   int    `json:"height"`
	}
	params := map[string]any{"session": session}
	err := c.Call(ctx, "session-info", params, &info)
	var remote *Error
	if errors.As(err, &remote) && remote.Code == "session_not_found" {
		create := map[string]any{"name": session, "window": false}
		if cols > 0 && rows > 0 {
			create["width"], create["height"] = cols, rows
		}
		if err = c.Call(ctx, "new-session", create, nil); err == nil {
			return 1, nil
		} else if !errors.As(err, &remote) || remote.Code != "session_exists" {
			return 0, err
		}
		err = c.Call(ctx, "session-info", params, &info)
	}
	if err != nil {
		return 0, err
	}
	if info.Height > 0 && info.Height < MinimumLaunchRows+2 {
		return 0, fmt.Errorf("session %q is only %d rows high; a usable new pane needs at least %d rows plus borders", session, info.Height, MinimumLaunchRows)
	}
	var windows struct {
		Windows []Pane `json:"windows"`
	}
	if err := c.Call(ctx, "list-windows", params, &windows); err != nil {
		return 0, err
	}
	current := info.Current
	if current < 1 {
		current = 1
	}
	occupied := false
	safe := info.Attached && info.Tiling == "tiling" && (info.Layout == "bsp" || info.Layout == "master-stack")
	for _, pane := range windows.Windows {
		if pane.Workspace != current || pane.Minimized {
			continue
		}
		occupied = true
		if pane.Height < 2*MinimumLaunchRows+2 {
			safe = false
		}
	}
	if !occupied || safe {
		return current, nil
	}
	var spaces struct {
		Workspaces []struct {
			Number int `json:"workspace"`
			Count  int `json:"window_count"`
		} `json:"workspaces"`
	}
	if err := c.Call(ctx, "list-workspaces", params, &spaces); err != nil {
		return 0, err
	}
	for _, space := range spaces.Workspaces {
		if space.Number > 0 && space.Count == 0 {
			return space.Number, nil
		}
	}
	return 0, fmt.Errorf("session %q has no empty workspace for a usable new pane (minimum %d rows); free a workspace or choose another session", session, MinimumLaunchRows)
}
