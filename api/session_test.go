package api

import (
	"crypto/tls"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRequestIsHTTPS(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name      string
		tls       bool
		forwarded string
		want      bool
	}{
		{name: "plain HTTP", want: false},
		{name: "direct TLS", tls: true, want: true},
		{name: "reverse proxy TLS", forwarded: "https", want: true},
		{name: "first forwarded protocol", forwarded: "https, http", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "http://example.com/", nil)
			if tt.tls {
				req.TLS = &tls.ConnectionState{}
			}
			if tt.forwarded != "" {
				req.Header.Set("X-Forwarded-Proto", tt.forwarded)
			}
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = req
			if got := requestIsHTTPS(ctx); got != tt.want {
				t.Fatalf("requestIsHTTPS() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRelayRefreshBaseURL(t *testing.T) {
	gin.SetMode(gin.TestMode)
	req := httptest.NewRequest("GET", "http://127.0.0.1/api/relay/1/bitbrowser.xlsx", nil)
	req.Host = "127.0.0.1:2095"
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "panel.example.com")
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = req

	if got, want := relayRefreshBaseURL(ctx, "/admin/"), "https://panel.example.com/admin"; got != want {
		t.Fatalf("relayRefreshBaseURL() = %q, want %q", got, want)
	}
}
