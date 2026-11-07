package server

import (
	"cafedrop/internal/storage"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// handleOpenFolder opens only the server-recorded upload folder in the host OS file manager.
func (s *Server) handleOpenFolder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var input struct {
		Code string `json:"code"`
	}
	if json.NewDecoder(r.Body).Decode(&input) != nil || strings.TrimSpace(input.Code) == "" {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	upload, err := s.db.FindUpload(strings.TrimSpace(input.Code))
	if err != nil || upload.Status != "completed" {
		http.NotFound(w, r)
		return
	}
	folder, err := filepath.Abs(upload.Folder)
	if err != nil {
		http.Error(w, "storage path error", http.StatusInternalServerError)
		return
	}
	info, err := os.Stat(folder)
	if err != nil || !info.IsDir() {
		http.NotFound(w, r)
		return
	}
	if err := s.restoreOriginalFilenames(upload.ID, folder); err != nil {
		writeJSON(w, 500, map[string]any{"error": "اصلاح نام فایل‌ها انجام نشد: " + err.Error()})
		return
	}
	if runtime.GOOS != "windows" {
		writeJSON(w, http.StatusNotImplemented, map[string]any{"error": "باز کردن مستقیم پوشه فقط روی میزبان ویندوز پشتیبانی می‌شود.", "folder": folder})
		return
	}
	if err := exec.Command("explorer.exe", folder).Start(); err != nil {
		http.Error(w, "could not open folder on host", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"opened": true, "folder": folder})
}

// Rename legacy generated files to safe original names without changing their bytes.
func (s *Server) restoreOriginalFilenames(uploadID int64, folder string) error {
	files, err := s.db.ListUploadFiles(uploadID)
	if err != nil {
		return err
	}
	for _, f := range files {
		old := filepath.Join(folder, filepath.Base(f.StoredName))
		if _, err := os.Stat(old); err != nil {
			return err
		}
		// Preserve already repaired names, including collision suffixes.
		prefix, _, generated := strings.Cut(f.StoredName, "_")
		if _, parseErr := strconv.ParseInt(prefix, 10, 64); !generated || parseErr != nil {
			continue
		}
		target := storage.UniqueName(folder, f.OriginalName)
		if target == f.StoredName {
			continue
		}
		newPath := filepath.Join(folder, target)
		if err := os.Rename(old, newPath); err != nil {
			return err
		}
		if _, err := s.db.DB.Exec(`UPDATE upload_files SET stored_name=? WHERE id=? AND upload_id=?`, target, f.ID, uploadID); err != nil {
			_ = os.Rename(newPath, old)
			return err
		}
	}
	return nil
}
