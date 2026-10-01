// Package files stores uploaded files on disk under random names.
package files

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	ErrInvalidName = errors.New("invalid file name")
	ErrBadType     = errors.New("unsupported file type")
	ErrTooLarge    = errors.New("file too large")
)

// Kind describes an accepted upload category.
type Kind int

const (
	KindPDF Kind = iota
	KindLogo
)

var validName = regexp.MustCompile(`^[a-f0-9]{32}\.(pdf|png|svg|jpg|webp)$`)

type Store struct {
	dir string
}

func New(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}

	return &Store{dir: dir}, nil
}

// Save validates and writes an upload, returning the generated file name.
func (s *Store) Save(r io.Reader, originalName string, kind Kind, maxSize int64) (string, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxSize+1))
	if err != nil {
		return "", err
	}

	if int64(len(data)) > maxSize {
		return "", ErrTooLarge
	}

	ext, err := detect(data, originalName, kind)
	if err != nil {
		return "", err
	}

	b := make([]byte, 16)
	_, _ = rand.Read(b)
	name := hex.EncodeToString(b) + "." + ext

	tmp := filepath.Join(s.dir, "."+name+".tmp")
	if err := os.WriteFile(tmp, data, 0o640); err != nil {
		return "", err
	}

	if err := os.Rename(tmp, filepath.Join(s.dir, name)); err != nil {
		os.Remove(tmp)

		return "", err
	}

	return name, nil
}

// Path returns the absolute path of a stored file.
func (s *Store) Path(name string) (string, error) {
	if !validName.MatchString(name) {
		return "", ErrInvalidName
	}

	return filepath.Join(s.dir, name), nil
}

// Serve writes a stored file to the response. SVG files are sandboxed so an
// uploaded SVG cannot run scripts in our origin.
func (s *Store) Serve(w http.ResponseWriter, r *http.Request, name, downloadName string) {
	path, err := s.Path(name)
	if err != nil {
		http.NotFound(w, r)

		return
	}

	f, err := os.Open(path)
	if err != nil {
		http.NotFound(w, r)

		return
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		http.NotFound(w, r)

		return
	}

	w.Header().Set("Content-Type", ContentType(name))
	w.Header().Set("X-Content-Type-Options", "nosniff")

	if strings.HasSuffix(name, ".svg") {
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	}

	if downloadName != "" {
		w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", downloadName))
	}

	http.ServeContent(w, r, "", st.ModTime(), f)
}

func ContentType(name string) string {
	switch filepath.Ext(name) {
	case ".pdf":
		return "application/pdf"
	case ".png":
		return "image/png"
	case ".svg":
		return "image/svg+xml"
	case ".jpg":
		return "image/jpeg"
	case ".webp":
		return "image/webp"
	}

	return "application/octet-stream"
}

// Detect validates data as the given kind and returns the file extension
// (without dot) to store it under.
func Detect(data []byte, originalName string, kind Kind) (string, error) {
	return detect(data, originalName, kind)
}

func detect(data []byte, originalName string, kind Kind) (string, error) {
	sniffed := http.DetectContentType(data)

	switch kind {
	case KindPDF:
		if bytes.HasPrefix(data, []byte("%PDF-")) {
			return "pdf", nil
		}
	case KindLogo:
		switch sniffed {
		case "image/png":
			return "png", nil
		case "image/jpeg":
			return "jpg", nil
		case "image/webp":
			return "webp", nil
		}

		if strings.EqualFold(filepath.Ext(originalName), ".svg") && isSVG(data) {
			return "svg", nil
		}
	}

	return "", ErrBadType
}

func isSVG(data []byte) bool {
	head := data
	if len(head) > 4096 {
		head = head[:4096]
	}

	return bytes.Contains(bytes.ToLower(head), []byte("<svg"))
}
