package handlers

import (
	"net/http/httptest"
	"testing"
)

func TestWebSocketCheckOrigin(t *testing.T) {
	cases := []struct {
		origin string
		host   string
		want   bool
	}{
		{"", "names.example.com", true}, // non-browser client, no Origin
		{"https://names.example.com", "names.example.com", true},
		{"http://localhost:8104", "localhost:8104", true},
		{"https://evil.example.com", "names.example.com", false},
		{"https://names.example.com.evil.com", "names.example.com", false},
		{"null", "names.example.com", false},
	}
	for _, tc := range cases {
		req := httptest.NewRequest("GET", "/ws", nil)
		req.Host = tc.host
		if tc.origin != "" {
			req.Header.Set("Origin", tc.origin)
		}
		if got := upgrader.CheckOrigin(req); got != tc.want {
			t.Errorf("CheckOrigin(origin=%q host=%q) = %v, want %v", tc.origin, tc.host, got, tc.want)
		}
	}
}
