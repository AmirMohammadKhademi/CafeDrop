package server

import "testing"

func TestEscapeWiFiQR(t *testing.T) {
	got := escapeWiFiQR(`Cafe;Drop,West:5\GHz"`)
	want := `Cafe\;Drop\,West\:5\\GHz\"`
	if got != want {
		t.Fatalf("escapeWiFiQR() = %q, want %q", got, want)
	}
}

func TestListensOnLAN(t *testing.T) {
	if !listensOnLAN("0.0.0.0:8080") || !listensOnLAN("192.168.1.26:8080") {
		t.Fatal("LAN bind should be recognized")
	}
	if listensOnLAN("127.0.0.1:8080") {
		t.Fatal("loopback-only bind must not be recognized as LAN accessible")
	}
}

func TestInterfaceRankPrefersPhysicalLAN(t *testing.T) {
	if interfaceRank("Wi-Fi") >= interfaceRank("vEthernet (WSL)") {
		t.Fatal("physical Wi-Fi should be preferred over a virtual WSL adapter")
	}
}

func TestNormalizeClientIP(t *testing.T) {
	cases := map[string]string{
		"192.168.1.25:54321":  "192.168.1.25",
		"[::1]:54321":         "::1",
		"::ffff:192.168.1.25": "192.168.1.25",
	}
	for input, want := range cases {
		if got := normalizeClientIP(input); got != want {
			t.Errorf("normalizeClientIP(%q) = %q, want %q", input, got, want)
		}
	}
}
