package debugui

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed static/*
var staticFS embed.FS

// Handler returns an HTTP handler that serves the embedded debug UI files.
// Mount at /debug/ in the router using http.StripPrefix.
func Handler() http.Handler {
	sub, _ := fs.Sub(staticFS, "static")
	return http.FileServer(http.FS(sub))
}
