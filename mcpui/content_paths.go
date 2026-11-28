package mcpui

import (
	"strings"

	"ofam.dev/matomeru/data"
)

func lookupContentPane(store *data.Store, id string) *data.Record {
	if id == "" {
		return nil
	}
	pane, _ := store.Get(paneRef(id))
	return pane
}

func contentPaneTask(pane *data.Record, taskID string) string {
	if taskID == "" && pane != nil {
		if task, ok := pane.RefField("task"); ok {
			return task.ID
		}
	}
	return taskID
}

func setContentBaseDir(fields map[string]any, baseDir, cwd, filePath string, pane *data.Record) {
	if baseDir = strings.TrimSpace(baseDir); baseDir == "" {
		baseDir = strings.TrimSpace(cwd)
	}
	if baseDir == "" {
		baseDir = data.MarkdownFileDir(filePath)
	}
	if baseDir == "" && pane != nil {
		baseDir = strings.TrimSpace(pane.Text("cwd"))
	}
	if baseDir != "" {
		fields["baseDir"] = baseDir
	}
}
