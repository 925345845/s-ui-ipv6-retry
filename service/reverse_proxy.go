package service

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/Hhz0823/1s-ui/util/common"
)

const (
	reverseProxyManagedBegin = "# BEGIN 1S-UI MANAGED REVERSE PROXY"
	reverseProxyManagedEnd   = "# END 1S-UI MANAGED REVERSE PROXY"
)

var reverseProxyDomainLabel = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

type ReverseProxyConfig struct {
	Engine string `json:"engine"`
	Domain string `json:"domain"`
}

type ReverseProxyStatus struct {
	Supported      bool   `json:"supported"`
	Privileged     bool   `json:"privileged"`
	Enabled        bool   `json:"enabled"`
	Installed      bool   `json:"installed"`
	Running        bool   `json:"running"`
	Managed        bool   `json:"managed"`
	CaddyInstalled bool   `json:"caddyInstalled"`
	NginxInstalled bool   `json:"nginxInstalled"`
	Engine         string `json:"engine"`
	Domain         string `json:"domain"`
	PanelListen    string `json:"panelListen"`
	PanelPort      int    `json:"panelPort"`
	PanelPath      string `json:"panelPath"`
	PublicURL      string `json:"publicUrl"`
	Message        string `json:"message"`
}

type ReverseProxyService struct{}

func (s *ReverseProxyService) GetStatus() (*ReverseProxyStatus, error) {
	return reverseProxyStatusPlatform(s)
}

func (s *ReverseProxyService) Apply(config ReverseProxyConfig) (*ReverseProxyStatus, error) {
	engine := strings.ToLower(strings.TrimSpace(config.Engine))
	if engine != "caddy" && engine != "nginx" {
		return nil, common.NewError("reverse proxy engine must be caddy or nginx")
	}
	domain, err := normalizeReverseProxyDomain(config.Domain)
	if err != nil {
		return nil, err
	}
	config.Engine = engine
	config.Domain = domain
	return applyReverseProxyPlatform(s, config)
}

func reverseProxyPanelSettings() (*ReverseProxyStatus, error) {
	settings := &SettingService{}
	listen, err := settings.GetListen()
	if err != nil {
		return nil, err
	}
	port, err := settings.GetPort()
	if err != nil {
		return nil, err
	}
	path, err := settings.GetWebPath()
	if err != nil {
		return nil, err
	}
	domain, err := settings.GetWebDomain()
	if err != nil {
		return nil, err
	}
	return &ReverseProxyStatus{
		Engine:      "caddy",
		Domain:      strings.TrimSpace(domain),
		PanelListen: strings.TrimSpace(listen),
		PanelPort:   port,
		PanelPath:   path,
	}, nil
}

func normalizeReverseProxyDomain(value string) (string, error) {
	domain := strings.ToLower(strings.TrimSpace(value))
	domain = strings.TrimSuffix(domain, ".")
	if domain == "" {
		return "", nil
	}
	if len(domain) > 253 || strings.ContainsAny(domain, "/:@[] \t\r\n") {
		return "", common.NewError("domain must be a hostname without scheme, port, or path")
	}
	for _, label := range strings.Split(domain, ".") {
		if !reverseProxyDomainLabel.MatchString(label) {
			return "", common.NewError("invalid reverse proxy domain")
		}
	}
	return domain, nil
}

func reverseProxyPublicURL(engine, domain, panelPath string) string {
	if domain == "" {
		return ""
	}
	scheme := "http"
	if engine == "caddy" {
		scheme = "https"
	}
	return scheme + "://" + domain + normalizePanelPath(panelPath)
}

func normalizePanelPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "/app/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if !strings.HasSuffix(path, "/") {
		path += "/"
	}
	return path
}

func renderCaddyReverseProxy(domain string, panelPort int) string {
	site := ":80"
	if domain != "" {
		site = domain
	}
	return fmt.Sprintf(`%s
%s {
	encode gzip
	reverse_proxy 127.0.0.1:%d
}
%s
`, reverseProxyManagedBegin, site, panelPort, reverseProxyManagedEnd)
}

func renderNginxReverseProxy(domain string, panelPort int) string {
	serverName := "_"
	if domain != "" {
		serverName = domain
	}
	return fmt.Sprintf(`%s
server {
    listen 80;
    listen [::]:80;
    server_name %s;

    client_max_body_size 32m;

    location / {
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_pass http://127.0.0.1:%d;
    }
}
%s
`, reverseProxyManagedBegin, serverName, panelPort, reverseProxyManagedEnd)
}

func canReplaceReverseProxyConfig(engine string, content []byte) bool {
	trimmed := strings.TrimSpace(string(content))
	if trimmed == "" {
		return true
	}
	if strings.HasPrefix(trimmed, reverseProxyManagedBegin) &&
		strings.HasSuffix(trimmed, reverseProxyManagedEnd) {
		return true
	}
	switch engine {
	case "caddy":
		return isLegacyCaddyReverseProxy(trimmed)
	case "nginx":
		return isLegacyNginxReverseProxy(trimmed)
	default:
		return false
	}
}

func isLegacyCaddyReverseProxy(content string) bool {
	if strings.Count(content, "reverse_proxy") != 1 ||
		!strings.Contains(content, "reverse_proxy 127.0.0.1:") ||
		!strings.Contains(content, "encode gzip") ||
		!strings.Contains(content, "header_up X-Real-IP {remote_host}") ||
		!strings.Contains(content, "header_up X-Forwarded-For {remote_host}") ||
		!strings.Contains(content, "header_up X-Forwarded-Proto {scheme}") {
		return false
	}
	for _, rawLine := range strings.Split(content, "\n") {
		line := strings.TrimSpace(rawLine)
		switch {
		case line == "", line == "{", line == "}":
		case strings.HasPrefix(line, "email "):
		case line == "encode gzip":
		case strings.HasPrefix(line, "reverse_proxy 127.0.0.1:"):
			port := strings.TrimSuffix(strings.TrimPrefix(line, "reverse_proxy 127.0.0.1:"), " {")
			if _, err := strconv.Atoi(port); err != nil {
				return false
			}
		case strings.HasSuffix(line, " {"):
		case strings.HasPrefix(line, "header_up X-Real-IP "):
		case strings.HasPrefix(line, "header_up X-Forwarded-For "):
		case strings.HasPrefix(line, "header_up X-Forwarded-Proto "):
		default:
			return false
		}
	}
	return true
}

func isLegacyNginxReverseProxy(content string) bool {
	if strings.Count(content, "server {") != 1 ||
		strings.Count(content, "proxy_pass ") != 1 ||
		!strings.Contains(content, "proxy_pass http://127.0.0.1:") ||
		!strings.Contains(content, "client_max_body_size 32m;") ||
		!strings.Contains(content, "proxy_set_header X-Real-IP $remote_addr;") ||
		!strings.Contains(content, "proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;") {
		return false
	}
	return !strings.Contains(content, "include ") && !strings.Contains(content, "root ")
}
