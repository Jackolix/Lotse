package hub

import (
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/Jackolix/Lotse/web"
)

// static serves the embedded single-page app. Unknown paths get index.html so
// client-side routes survive a reload.
func (h *Hub) static() http.Handler {
	dist := web.Dist()
	index, _ := fs.ReadFile(dist, "index.html")
	files := http.FileServerFS(dist)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			// Vite puts a content hash in every asset name.
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			files.ServeHTTP(w, r)
			return
		}
		if p := strings.TrimPrefix(path.Clean(r.URL.Path), "/"); p != "" && p != "index.html" {
			if fi, err := fs.Stat(dist, p); err == nil && !fi.IsDir() {
				files.ServeHTTP(w, r)
				return
			}
		}
		if index == nil {
			http.Error(w, "The web UI is not part of this build. Run `npm run build` in web/ and rebuild the hub.", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(index)
	})
}
