package server

import (
	"encoding/json"
	"image/png"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"cafedrop/internal/store"
)

type networkAddress struct {
	IP      string `json:"ip"`
	Prefix  int    `json:"prefix"`
	Private bool   `json:"private"`
}

type networkInterface struct {
	Name         string           `json:"name"`
	HardwareAddr string           `json:"hardware_addr"`
	MTU          int              `json:"mtu"`
	Up           bool             `json:"up"`
	Loopback     bool             `json:"loopback"`
	Addresses    []networkAddress `json:"addresses"`
}

func discoverInterfaces() ([]networkInterface, []string, error) {
	all, err := net.Interfaces()
	if err != nil {
		return nil, nil, err
	}
	items := make([]networkInterface, 0, len(all))
	type candidate struct {
		ip   string
		rank int
	}
	candidateItems := []candidate{}
	for _, iface := range all {
		item := networkInterface{Name: iface.Name, HardwareAddr: iface.HardwareAddr.String(), MTU: iface.MTU, Up: iface.Flags&net.FlagUp != 0, Loopback: iface.Flags&net.FlagLoopback != 0, Addresses: []networkAddress{}}
		if item.Up && !item.Loopback {
			addrs, _ := iface.Addrs()
			for _, raw := range addrs {
				ip, network, parseErr := net.ParseCIDR(raw.String())
				if parseErr != nil || ip == nil {
					continue
				}
				if v4 := ip.To4(); v4 != nil {
					private := v4.IsPrivate()
					item.Addresses = append(item.Addresses, networkAddress{IP: v4.String(), Prefix: maskBits(network.Mask), Private: private})
					if private && !v4.IsLoopback() && !v4.IsLinkLocalUnicast() {
						candidateItems = append(candidateItems, candidate{ip: v4.String(), rank: interfaceRank(iface.Name)})
					}
				} else if !ip.IsLoopback() && !ip.IsLinkLocalUnicast() {
					item.Addresses = append(item.Addresses, networkAddress{IP: ip.String(), Prefix: maskBits(network.Mask), Private: ip.IsPrivate()})
				}
			}
		}
		items = append(items, item)
	}
	sort.SliceStable(candidateItems, func(i, j int) bool {
		if candidateItems[i].rank != candidateItems[j].rank {
			return candidateItems[i].rank < candidateItems[j].rank
		}
		return ipLess(candidateItems[i].ip, candidateItems[j].ip)
	})
	candidates := make([]string, 0, len(candidateItems))
	seen := map[string]bool{}
	for _, item := range candidateItems {
		if !seen[item.ip] {
			candidates = append(candidates, item.ip)
			seen[item.ip] = true
		}
	}
	return items, candidates, nil
}

func interfaceRank(name string) int {
	lower := strings.ToLower(name)
	for _, marker := range []string{"veth", "vethernet", "virtual", "vmware", "virtualbox", "wsl", "docker", "hyper-v", "tap", "tun", "vpn", "loopback", "bluetooth"} {
		if strings.Contains(lower, marker) {
			return 2
		}
	}
	for _, marker := range []string{"wi-fi", "wifi", "wireless", "ethernet", "wlan", "lan"} {
		if strings.Contains(lower, marker) {
			return 0
		}
	}
	return 1
}

func ipLess(a, b string) bool {
	left, right := net.ParseIP(a).To4(), net.ParseIP(b).To4()
	if left == nil || right == nil {
		return a < b
	}
	for i := 0; i < 4; i++ {
		if left[i] != right[i] {
			return left[i] < right[i]
		}
	}
	return false
}

func maskBits(mask net.IPMask) int { ones, _ := mask.Size(); return ones }

func (s *Server) selectedNetworkIP(settings store.NetworkSettings, candidates []string) string {
	for _, candidate := range candidates {
		if candidate == settings.PreferredIP {
			return candidate
		}
	}
	if len(candidates) > 0 {
		return candidates[0]
	}
	return ""
}

func listenPort(addr string) string {
	_, port, err := net.SplitHostPort(addr)
	if err == nil && port != "" {
		return port
	}
	return "8080"
}

func listensOnLAN(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && !ip.IsLoopback()
}

func (s *Server) networkPortalURL(ip string) string {
	if ip == "" {
		return ""
	}
	return "http://" + net.JoinHostPort(ip, listenPort(s.cfg.ListenAddr)) + "/"
}

func (s *Server) handleAdminNetwork(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		interfaces, candidates, err := discoverInterfaces()
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": "خواندن کارت‌های شبکه ناموفق بود"})
			return
		}
		settings, err := s.db.GetNetworkSettings()
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": "خواندن تنظیمات شبکه ناموفق بود"})
			return
		}
		selected := s.selectedNetworkIP(settings, candidates)
		blocked, err := s.db.ListBlockedIPs()
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": "خواندن فهرست IPهای مسدود ناموفق بود"})
			return
		}
		writeJSON(w, 200, map[string]any{
			"interfaces": interfaces, "candidate_ips": candidates,
			"preferred_ip": settings.PreferredIP, "selected_ip": selected,
			"wifi_ssid": settings.WiFiSSID, "shop_name": settings.ShopName, "portal_url": s.networkPortalURL(selected),
			"listen_addr": s.cfg.ListenAddr, "port": listenPort(s.cfg.ListenAddr), "lan_listening": listensOnLAN(s.cfg.ListenAddr),
			"blocked_ips": blocked, "captive_portal": map[string]any{"status": "not_configured", "message": "برای هدایت خودکار، باید قابلیت Captive Portal در روتر سازگار تنظیم شود."},
		})
	case http.MethodPut:
		var in store.NetworkSettings
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			writeJSON(w, 400, map[string]string{"error": "درخواست نامعتبر است"})
			return
		}
		in.PreferredIP = strings.TrimSpace(in.PreferredIP)
		in.WiFiSSID = strings.TrimSpace(in.WiFiSSID)
		in.ShopName = strings.TrimSpace(in.ShopName)
		if len([]rune(in.ShopName)) > 60 {
			writeJSON(w, 400, map[string]string{"error": "نام کافی‌نت نباید بیشتر از ۶۰ نویسه باشد"})
			return
		}
		if len(in.WiFiSSID) > 32 {
			writeJSON(w, 400, map[string]string{"error": "نام Wi-Fi نباید بیشتر از ۳۲ بایت باشد"})
			return
		}
		_, candidates, err := discoverInterfaces()
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": "خواندن کارت‌های شبکه ناموفق بود"})
			return
		}
		if in.PreferredIP != "" {
			valid := false
			for _, candidate := range candidates {
				if candidate == in.PreferredIP {
					valid = true
					break
				}
			}
			if !valid {
				writeJSON(w, 400, map[string]string{"error": "IP انتخاب‌شده روی کارت شبکه فعال این کامپیوتر وجود ندارد"})
				return
			}
		}
		if err := s.db.SaveNetworkSettings(in); err != nil {
			writeJSON(w, 500, map[string]string{"error": "ذخیره تنظیمات شبکه ناموفق بود"})
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "settings": in})
	default:
		writeJSON(w, 405, map[string]string{"error": "method not allowed"})
	}
}

func escapeWiFiQR(value string) string {
	replacer := strings.NewReplacer("\\", "\\\\", ";", "\\;", ",", "\\,", ":", "\\:", "\"", "\\\"")
	return replacer.Replace(value)
}

func (s *Server) handleAdminNetworkQR(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", 405)
		return
	}
	settings, err := s.db.GetNetworkSettings()
	if err != nil {
		http.Error(w, "network settings unavailable", 500)
		return
	}
	var payload string
	switch r.URL.Query().Get("type") {
	case "portal":
		_, candidates, discoverErr := discoverInterfaces()
		if discoverErr != nil {
			http.Error(w, "network discovery failed", 500)
			return
		}
		ip := s.selectedNetworkIP(settings, candidates)
		payload = s.networkPortalURL(ip)
		if payload == "" {
			http.Error(w, "هیچ IP محلی قابل استفاده‌ای پیدا نشد", 409)
			return
		}
	case "wifi":
		if settings.WiFiSSID == "" {
			http.Error(w, "ابتدا نام Wi-Fi را ذخیره کنید", 409)
			return
		}
		payload = "WIFI:T:nopass;S:" + escapeWiFiQR(settings.WiFiSSID) + ";;"
	default:
		http.Error(w, "invalid QR type", 400)
		return
	}
	code, err := qrcode.New(payload, qrcode.Medium)
	if err != nil {
		http.Error(w, "QR generation failed", 500)
		return
	}
	img := code.Image(280)
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	if err := png.Encode(w, img); err != nil {
		return
	}
}

func normalizeClientIP(raw string) string {
	host, _, err := net.SplitHostPort(raw)
	if err == nil {
		raw = host
	}
	raw = strings.TrimSpace(raw)
	if zone := strings.LastIndex(raw, "%"); zone >= 0 {
		raw = raw[:zone]
	}
	if ip := net.ParseIP(raw); ip != nil {
		return ip.String()
	}
	return strings.TrimSpace(raw)
}

func (s *Server) handleAdminBlockIP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]string{"error": "method not allowed"})
		return
	}
	var in struct {
		IP string `json:"ip"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		writeJSON(w, 400, map[string]string{"error": "درخواست نامعتبر است"})
		return
	}
	ip := net.ParseIP(strings.TrimSpace(in.IP))
	if ip == nil {
		writeJSON(w, 400, map[string]string{"error": "آدرس IP معتبر نیست"})
		return
	}
	canonical := ip.String()
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	if err := s.db.BlockIP(canonical, "blocked from CafeDrop admin"); err != nil {
		writeJSON(w, 500, map[string]string{"error": "مسدودسازی ناموفق بود"})
		return
	}
	_, _ = s.db.DB.Exec(`UPDATE client_sessions SET status='kicked' WHERE ip=? AND status IN ('active','grace','waiting')`, canonical)
	if cfg, err := s.getSessionSettings(); err == nil {
		_ = s.promoteWaiting(time.Now().UTC(), cfg)
	}
	writeJSON(w, 200, map[string]any{"ok": true, "ip": canonical})
}

func (s *Server) handleAdminUnblockIP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]string{"error": "method not allowed"})
		return
	}
	var in struct {
		IP string `json:"ip"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		writeJSON(w, 400, map[string]string{"error": "درخواست نامعتبر است"})
		return
	}
	ip := net.ParseIP(strings.TrimSpace(in.IP))
	if ip == nil {
		writeJSON(w, 400, map[string]string{"error": "آدرس IP معتبر نیست"})
		return
	}
	if err := s.db.UnblockIP(ip.String()); err != nil {
		writeJSON(w, 500, map[string]string{"error": "رفع مسدودسازی ناموفق بود"})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "ip": ip.String()})
}

func (s *Server) ipBlocked(ip string) bool {
	blocked, err := s.db.IsIPBlocked(normalizeClientIP(ip))
	// Fail closed if the deny-list cannot be checked.
	return err != nil || blocked
}
