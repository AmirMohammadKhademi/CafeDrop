package server

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"cafedrop/internal/config"
	"cafedrop/internal/storage"
	"cafedrop/internal/store"
)

type Server struct {
	cfg       config.Config
	db        *store.Store
	files     *storage.Storage
	mux       *http.ServeMux
	sessionMu sync.Mutex
}

func New(cfg config.Config, db *store.Store, files *storage.Storage) *Server {
	s := &Server{cfg: cfg, db: db, files: files, mux: http.NewServeMux()}
	s.routes()
	return s
}
func (s *Server) Handler() http.Handler { return securityHeaders(s.mux) }
func (s *Server) routes() {
	web := filepath.Join("web", "customer")
	s.mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.Dir(filepath.Join(web, "assets")))))
	s.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Join(web, "index.html"))
	})
	s.mux.HandleFunc("/api/uploads", s.handleUpload)
	s.mux.HandleFunc("/api/session/start", s.handleSessionStart)
	s.mux.HandleFunc("/api/session/heartbeat", s.handleSessionHeartbeat)
	s.mux.HandleFunc("/api/session/leave", s.handleSessionLeave)
	s.mux.HandleFunc("/api/admin/sessions", s.admin(s.handleAdminSessions))
	s.mux.HandleFunc("/api/admin/network", s.admin(s.handleAdminNetwork))
	s.mux.HandleFunc("/api/admin/network/qr", s.admin(s.handleAdminNetworkQR))
	s.mux.HandleFunc("/api/admin/devices/block", s.admin(s.handleAdminBlockIP))
	s.mux.HandleFunc("/api/admin/devices/unblock", s.admin(s.handleAdminUnblockIP))
	s.mux.HandleFunc("/api/admin/session-settings", s.admin(s.handleAdminSessionSettings))
	s.mux.HandleFunc("/api/admin/session-action", s.admin(s.handleAdminSessionAction))
	s.mux.HandleFunc("/api/admin/uploads", s.admin(s.handleAdminUploads))
	s.mux.HandleFunc("/api/admin/delete-upload", s.admin(s.handleAdminDeleteUpload))
	s.mux.HandleFunc("/api/admin/upload-status", s.admin(s.handleAdminUploadStatus))
	s.mux.HandleFunc("/api/admin/search", s.admin(s.handleSearchUploads))
	s.mux.HandleFunc("/api/admin/events", s.admin(s.handleAdminEvents))
	s.mux.HandleFunc("/api/admin/lookup", s.admin(s.handleLookup))
	s.mux.HandleFunc("/api/admin/download", s.admin(s.handleDownload))
	s.mux.HandleFunc("/api/admin/download-file", s.admin(s.handleDownloadFile))
	s.mux.HandleFunc("/api/admin/open-folder", s.admin(s.handleOpenFolder))
	s.mux.HandleFunc("/admin", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		http.ServeFile(w, r, filepath.Join("web", "admin", "index.html"))
	})
}
func (s *Server) admin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || subtle.ConstantTimeCompare([]byte(u), []byte(s.cfg.AdminUser)) != 1 || subtle.ConstantTimeCompare([]byte(p), []byte(s.cfg.AdminPassword)) != 1 {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}
		next(w, r)
	}
}
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// Authenticated server-sent events: browser uses fetch with its existing Basic auth header.
func (s *Server) handleAdminEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "stream unsupported", http.StatusInternalServerError)
		return
	}

	// The client sends its last processed event ID on reconnect so that
	// uploads completed during a short disconnect are not silently skipped.
	latest, err := s.db.LatestUploadEventID()
	if err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	after := latest
	if raw := r.URL.Query().Get("after"); raw != "" {
		parsed, parseErr := strconv.ParseInt(raw, 10, 64)
		if parseErr != nil || parsed < 0 {
			http.Error(w, "invalid event cursor", http.StatusBadRequest)
			return
		}
		after = parsed
	}

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	_, _ = fmt.Fprintf(w, "id: %d\nevent: ready\ndata: %d\n\n", after, after)
	flusher.Flush()

	ticker := time.NewTicker(1500 * time.Millisecond)
	defer ticker.Stop()
	ticks := 0
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			ticks++
			if ticks%10 == 0 {
				_, _ = fmt.Fprint(w, "event: sessions\ndata: 1\n\n")
			}
			events, queryErr := s.db.UploadEventsAfter(after)
			if queryErr != nil {
				return
			}
			if len(events) == 0 {
				_, _ = fmt.Fprint(w, ": heartbeat\n\n")
			} else {
				for _, event := range events {
					_, _ = fmt.Fprintf(w, "id: %d\nevent: upload\ndata: %d\n\n", event.ID, event.UploadID)
					after = event.ID
				}
			}
			flusher.Flush()
		}
	}
}
