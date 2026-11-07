package storage

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Storage struct {
	root     string
	maxBytes int64
}

func New(root string, maxBytes int64) (*Storage, error) {
	if err := os.MkdirAll(root, 0750); err != nil {
		return nil, err
	}
	return &Storage{root: root, maxBytes: maxBytes}, nil
}
func (s *Storage) NewRequestFolder(id int64) (string, error) {
	day := time.Now().Format("2006-01-02")
	rel := filepath.Join(day, fmt.Sprintf("%06d", id))
	full := filepath.Join(s.root, rel)
	return full, os.MkdirAll(full, 0750)
}
func (s *Storage) Save(folder, original string, src io.Reader) (string, int64, error) {
	name := filepath.Base(strings.ReplaceAll(original, "\\", "/"))
	if name == "." || name == "/" || name == "" {
		name = "file"
	}
	// Store under a generated filename; never trust a client-supplied path.
	stored := fmt.Sprintf("%d_%s", time.Now().UnixNano(), safeName(name))
	path := filepath.Join(folder, stored)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0640)
	if err != nil {
		return "", 0, err
	}
	n, copyErr := io.Copy(f, io.LimitReader(src, s.maxBytes+1))
	closeErr := f.Close()
	if copyErr != nil {
		os.Remove(path)
		return "", 0, copyErr
	}
	if closeErr != nil {
		os.Remove(path)
		return "", 0, closeErr
	}
	if n > s.maxBytes {
		os.Remove(path)
		return "", 0, fmt.Errorf("file exceeds configured limit")
	}
	return stored, n, nil
}
func safeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	if b.Len() > 80 {
		return b.String()[:80]
	}
	return b.String()
}
