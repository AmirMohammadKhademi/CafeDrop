package storage

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

type Storage struct {
	root     string
	maxBytes int64
}

func New(root string, maxBytes int64) (*Storage, error) {
	if err := os.MkdirAll(root, 0750); err != nil {
		return nil, err
	}
	return &Storage{root, maxBytes}, nil
}
func (s *Storage) NewRequestFolder(id int64) (string, error) {
	full := filepath.Join(s.root, time.Now().Format("2006-01-02"), fmt.Sprintf("%06d", id))
	return full, os.MkdirAll(full, 0750)
}

// SafeOriginalName retains Unicode and the actual extension, while stripping client paths and unsafe characters.
func SafeOriginalName(original string) string {
	original = filepath.Base(strings.ReplaceAll(original, "\\", "/"))
	original = strings.TrimSpace(original)
	var b strings.Builder
	for _, r := range original {
		if unicode.IsControl(r) || r == '/' || r == '\\' || r == ':' || r == '<' || r == '>' || r == '"' || r == '|' || r == '?' || r == '*' {
			b.WriteRune('_')
		} else {
			b.WriteRune(r)
		}
	}
	name := strings.Trim(b.String(), " .")
	if name == "" || name == "." || name == ".." {
		name = "file"
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	if len([]rune(base)) > 100 {
		base = string([]rune(base)[:100])
	}
	if len([]rune(ext)) > 20 {
		ext = ""
	}
	return base + ext
}
func UniqueName(folder, original string) string {
	name := SafeOriginalName(original)
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for n := 0; ; n++ {
		candidate := name
		if n > 0 {
			candidate = fmt.Sprintf("%s (%d)%s", stem, n+1, ext)
		}
		_, err := os.Stat(filepath.Join(folder, candidate))
		if os.IsNotExist(err) {
			return candidate
		}
	}
}
func (s *Storage) Save(folder, original string, src io.Reader) (string, int64, error) {
	name := UniqueName(folder, original)
	path := filepath.Join(folder, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0640)
	if err != nil {
		return "", 0, err
	}
	n, copyErr := io.Copy(f, io.LimitReader(src, s.maxBytes+1))
	closeErr := f.Close()
	if copyErr != nil || closeErr != nil || n > s.maxBytes {
		os.Remove(path)
		if copyErr != nil {
			return "", 0, copyErr
		}
		if closeErr != nil {
			return "", 0, closeErr
		}
		return "", 0, fmt.Errorf("file exceeds configured limit")
	}
	return name, n, nil
}
