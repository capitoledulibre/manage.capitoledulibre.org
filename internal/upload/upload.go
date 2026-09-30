// Package upload stores user-uploaded images on disk.
package upload

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// MaxSize is the largest accepted upload.
const MaxSize = 2 << 20 // 2 MiB

// ErrInvalid is returned for files that are not a supported image or are too large.
var ErrInvalid = errors.New("unsupported image (PNG, JPEG, GIF or WebP, 2 MB max)")

var extensions = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

// Store saves files under Dir.
type Store struct {
	Dir string
}

// New creates the directory if needed.
func New(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Store{Dir: dir}, nil
}

// Save reads the multipart file field and stores it. It returns the generated
// file name, or "" when no file was submitted.
func (s *Store) Save(r *http.Request, field string) (string, error) {
	if err := r.ParseMultipartForm(MaxSize); err != nil {
		return "", ErrInvalid
	}
	f, hdr, err := r.FormFile(field)
	if err != nil {
		if errors.Is(err, http.ErrMissingFile) {
			return "", nil
		}
		return "", err
	}
	defer f.Close()
	if hdr.Size == 0 {
		return "", nil
	}
	if hdr.Size > MaxSize {
		return "", ErrInvalid
	}
	return s.saveFile(f)
}

func (s *Store) saveFile(f multipart.File) (string, error) {
	b, err := io.ReadAll(io.LimitReader(f, MaxSize+1))
	if err != nil {
		return "", err
	}
	return s.SaveBytes(b)
}

// SaveBytes stores an image already held in memory, for pictures the server
// fetched itself rather than received through a form. It returns the generated
// file name.
func (s *Store) SaveBytes(b []byte) (string, error) {
	if len(b) == 0 || len(b) > MaxSize {
		return "", ErrInvalid
	}
	ext, ok := extensions[http.DetectContentType(b)]
	if !ok {
		return "", ErrInvalid
	}
	r := make([]byte, 16)
	rand.Read(r)
	name := hex.EncodeToString(r) + ext
	if err := os.WriteFile(filepath.Join(s.Dir, name), b, 0o644); err != nil {
		return "", err
	}
	return name, nil
}

// Remove deletes a stored file; a missing file is not an error.
func (s *Store) Remove(name string) {
	if name == "" || strings.ContainsAny(name, `/\`) {
		return
	}
	os.Remove(filepath.Join(s.Dir, name))
}

// Handler serves stored files with long-lived caching (names are random).
func (s *Store) Handler() http.Handler {
	fs := http.FileServer(http.Dir(s.Dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		fs.ServeHTTP(w, r)
	})
}
