package mdweb

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"net/http"
)

//go:embed assets/app.css
var AppCSS []byte

//go:embed assets/app.js
var AppJS []byte

//go:embed assets/beautiful-mermaid.js
var BeautifulMermaidJS []byte

type staticAsset struct {
	data         []byte
	contentType  string
	etag         string
	cacheControl string
}

var staticAssets map[string]staticAsset

func init() {
	staticAssets = map[string]staticAsset{
		"app.css": {
			data:         AppCSS,
			contentType:  "text/css; charset=utf-8",
			etag:         computeETag(AppCSS),
			cacheControl: "public, max-age=300, must-revalidate",
		},
		"app.js": {
			data:         AppJS,
			contentType:  "application/javascript; charset=utf-8",
			etag:         computeETag(AppJS),
			cacheControl: "public, max-age=300, must-revalidate",
		},
		"beautiful-mermaid.js": {
			data:         BeautifulMermaidJS,
			contentType:  "application/javascript; charset=utf-8",
			etag:         computeETag(BeautifulMermaidJS),
			cacheControl: "public, max-age=3600, must-revalidate",
		},
	}
}

func computeETag(data []byte) string {
	sum := sha256.Sum256(data)
	return `"` + hex.EncodeToString(sum[:8]) + `"`
}

func serveStaticAsset(w http.ResponseWriter, r *http.Request, name string) bool {
	asset, ok := staticAssets[name]
	if !ok {
		return false
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return true
	}
	w.Header().Set("Content-Type", asset.contentType)
	w.Header().Set("ETag", asset.etag)
	w.Header().Set("Cache-Control", asset.cacheControl)
	if r.Header.Get("If-None-Match") == asset.etag {
		w.WriteHeader(http.StatusNotModified)
		return true
	}
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return true
	}
	_, _ = w.Write(asset.data)
	return true
}
