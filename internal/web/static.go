package web

import (
	"bytes"
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

//go:embed dist/*
var embeddedDist embed.FS

func (ws *WebServer) handleSPA(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/keys/") || strings.HasPrefix(r.URL.Path, "/v1/") {
		http.NotFound(w, r)
		return
	}

	name, ok := cleanSPAPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}

	if name != "index.html" && serveEmbeddedSPA(w, r, name) {
		return
	}
	if serveEmbeddedSPA(w, r, "index.html") {
		return
	}

	// ponytail: dist может быть пустым в чистой Go-разработке без npm build.
	// Docker/прод собирают SPA перед go build, там index.html будет.
	http.Error(w, "SPA build missing; run npm --prefix web run build", http.StatusNotFound)
}

func cleanSPAPath(urlPath string) (string, bool) {
	name := strings.TrimPrefix(urlPath, "/")
	if name == "" || name == "/" {
		return "index.html", true
	}
	if strings.Contains(name, "..") {
		return "", false
	}
	name = strings.TrimPrefix(path.Clean("/"+name), "/")
	if name == "" || name == "." {
		return "index.html", true
	}
	return name, true
}

func serveEmbeddedSPA(w http.ResponseWriter, r *http.Request, name string) bool {
	spaFS, err := fs.Sub(embeddedDist, "dist")
	if err != nil {
		return false
	}
	b, err := fs.ReadFile(spaFS, name)
	if err != nil {
		return false
	}
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(b))
	return true
}
