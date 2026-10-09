package artifacts

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequestBaseURL(t *testing.T) {
	tests := []struct {
		name   string
		host   string
		proto  string
		fwdHost string
		want   string
	}{
		{name: "plain HTTP", host: "127.0.0.1:8088", want: "http://127.0.0.1:8088"},
		{name: "forwarded HTTPS", host: "127.0.0.1:8088", proto: "https", fwdHost: "artifacts.example.com", want: "https://artifacts.example.com"},
		{name: "ignore invalid forwarded proto", host: "localhost:8088", proto: "javascript", want: "http://localhost:8088"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://"+tt.host+"/", nil)
			if tt.proto != "" {
				req.Header.Set("X-Forwarded-Proto", tt.proto)
			}
			if tt.fwdHost != "" {
				req.Header.Set("X-Forwarded-Host", tt.fwdHost)
			}
			if got := requestBaseURL(req); got != tt.want {
				t.Fatalf("requestBaseURL() = %q, want %q", got, tt.want)
			}
		})
	}
}
