package main

// forgotten holds the rows this session deleted, by noun and key, so
// history.undo of a delete puts one back as it was (a store with
// AcceptsKey asks recall for its key on create). One map serves every
// noun; each noun keeps its own record type.
type forgotten map[[2]string]any

// forget keeps a deleted row of noun under its key.
func forget[T any](a *App, noun, key string, row T) {
	if a.forgotten == nil {
		a.forgotten = forgotten{}
	}
	a.forgotten[[2]string{noun, key}] = row
}

// recall takes back the row of noun deleted under key: ok is whether this
// session deleted one; it is kept no longer.
func recall[T any](a *App, noun, key string) (row T, ok bool) {
	row, ok = a.forgotten[[2]string{noun, key}].(T)
	delete(a.forgotten, [2]string{noun, key})
	return row, ok
}
