package server

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"cafedrop/internal/store"
)

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.sessionAllowsUpload(r) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "نشست شما فعال نیست یا زمان آن به پایان رسیده است. صفحه را تازه‌سازی کنید."})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxUploadBytes*int64(s.cfg.MaxFilesPerRequest)+2*1024*1024)
	mr, err := r.MultipartReader()
	if err != nil {
		http.Error(w, "invalid multipart request", http.StatusBadRequest)
		return
	}
	code, err := s.db.NextTrackingCode()
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
	items, err := s.db.ListUploads(500)
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
	files, err := s.db.ListUploadFiles(u.ID)
	if err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	absoluteFolder, err := filepath.Abs(u.Folder)
	if err != nil {
		http.Error(w, "storage path error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, 200, map[string]any{"upload": u, "files": files, "folder": absoluteFolder})
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

func (s *Server) handleDownloadFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	fileID, err := strconv.ParseInt(r.URL.Query().Get("file_id"), 10, 64)
	if err != nil || fileID < 1 {
		http.Error(w, "invalid file id", http.StatusBadRequest)
		return
	}
	u, err := s.db.FindUpload(code)
	if err != nil || u.Status != "completed" {
		http.NotFound(w, r)
		return
	}
	f, err := s.db.FindUploadFile(u.ID, fileID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	path := filepath.Join(u.Folder, filepath.Base(f.StoredName))
	if _, err := os.Stat(path); err != nil {
		http.NotFound(w, r)
		return
	}
	if contentType := mime.TypeByExtension(filepath.Ext(f.OriginalName)); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": f.OriginalName}))
	http.ServeFile(w, r, path)
}

func (s *Server) handleSearchUploads(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", 405)
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) > 80 {
		http.Error(w, "query too long", 400)
		return
	}
	if q == "" {
		writeJSON(w, 200, []store.Upload{})
		return
	}
	rows, err := s.db.SearchUploads(q, 100)
	if err != nil {
		http.Error(w, "search failed", 500)
		return
	}
	writeJSON(w, 200, rows)
}

func (s *Server) handleAdminUploadStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var in struct {
		Code        string `json:"code"`
		AdminStatus string `json:"admin_status"`
		AdminNote   string `json:"admin_note"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	in.Code = strings.TrimSpace(in.Code)
	in.AdminNote = strings.TrimSpace(in.AdminNote)
	if in.Code == "" || len(in.Code) > 80 || utf8.RuneCountInString(in.AdminNote) > 2000 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid code or note"})
		return
	}
	switch in.AdminStatus {
	case "received", "reviewing", "delivered", "rejected":
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request status"})
		return
	}
	u, err := s.db.FindUpload(in.Code)
	if err != nil {
		if store.IsNoRows(err) {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "database error"})
		return
	}
	if u.Status != "completed" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "upload is not complete yet"})
		return
	}
	if err := s.db.UpdateUploadWorkflowStatus(in.Code, in.AdminStatus, in.AdminNote); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not update request status"})
		return
	}
	updated, err := s.db.FindUpload(in.Code)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not reload request"})
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleAdminDeleteUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var in struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	code := strings.TrimSpace(in.Code)
	if code == "" || len(code) > 80 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid tracking code"})
		return
	}

	// Resolve and validate the stored folder before deleting the DB record.
	u, err := s.db.FindUpload(code)
	if err != nil {
		if store.IsNoRows(err) {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "database error"})
		return
	}
	if u.Status == "uploading" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "upload is still in progress"})
		return
	}
	root, err := filepath.Abs(s.cfg.UploadDir)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "storage path error"})
		return
	}
	folder, err := filepath.Abs(u.Folder)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "storage path error"})
		return
	}
	rel, err := filepath.Rel(root, folder)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "unsafe storage path"})
		return
	}

	deletedFolder, err := s.db.DeleteUpload(code)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not delete request"})
		return
	}
	// The DB record is authoritative; report a cleanup warning if files could
	// not be removed so the administrator can manually inspect the folder.
	if deletedFolder != "" {
		if err := os.RemoveAll(folder); err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "warning": "record deleted but stored files could not be fully removed"})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}
