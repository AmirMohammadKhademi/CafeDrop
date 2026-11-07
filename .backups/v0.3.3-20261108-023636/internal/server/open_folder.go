package server

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
