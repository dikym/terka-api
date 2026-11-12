// Package web serves the static client build that is embedded into the binary.
package web

import (
	"embed"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"
)

//go:embed all:dist
var dist embed.FS

// Dist is the client build copied into dist/ before the binary is compiled.
func Dist() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}

const (
	immutablePrefix = "_app/immutable/"
	// Installed clients registered the worker at /sw.js; the build writes it under this name.
	workerBuildName = "service-worker.js"
)

// Types the standard table lacks or gets wrong for a browser.
var contentTypes = map[string]string{
	".js":          "text/javascript; charset=utf-8",
	".json":        "application/json",
	".webmanifest": "application/manifest+json",
}

// Handler serves files with clean URLs: /sesi answers with sesi.html.
// Hashed build files are cached forever; everything else is revalidated against the build version.
func Handler(files fs.FS, version string) http.Handler {
	etag := `"` + version + `"`
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var name string
		var file readSeekCloser
		if urlPath := r.URL.Path; len(urlPath) > 1 && strings.HasSuffix(urlPath, "/") {
			if target, ok := cleanTarget(files, urlPath); ok {
				if r.URL.RawQuery != "" {
					target += "?" + r.URL.RawQuery
				}
				http.Redirect(w, r, target, http.StatusPermanentRedirect)
				return
			}
		} else {
			name, file = find(files, strings.TrimPrefix(urlPath, "/"))
		}

		status := http.StatusOK
		if file == nil {
			name, file = open(files, "404.html")
			status = http.StatusNotFound
		}
		if file == nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		defer func() { _ = file.Close() }()

		h := w.Header()
		ext := path.Ext(name)
		if kind, ok := contentTypes[ext]; ok {
			h.Set("Content-Type", kind)
		} else if kind := mime.TypeByExtension(ext); kind != "" {
			h.Set("Content-Type", kind)
		}
		if strings.HasPrefix(name, immutablePrefix) {
			h.Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			h.Set("Cache-Control", "no-cache")
			h.Set("ETag", etag)
		}
		if status != http.StatusOK {
			w.WriteHeader(status)
			if r.Method == http.MethodGet {
				_, _ = io.Copy(w, file)
			}
			return
		}
		// Embedded files have no modification time, so freshness rides on the ETag alone.
		http.ServeContent(w, r, name, time.Time{}, file)
	})
}

// cleanTarget returns the slash-free path to redirect to, only when it names a file that exists.
// Echoing the request path back would let //host/ or /\host/ send the browser to another site.
func cleanTarget(files fs.FS, urlPath string) (string, bool) {
	clean := strings.TrimPrefix(strings.TrimRight(urlPath, "/"), "/")
	if clean == "" || strings.Contains(clean, `\`) {
		return "", false
	}
	_, file := find(files, clean)
	if file == nil {
		return "", false
	}
	_ = file.Close()
	return "/" + clean, true
}

func find(files fs.FS, name string) (string, readSeekCloser) {
	if name == "" {
		name = "index.html"
	}
	candidates := []string{name, name + ".html"}
	if name == "sw.js" {
		candidates = append(candidates, workerBuildName)
	}
	for _, candidate := range candidates {
		if found, file := open(files, candidate); file != nil {
			return found, file
		}
	}
	return "", nil
}

type readSeekCloser interface {
	io.ReadSeeker
	io.Closer
}

// open returns nil for anything that is not a regular file inside the tree.
func open(files fs.FS, name string) (string, readSeekCloser) {
	if !fs.ValidPath(name) {
		return "", nil
	}
	file, err := files.Open(name)
	if err != nil {
		return "", nil
	}
	info, err := file.Stat()
	seeker, ok := file.(readSeekCloser)
	if err != nil || !ok || !info.Mode().IsRegular() {
		_ = file.Close()
		return "", nil
	}
	return name, seeker
}
