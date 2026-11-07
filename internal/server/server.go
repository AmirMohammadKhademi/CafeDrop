package server

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"path/filepath"
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
	s.mux.HandleFunc("/api/admin/session-settings", s.admin(s.handleAdminSessionSettings))
	s.mux.HandleFunc("/api/admin/session-action", s.admin(s.handleAdminSessionAction))
	s.mux.HandleFunc("/api/admin/uploads", s.admin(s.handleAdminUploads))
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
		http.Error(w, "method not allowed", 405)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "stream unsupported", 500)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	latest, err := s.db.LatestCompletedID()
	if err != nil {
		http.Error(w, "database error", 500)
		return
	}
	fmt.Fprintf(w, "event: ready\ndata: %d\n\n", latest)
	flusher.Flush()
	ticker := time.NewTicker(time.Second)
	ticks := 0
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			ticks++
			if ticks%5 == 0 {
				fmt.Fprint(w, "event: sessions\ndata: 1\n\n")
			}
			current, err := s.db.LatestCompletedID()
			if err != nil {
				return
			}
			if current > latest {
				fmt.Fprintf(w, "event: upload\ndata: %d\n\n", current)
				latest = current
			} else {
				fmt.Fprint(w, ": heartbeat\n\n")
			}
			flusher.Flush()
		}
	}
}
