package service

import (
	"strings"
	"testing"
)

func TestNormalizeReverseProxyDomain(t *testing.T) {
	tests := []struct {
		input   string
		want    string
		wantErr bool
	}{
		{input: "", want: ""},
		{input: " Panel.Example.COM. ", want: "panel.example.com"},
		{input: "https://panel.example.com", wantErr: true},
		{input: "panel.example.com:443", wantErr: true},
		{input: "-panel.example.com", wantErr: true},
		{input: "panel..example.com", wantErr: true},
	}
	for _, test := range tests {
		got, err := normalizeReverseProxyDomain(test.input)
		if test.wantErr {
			if err == nil {
				t.Fatalf("normalizeReverseProxyDomain(%q) unexpectedly succeeded", test.input)
			}
			continue
		}
		if err != nil || got != test.want {
			t.Fatalf("normalizeReverseProxyDomain(%q) = %q, %v; want %q", test.input, got, err, test.want)
		}
	}
}

func TestRenderReverseProxyConfigs(t *testing.T) {
	caddy := renderCaddyReverseProxy("panel.example.com", 2095)
	for _, expected := range []string{
		reverseProxyManagedBegin,
		"panel.example.com {",
		"reverse_proxy 127.0.0.1:2095",
		reverseProxyManagedEnd,
	} {
		if !strings.Contains(caddy, expected) {
			t.Fatalf("Caddy config missing %q:\n%s", expected, caddy)
		}
	}

	nginx := renderNginxReverseProxy("", 2095)
	for _, expected := range []string{
		reverseProxyManagedBegin,
		"server_name _;",
		"proxy_pass http://127.0.0.1:2095;",
		reverseProxyManagedEnd,
	} {
		if !strings.Contains(nginx, expected) {
			t.Fatalf("Nginx config missing %q:\n%s", expected, nginx)
		}
	}
}

func TestCanReplaceReverseProxyConfig(t *testing.T) {
	managed := []byte(renderCaddyReverseProxy("", 2095))
	if !canReplaceReverseProxyConfig("caddy", managed) {
		t.Fatal("managed Caddy config was rejected")
	}

	legacy := []byte(`:80 {
	encode gzip
	reverse_proxy 127.0.0.1:2095 {
		header_up X-Real-IP {remote_host}
		header_up X-Forwarded-For {remote_host}
		header_up X-Forwarded-Proto {scheme}
	}
}`)
	if !canReplaceReverseProxyConfig("caddy", legacy) {
		t.Fatal("legacy installer Caddy config was rejected")
	}

	custom := []byte(`example.com {
	root * /srv/site
	file_server
}`)
	if canReplaceReverseProxyConfig("caddy", custom) {
		t.Fatal("custom Caddy config must not be overwritten")
	}

	customLocalProxy := []byte(`example.com {
	reverse_proxy 127.0.0.1:3000
}`)
	if canReplaceReverseProxyConfig("caddy", customLocalProxy) {
		t.Fatal("custom local reverse proxy must not be mistaken for a legacy 1S-UI config")
	}
}

func TestReverseProxyPublicURL(t *testing.T) {
	if got := reverseProxyPublicURL("caddy", "panel.example.com", "app"); got != "https://panel.example.com/app/" {
		t.Fatalf("Caddy public URL = %q", got)
	}
	if got := reverseProxyPublicURL("nginx", "panel.example.com", "/app/"); got != "http://panel.example.com/app/" {
		t.Fatalf("Nginx public URL = %q", got)
	}
	if got := reverseProxyPublicURL("caddy", "", "/app/"); got != "" {
		t.Fatalf("IP-only public URL should be resolved by the browser, got %q", got)
	}
}
