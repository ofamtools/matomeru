package mcpui

import (
	"errors"
	"path/filepath"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

// CreateDocumentArgs are the arguments of data.create_document.
type CreateDocumentArgs struct {
	ID       string `json:"id,omitempty"`
	Title    string `json:"title,omitempty"`
	FilePath string `json:"filePath,omitempty"`
	BaseDir  string `json:"baseDir,omitempty"`
	Cwd      string `json:"cwd,omitempty"`
	Summary  string `json:"summary,omitempty"`
	Body     string `json:"body,omitempty"`
	Pane     string `json:"pane,omitempty"`
	Task     string `json:"task,omitempty"`
	By       string `json:"by,omitempty"`
}

// CreateDocumentResult is what data.create_document returns.
type CreateDocumentResult struct {
	Document *data.Record   `json:"document"`
	Relation *data.Relation `json:"relation,omitempty"`
}

// CreateDocument is data.create_document: it stores a KindDocument record,
// inherits the pane's task when omitted, and links pane -> document via RelCreated.
func (d *DataTools) CreateDocument(actor core.Actor, args CreateDocumentArgs) (*CreateDocumentResult, error) {
	title := strings.TrimSpace(args.Title)
	if title == "" && args.FilePath != "" {
		title = filepath.Base(args.FilePath)
	}
	if title == "" {
		return nil, errors.New("title or filePath is required")
	}
	pane := lookupContentPane(d.store, args.Pane)
	taskID := contentPaneTask(pane, args.Task)
	fields := map[string]any{"title": title}
	setContentBaseDir(fields, args.BaseDir, args.Cwd, args.FilePath, pane)
	if args.FilePath != "" {
		fields["filePath"] = args.FilePath
	}
	if args.Summary != "" {
		fields["summary"] = args.Summary
	}
	if args.Body != "" {
		fields["body"] = args.Body
	}
	if taskID != "" {
		fields["task"] = taskRef(taskID)
	}
	doc, _, err := d.store.Create(data.CreateInput{Kind: data.KindDocument, ID: args.ID, Fields: fields}, actor)
	if err != nil {
		return nil, err
	}
	if args.FilePath == "" && args.Body != "" {
		if root := d.store.FilesRoot(); root != "" {
			managedPath, werr := data.WriteManagedFile(root, data.CategoryDocuments, doc.ID, title, ".md", args.Body)
			if werr != nil {
				return nil, werr
			}
			fileFields := map[string]any{"filePath": managedPath}
			if doc.Text("baseDir") == "" {
				fileFields["baseDir"] = filepath.Dir(managedPath)
			}
			if updated, _, perr := d.store.Patch(data.PatchInput{
				Ref:     doc.Ref(),
				Fields:  fileFields,
				Summary: "record managed file path",
			}, actor); perr == nil && updated != nil {
				doc = updated
			}
		}
	}
	out := &CreateDocumentResult{Document: doc}
	if args.Pane != "" {
		if out.Relation, _, err = d.store.Relate(data.RelateInput{From: paneRef(args.Pane), Kind: data.RelCreated, To: doc.Ref()}, actor); err != nil {
			return nil, err
		}
	}
	return out, nil
}
