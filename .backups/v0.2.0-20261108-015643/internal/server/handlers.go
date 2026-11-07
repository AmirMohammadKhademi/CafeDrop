package server

import (
	"archive/zip"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"cafedrop/internal/store"
)

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxUploadBytes*int64(s.cfg.MaxFilesPerRequest)+2*1024*1024)
	mr, err := r.MultipartReader()
	if err != nil {
		http.Error(w, "invalid multipart request", http.StatusBadRequest)
		return
	}
	code, err := newCode()
	if err != nil {
		http.Error(w, "could not create tracking code", http.StatusInternalServerError)
		return
	}
	id, err := s.db.CreateUpload(code, "pending")
	if err != nil {
		http.Error(w, "could not create upload", http.StatusInternalServerError)
		return
	}
	folder, err := s.files.NewRequestFolder(id)
	if err != nil {
		_ = s.db.FinishUpload(id, "failed")
		http.Error(w, "storage unavailable", http.StatusInternalServerError)
		return
	}
	_, _ = s.db.DB.Exec(`UPDATE uploads SET folder=? WHERE id=?`, folder, id)
	count := 0
	for {
		part, perr := mr.NextPart()
		if errors.Is(perr, io.EOF) {
			break
		}
		if perr != nil {
			_ = s.db.FinishUpload(id, "failed")
			http.Error(w, "upload interrupted", http.StatusBadRequest)
			return
		}
		if part.FileName() == "" {
			part.Close()
			continue
		}
		count++
		if count > s.cfg.MaxFilesPerRequest {
			part.Close()
			_ = s.db.FinishUpload(id, "failed")
			http.Error(w, "too many files", http.StatusRequestEntityTooLarge)
			return
		}
		stored, size, saveErr := s.files.Save(folder, part.FileName(), part)
		part.Close()
		if saveErr != nil {
			_ = s.db.FinishUpload(id, "failed")
			http.Error(w, saveErr.Error(), http.StatusRequestEntityTooLarge)
			return
		}
		if err := s.db.AddFile(id, part.FileName(), stored, size); err != nil {
			_ = s.db.FinishUpload(id, "failed")
			http.Error(w, "database write failed", http.StatusInternalServerError)
			return
		}
	}
	if count == 0 {
		_ = s.db.FinishUpload(id, "failed")
		http.Error(w, "no files received", http.StatusBadRequest)
		return
	}
	if err := s.db.FinishUpload(id, "completed"); err != nil {
		http.Error(w, "could not finalize upload", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"code": code, "file_count": count, "status": "completed"})
}
func (s *Server) handleAdminUploads(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "method not allowed", 405)
		return
	}
	items, err := s.db.ListUploads(100)
	if err != nil {
		http.Error(w, "database error", 500)
		return
	}
	writeJSON(w, 200, items)
}
func (s *Server) handleLookup(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}
	var in struct {
		Code string `json:"code"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		http.Error(w, "invalid request", 400)
		return
	}
	u, err := s.db.FindUpload(strings.TrimSpace(in.Code))
	if err != nil {
		if store.IsNoRows(err) {
			http.NotFound(w, r)
			return
		}
		http.NotFound(w, r)
		return
	}
	entries, err := os.ReadDir(u.Folder)
	if err != nil {
		http.Error(w, "storage error", 500)
		return
	}
	names := []string{}
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	writeJSON(w, 200, map[string]any{"upload": u, "files": names, "folder": filepath.Clean(u.Folder)})
}
func newCode() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "CD-" + strings.ToUpper(hex.EncodeToString(b)), nil
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	u, err := s.db.FindUpload(strings.TrimSpace(r.URL.Query().Get("code")))
	if err != nil || u.Status != "completed" {
		http.NotFound(w, r)
		return
	}
	entries, err := os.ReadDir(u.Folder)
	if err != nil {
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+u.Code+`.zip"`)
	zw := zip.NewWriter(w)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(u.Folder, entry.Name())
		f, openErr := os.Open(path)
		if openErr != nil {
			_ = zw.Close()
			return
		}
		out, createErr := zw.Create(entry.Name())
		if createErr != nil {
			f.Close()
			_ = zw.Close()
			return
		}
		_, copyErr := io.Copy(out, f)
		_ = f.Close()
		if copyErr != nil {
			_ = zw.Close()
			return
		}
	}
	_ = zw.Close()
}
