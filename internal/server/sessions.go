package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

const sessionCookie = "cafedrop_session"

func sessionTimestamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000000000Z") }

type sessionSettings struct {
	MaxActive       int `json:"max_active"`
	DurationMinutes int `json:"duration_minutes"`
	GraceMinutes    int `json:"grace_minutes"`
}

type clientSession struct {
	Token         string     `json:"token"`
	IP            string     `json:"ip"`
	UserAgent     string     `json:"user_agent"`
	CreatedAt     time.Time  `json:"created_at"`
	LastSeen      time.Time  `json:"last_seen"`
	ExpiresAt     time.Time  `json:"expires_at"`
	GraceUntil    *time.Time `json:"grace_until,omitempty"`
	Status        string     `json:"status"`
	QueuePosition int        `json:"queue_position,omitempty"`
}

func (s *Server) getSessionSettings() (sessionSettings, error) {
	var v sessionSettings
	err := s.db.DB.QueryRow(`SELECT max_active,duration_minutes,grace_minutes FROM session_config WHERE id=1`).Scan(&v.MaxActive, &v.DurationMinutes, &v.GraceMinutes)
	return v, err
}

func (s *Server) normalizeSessions(now time.Time, grace int) error {
	nowText := now.UTC().Format("2006-01-02T15:04:05.000000000Z")
	if grace > 0 {
		graceUntil := now.Add(time.Duration(grace) * time.Minute).UTC().Format("2006-01-02T15:04:05.000000000Z")
		if _, err := s.db.DB.Exec(`UPDATE client_sessions SET status='grace',grace_until=? WHERE status='active' AND expires_at<=?`, graceUntil, nowText); err != nil {
			return err
		}
	} else if _, err := s.db.DB.Exec(`UPDATE client_sessions SET status='expired' WHERE status='active' AND expires_at<=?`, nowText); err != nil {
		return err
	}
	if _, err := s.db.DB.Exec(`UPDATE client_sessions SET status='expired' WHERE status='grace' AND grace_until<>'' AND grace_until<=?`, nowText); err != nil {
		return err
	}
	return nil
}

func (s *Server) promoteWaiting(now time.Time, cfg sessionSettings) error {
	var active int
	if err := s.db.DB.QueryRow(`SELECT COUNT(*) FROM client_sessions WHERE status='active' AND expires_at>?`, now.UTC().Format("2006-01-02T15:04:05.000000000Z")).Scan(&active); err != nil {
		return err
	}
	slots := cfg.MaxActive - active
	if slots <= 0 {
		return nil
	}
	rows, err := s.db.DB.Query(`SELECT token FROM client_sessions WHERE status='waiting' ORDER BY created_at ASC LIMIT ?`, slots)
	if err != nil {
		return err
	}
	tokens := []string{}
	for rows.Next() {
		var token string
		if err := rows.Scan(&token); err != nil {
			rows.Close()
			return err
		}
		tokens = append(tokens, token)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, token := range tokens {
		expiry := now.Add(time.Duration(cfg.DurationMinutes) * time.Minute).UTC().Format("2006-01-02T15:04:05.000000000Z")
		if _, err := s.db.DB.Exec(`UPDATE client_sessions SET status='active',created_at=?,last_seen=?,expires_at=?,grace_until='' WHERE token=? AND status='waiting'`, now.UTC().Format("2006-01-02T15:04:05.000000000Z"), now.UTC().Format("2006-01-02T15:04:05.000000000Z"), expiry, token); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) activeCount(now time.Time) (int, error) {
	var n int
	err := s.db.DB.QueryRow(`SELECT COUNT(*) FROM client_sessions WHERE status='active' AND expires_at>?`, now.UTC().Format("2006-01-02T15:04:05.000000000Z")).Scan(&n)
	return n, err
}

func (s *Server) sessionResponse(token string, now time.Time, cfg sessionSettings) (map[string]any, error) {
	var ip, ua, created, last, expires, grace, status string
	err := s.db.DB.QueryRow(`SELECT ip,user_agent,created_at,last_seen,expires_at,grace_until,status FROM client_sessions WHERE token=?`, token).Scan(&ip, &ua, &created, &last, &expires, &grace, &status)
	if err != nil {
		return nil, err
	}
	var pos int
	if status == "waiting" {
		_ = s.db.DB.QueryRow(`SELECT COUNT(*) FROM client_sessions WHERE status='waiting' AND (created_at<? OR (created_at=? AND token<=?))`, created, created, token).Scan(&pos)
	}
	active, _ := s.activeCount(now)
	var waiting int
	_ = s.db.DB.QueryRow(`SELECT COUNT(*) FROM client_sessions WHERE status='waiting'`).Scan(&waiting)
	shopName := ""
	_ = s.db.DB.QueryRow(`SELECT shop_name FROM network_settings WHERE id=1`).Scan(&shopName)
	response := map[string]any{"status": status, "active_count": active, "waiting_count": waiting, "active_limit": cfg.MaxActive, "duration_minutes": cfg.DurationMinutes, "grace_minutes": cfg.GraceMinutes, "shop_name": shopName}
	if status == "waiting" {
		response["queue_position"] = pos
	}
	if expires != "" {
		response["expires_at"] = expires
	}
	if grace != "" {
		response["grace_until"] = grace
	}
	return response, nil
}

func clientIP(r *http.Request) string {
	return normalizeClientIP(r.RemoteAddr)
}

func randomToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (s *Server) handleSessionStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	if s.ipBlocked(clientIP(r)) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "دسترسی این IP توسط مدیر کافی‌نت مسدود شده است."})
		return
	}
	now := time.Now().UTC()
	cfg, err := s.getSessionSettings()
	if err != nil {
		http.Error(w, "session settings unavailable", 500)
		return
	}
	if err = s.normalizeSessions(now, cfg.GraceMinutes); err != nil {
		http.Error(w, "session state error", 500)
		return
	}
	if err = s.promoteWaiting(now, cfg); err != nil {
		http.Error(w, "queue state error", 500)
		return
	}
	token := ""
	if c, e := r.Cookie(sessionCookie); e == nil {
		token = c.Value
	}
	if token != "" {
		var status string
		if e := s.db.DB.QueryRow(`SELECT status FROM client_sessions WHERE token=?`, token).Scan(&status); e == nil && (status == "active" || status == "waiting") {
			_, _ = s.db.DB.Exec(`UPDATE client_sessions SET last_seen=? WHERE token=?`, sessionTimestamp(now), token)
			_ = s.promoteWaiting(now, cfg)
			data, e := s.sessionResponse(token, now, cfg)
			if e == nil {
				writeJSON(w, 200, data)
				return
			}
		}
	}
	token, err = randomToken()
	if err != nil {
		http.Error(w, "could not create session", 500)
		return
	}
	active, err := s.activeCount(now)
	if err != nil {
		http.Error(w, "session state error", 500)
		return
	}
	status := "active"
	expiry := now.Add(time.Duration(cfg.DurationMinutes) * time.Minute)
	if active >= cfg.MaxActive {
		status = "waiting"
		expiry = now
	}
	ts := sessionTimestamp(now)
	_, err = s.db.DB.Exec(`INSERT INTO client_sessions(token,ip,user_agent,created_at,last_seen,expires_at,status) VALUES(?,?,?,?,?,?,?)`, token, clientIP(r), r.UserAgent(), ts, ts, sessionTimestamp(expiry), status)
	if err != nil {
		http.Error(w, "could not create session", 500)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 604800})
	data, err := s.sessionResponse(token, now, cfg)
	if err != nil {
		http.Error(w, "session response error", 500)
		return
	}
	code := http.StatusOK
	if status == "waiting" {
		code = http.StatusAccepted
	}
	writeJSON(w, code, data)
}

func (s *Server) handleSessionHeartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		http.Error(w, "session missing", 401)
		return
	}
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	if s.ipBlocked(clientIP(r)) {
		_, _ = s.db.DB.Exec(`UPDATE client_sessions SET status='kicked' WHERE token=?`, c.Value)
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "دسترسی این IP مسدود شده است."})
		return
	}
	now := time.Now().UTC()
	cfg, err := s.getSessionSettings()
	if err != nil {
		http.Error(w, "settings error", 500)
		return
	}
	if err = s.normalizeSessions(now, cfg.GraceMinutes); err != nil {
		http.Error(w, "session state error", 500)
		return
	}
	_, _ = s.db.DB.Exec(`UPDATE client_sessions SET last_seen=? WHERE token=? AND status IN ('active','grace','waiting')`, sessionTimestamp(now), c.Value)
	if err = s.promoteWaiting(now, cfg); err != nil {
		http.Error(w, "queue error", 500)
		return
	}
	data, err := s.sessionResponse(c.Value, now, cfg)
	if err != nil {
		http.Error(w, "session not found", 401)
		return
	}
	status := http.StatusOK
	if data["status"] == "waiting" {
		status = http.StatusAccepted
	}
	writeJSON(w, status, data)
}

func (s *Server) handleSessionLeave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.sessionMu.Lock()
		_, _ = s.db.DB.Exec(`UPDATE client_sessions SET status='left' WHERE token=? AND status IN ('active','grace','waiting')`, c.Value)
		if cfg, e := s.getSessionSettings(); e == nil {
			_ = s.promoteWaiting(time.Now().UTC(), cfg)
		}
		s.sessionMu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) sessionAllowsUpload(r *http.Request) bool {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return false
	}
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	if s.ipBlocked(clientIP(r)) {
		return false
	}
	now := time.Now().UTC()
	cfg, err := s.getSessionSettings()
	if err != nil {
		return false
	}
	if err = s.normalizeSessions(now, cfg.GraceMinutes); err != nil {
		return false
	}
	var status, expires, grace string
	err = s.db.DB.QueryRow(`SELECT status,expires_at,grace_until FROM client_sessions WHERE token=?`, c.Value).Scan(&status, &expires, &grace)
	if err != nil {
		return false
	}
	if status != "active" {
		return false
	}
	t, parseErr := time.Parse(time.RFC3339Nano, expires)
	return parseErr == nil && now.Before(t)
}

func (s *Server) handleAdminSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", 405)
		return
	}
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	now := time.Now().UTC()
	cfg, err := s.getSessionSettings()
	if err != nil {
		http.Error(w, "settings error", 500)
		return
	}
	if err = s.normalizeSessions(now, cfg.GraceMinutes); err != nil {
		http.Error(w, "session state error", 500)
		return
	}
	_ = s.promoteWaiting(now, cfg)
	rows, err := s.db.DB.Query(`SELECT token,ip,user_agent,created_at,last_seen,expires_at,grace_until,status FROM client_sessions WHERE status IN ('active','grace','waiting') ORDER BY CASE status WHEN 'active' THEN 0 WHEN 'grace' THEN 1 ELSE 2 END,created_at ASC`)
	if err != nil {
		http.Error(w, "database error", 500)
		return
	}
	defer rows.Close()
	out := []clientSession{}
	queuePosition := 0
	for rows.Next() {
		var x clientSession
		var created, last, expires, grace string
		if err := rows.Scan(&x.Token, &x.IP, &x.UserAgent, &created, &last, &expires, &grace, &x.Status); err != nil {
			http.Error(w, "database error", 500)
			return
		}
		x.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		x.LastSeen, _ = time.Parse(time.RFC3339Nano, last)
		x.ExpiresAt, _ = time.Parse(time.RFC3339Nano, expires)
		if grace != "" {
			t, e := time.Parse(time.RFC3339Nano, grace)
			if e == nil {
				x.GraceUntil = &t
			}
		}
		if x.Status == "waiting" {
			queuePosition++
			x.QueuePosition = queuePosition
		}
		out = append(out, x)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		http.Error(w, "database error", 500)
		return
	}
	if err := rows.Close(); err != nil {
		http.Error(w, "database error", 500)
		return
	}
	var waitingCount int
	_ = s.db.DB.QueryRow(`SELECT COUNT(*) FROM client_sessions WHERE status='waiting'`).Scan(&waitingCount)
	writeJSON(w, 200, map[string]any{"sessions": out, "settings": cfg, "active_count": func() int { n, _ := s.activeCount(now); return n }(), "waiting_count": waitingCount})
}

func (s *Server) handleAdminSessionSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		cfg, err := s.getSessionSettings()
		if err != nil {
			http.Error(w, "settings error", 500)
			return
		}
		writeJSON(w, 200, cfg)
		return
	}
	if r.Method != http.MethodPut {
		http.Error(w, "method not allowed", 405)
		return
	}
	var cfg sessionSettings
	if json.NewDecoder(r.Body).Decode(&cfg) != nil {
		http.Error(w, "invalid settings", 400)
		return
	}
	if cfg.MaxActive < 1 || cfg.MaxActive > 200 || cfg.DurationMinutes < 5 || cfg.DurationMinutes > 720 || cfg.GraceMinutes < 0 || cfg.GraceMinutes > 30 {
		http.Error(w, "مقادیر خارج از محدوده مجاز هستند", 400)
		return
	}
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	_, err := s.db.DB.Exec(`UPDATE session_config SET max_active=?,duration_minutes=?,grace_minutes=? WHERE id=1`, cfg.MaxActive, cfg.DurationMinutes, cfg.GraceMinutes)
	if err != nil {
		http.Error(w, "settings save failed", 500)
		return
	}
	writeJSON(w, 200, cfg)
}

func (s *Server) handleAdminSessionAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	var in struct {
		Token   string `json:"token"`
		Action  string `json:"action"`
		Minutes int    `json:"minutes"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || strings.TrimSpace(in.Token) == "" {
		http.Error(w, "invalid request", 400)
		return
	}
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	now := time.Now().UTC()
	cfg, err := s.getSessionSettings()
	if err != nil {
		http.Error(w, "settings error", 500)
		return
	}
	switch in.Action {
	case "kick":
		_, err = s.db.DB.Exec(`UPDATE client_sessions SET status='kicked' WHERE token=? AND status IN ('active','grace','waiting')`, in.Token)
	case "extend":
		if in.Minutes < 1 || in.Minutes > 240 {
			http.Error(w, "invalid extension", 400)
			return
		}
		var currentStatus string
		if err = s.db.DB.QueryRow(`SELECT status FROM client_sessions WHERE token=?`, in.Token).Scan(&currentStatus); err != nil {
			http.Error(w, "session not found", 404)
			return
		}
		if currentStatus == "grace" {
			_, err = s.db.DB.Exec(`UPDATE client_sessions SET grace_until=? WHERE token=? AND status='grace'`, sessionTimestamp(now.Add(time.Duration(in.Minutes)*time.Minute)), in.Token)
		} else if currentStatus == "active" {
			_, err = s.db.DB.Exec(`UPDATE client_sessions SET expires_at=? WHERE token=? AND status='active'`, sessionTimestamp(now.Add(time.Duration(in.Minutes)*time.Minute)), in.Token)
		} else {
			http.Error(w, "session cannot be extended", 400)
			return
		}
	default:
		http.Error(w, "unknown action", 400)
		return
	}
	if err != nil {
		http.Error(w, "action failed", 500)
		return
	}
	if err = s.promoteWaiting(now, cfg); err != nil {
		http.Error(w, "queue update failed", 500)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}
