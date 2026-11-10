package main

// pageLoad defers a page's store reads until something needs its rows: the
// page opens (Page.OnOpen), a reader of the loaded data asks (ensure), or a
// verb that reads the rows through the page. A headless CLI/MCP call draws no
// page, so New builds the page empty and never decodes a table it does not
// use. Until the first load, a refresh is a no-op: nothing shows the rows
// yet, and the first load reads the newest state anyway.
type pageLoad struct {
	load   func() error // reads the store and pushes the rows
	loaded bool
	count  int // how many times load ran (the test seam)
}

// ensure loads once; later calls are free.
func (p *pageLoad) ensure() error {
	if p.loaded {
		return nil
	}
	return p.reload()
}

// reload reads the store again (the first load included).
func (p *pageLoad) reload() error {
	p.loaded = true
	p.count++
	return p.load()
}

// refresh reloads only a page that has loaded; before that it does nothing.
func (p *pageLoad) refresh() error {
	if !p.loaded {
		return nil
	}
	return p.reload()
}
