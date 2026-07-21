// Package webui serves the operator console from inside the control-plane
// binary. Runix ships as a single executable, so the UI is exported to
// static files at build time (`make web-build`, which runs `next build`
// with output: "export") and embedded here — there is no Node runtime on
// a Runix host and nothing to reverse-proxy.
package webui

import (
	"embed"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// dist holds the exported UI. `all:` matters: without it go:embed skips
// entries beginning with _ or ., which would drop Next's entire _next
// directory — every script and stylesheet the page loads.
//
// A placeholder index.html is committed so the tree always compiles; a
// real build overwrites it. Check Built() before assuming there is a UI.
//
//go:embed all:dist
var dist embed.FS

const placeholderMarker = "runix-webui-placeholder"

// Built reports whether a real UI was compiled in, as opposed to the
// placeholder that keeps `go build ./...` working in a bare checkout.
func Built() bool {
	b, err := dist.ReadFile("dist/index.html")
	if err != nil {
		return false
	}
	return !strings.Contains(string(b), placeholderMarker)
}

// Handler serves the exported app.
//
// Next is configured with trailingSlash, so every page is a directory
// holding an index.html and any path can be resolved from disk — no
// catch-all rewrite to index.html, which would otherwise answer 200 for
// URLs that do not exist. Unknown paths get the exported 404 page.
func Handler() http.Handler {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		// Only possible if the embed directive above is wrong, which is a
		// build-time mistake rather than a runtime condition.
		panic("webui: " + err.Error())
	}
	return &handler{files: sub}
}

type handler struct{ files fs.FS }

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "" {
		name = "index.html"
	}

	f, info, ok := h.open(name)
	if !ok {
		h.notFound(w, r)
		return
	}
	defer f.Close()

	seeker, seekable := f.(io.ReadSeeker)
	if !seekable {
		// Every file in an embed.FS is seekable; this guards a future
		// change of backing store rather than a case that happens today.
		h.notFound(w, r)
		return
	}

	setCacheHeaders(w, name)
	http.ServeContent(w, r, info.Name(), info.ModTime(), seeker)
}

// open resolves a request path to a file: the exact entry, or the
// directory's index.html, mirroring how the export is laid out.
func (h *handler) open(name string) (fs.File, fs.FileInfo, bool) {
	for _, candidate := range []string{name, path.Join(name, "index.html"), name + ".html"} {
		f, err := h.files.Open(candidate)
		if err != nil {
			continue
		}
		info, err := f.Stat()
		if err != nil || info.IsDir() {
			f.Close()
			continue
		}
		return f, info, true
	}
	return nil, nil, false
}

func (h *handler) notFound(w http.ResponseWriter, r *http.Request) {
	f, _, ok := h.open("404.html")
	if !ok {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	// Written directly rather than through http.ServeContent, which sets
	// its own 200 status and would silently turn this into a success.
	body, err := io.ReadAll(f)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write(body)
}

func setCacheHeaders(w http.ResponseWriter, name string) {
	switch {
	case strings.HasPrefix(name, "_next/static/"):
		// Filenames in here carry a content hash, so they can never go
		// stale for a given URL.
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	case strings.HasSuffix(name, ".html"):
		// Revalidate every time: after an upgrade the HTML must point at
		// the new hashed assets immediately.
		w.Header().Set("Cache-Control", "no-cache")
	default:
		w.Header().Set("Cache-Control", "public, max-age=3600")
	}
}
