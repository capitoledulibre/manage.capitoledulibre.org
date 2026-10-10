package server

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http"

	"manage/web"
)

// assetVersions maps each static file path ("/static/css/app.css") to a short
// hash of its content. Templates reference assets through the "asset"
// function, so a new build changes the URL and browsers never keep a stale
// stylesheet or script.
var assetVersions = func() map[string]string {
	out := map[string]string{}

	_ = fs.WalkDir(web.Static, "static", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}

		data, err := fs.ReadFile(web.Static, p)
		if err != nil {
			return err
		}

		sum := sha256.Sum256(data)
		out["/"+p] = hex.EncodeToString(sum[:])[:12]

		return nil
	})

	return out
}()

// assetURL returns the versioned URL of a static file.
func assetURL(path string) string {
	if v, ok := assetVersions[path]; ok {
		return path + "?v=" + v
	}

	return path
}

// cacheStatic lets browsers cache versioned assets forever; unversioned
// requests must be revalidated.
func cacheStatic(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if v := r.URL.Query().Get("v"); v != "" && v == assetVersions["/static/"+r.URL.Path] {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}

		h.ServeHTTP(w, r)
	})
}
