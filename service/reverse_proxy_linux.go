//go:build linux

package service

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Hhz0823/1s-ui/logger"
	"github.com/Hhz0823/1s-ui/util/common"
)

const (
	caddyConfigPath = "/etc/caddy/Caddyfile"
	nginxConfigPath = "/etc/nginx/sites-available/s-ui.conf"
)

func reverseProxyStatusPlatform(_ *ReverseProxyService) (*ReverseProxyStatus, error) {
	status, err := reverseProxyPanelSettings()
	if err != nil {
		return nil, err
	}

	_, systemctlErr := exec.LookPath("systemctl")
	_, caddyErr := exec.LookPath("caddy")
	_, nginxErr := exec.LookPath("nginx")
	status.Privileged = os.Geteuid() == 0
	status.Supported = status.Privileged && systemctlErr == nil
	status.CaddyInstalled = caddyErr == nil
	status.NginxInstalled = nginxErr == nil

	caddyRunning := serviceIsActive("caddy")
	nginxRunning := serviceIsActive("nginx")
	caddyManaged := proxyConfigIsManaged("caddy", caddyConfigPath)
	nginxManaged := proxyConfigIsManaged("nginx", nginxConfigPath)

	switch {
	case caddyRunning:
		status.Engine = "caddy"
		status.Running = true
		status.Managed = caddyManaged
	case nginxRunning:
		status.Engine = "nginx"
		status.Running = true
		status.Managed = nginxManaged
	case caddyManaged:
		status.Engine = "caddy"
		status.Managed = true
	case nginxManaged:
		status.Engine = "nginx"
		status.Managed = true
	case status.CaddyInstalled:
		status.Engine = "caddy"
	case status.NginxInstalled:
		status.Engine = "nginx"
	}

	status.Installed = (status.Engine == "caddy" && status.CaddyInstalled) ||
		(status.Engine == "nginx" && status.NginxInstalled)
	status.Enabled = status.Running && status.Managed
	status.PublicURL = reverseProxyPublicURL(status.Engine, status.Domain, status.PanelPath)

	switch {
	case !status.Privileged:
		status.Message = "s-ui must run as root to manage the system reverse proxy"
	case systemctlErr != nil:
		status.Message = "systemd is unavailable; reverse proxy management is read-only"
	case status.Running && !status.Managed:
		status.Message = "the active reverse proxy has custom configuration and will not be overwritten"
	case !status.CaddyInstalled && !status.NginxInstalled:
		status.Message = "Caddy or Nginx is not installed"
	}
	return status, nil
}

func applyReverseProxyPlatform(s *ReverseProxyService, config ReverseProxyConfig) (*ReverseProxyStatus, error) {
	status, err := reverseProxyStatusPlatform(s)
	if err != nil {
		return nil, err
	}
	if !status.Supported {
		return nil, common.NewError(status.Message)
	}
	if config.Engine == "caddy" && !status.CaddyInstalled {
		return nil, common.NewError("Caddy is not installed; install it first or rerun the full installer")
	}
	if config.Engine == "nginx" && !status.NginxInstalled {
		return nil, common.NewError("Nginx is not installed; install it first or rerun the full installer")
	}

	otherEngine := "nginx"
	if config.Engine == "nginx" {
		otherEngine = "caddy"
	}
	if serviceIsActive(otherEngine) {
		return nil, common.NewErrorf("%s is already active; stop it before switching reverse proxy engines", otherEngine)
	}

	settings := &SettingService{}
	panelPort, err := settings.GetPort()
	if err != nil {
		return nil, err
	}
	panelPath, err := settings.GetWebPath()
	if err != nil {
		return nil, err
	}
	oldListen, err := settings.GetListen()
	if err != nil {
		return nil, err
	}
	oldDomain, err := settings.GetWebDomain()
	if err != nil {
		return nil, err
	}
	oldURI, err := settings.getString("webURI")
	if err != nil {
		return nil, err
	}

	configPath := caddyConfigPath
	rendered := renderCaddyReverseProxy(config.Domain, panelPort)
	if config.Engine == "nginx" {
		configPath = nginxConfigPath
		rendered = renderNginxReverseProxy(config.Domain, panelPort)
	}

	oldConfig, oldConfigExists, err := readOptionalFile(configPath)
	if err != nil {
		return nil, err
	}
	if !canReplaceReverseProxyConfig(config.Engine, oldConfig) {
		return nil, common.NewErrorf("%s contains custom configuration; the panel will not overwrite it", configPath)
	}

	if err := writeAtomicFile(configPath, []byte(rendered), 0644); err != nil {
		return nil, err
	}
	if config.Engine == "nginx" {
		if err := enableNginxSite(configPath); err != nil {
			restoreProxyConfig(configPath, oldConfig, oldConfigExists)
			return nil, err
		}
	}
	if out, err := validateReverseProxy(config.Engine, configPath); err != nil {
		restoreProxyConfig(configPath, oldConfig, oldConfigExists)
		return nil, common.NewErrorf("%s configuration is invalid: %s", config.Engine, commandOutput(out, err))
	}

	wasRunning := serviceIsActive(config.Engine)
	if out, err := exec.Command("systemctl", "enable", config.Engine).CombinedOutput(); err != nil {
		restoreProxyConfig(configPath, oldConfig, oldConfigExists)
		return nil, common.NewErrorf("failed to enable %s: %s", config.Engine, commandOutput(out, err))
	}
	serviceAction := "start"
	if wasRunning {
		serviceAction = "reload"
	}
	if out, err := exec.Command("systemctl", serviceAction, config.Engine).CombinedOutput(); err != nil {
		restoreProxyConfig(configPath, oldConfig, oldConfigExists)
		if wasRunning {
			_, _ = exec.Command("systemctl", "reload", config.Engine).CombinedOutput()
		}
		return nil, common.NewErrorf("failed to %s %s: %s", serviceAction, config.Engine, commandOutput(out, err))
	}

	publicURL := reverseProxyPublicURL(config.Engine, config.Domain, panelPath)
	if err := saveReverseProxyPanelSettings(settings, config.Domain, publicURL); err != nil {
		_ = settings.SetWebListen(oldListen)
		_ = settings.SetWebDomain(oldDomain)
		_ = settings.SetWebURI(oldURI)
		restoreProxyConfig(configPath, oldConfig, oldConfigExists)
		if wasRunning {
			_, _ = exec.Command("systemctl", "reload", config.Engine).CombinedOutput()
		} else {
			_, _ = exec.Command("systemctl", "stop", config.Engine).CombinedOutput()
		}
		return nil, err
	}

	logger.Infof("reverse proxy configured: engine=%s domain=%s upstream=127.0.0.1:%d", config.Engine, config.Domain, panelPort)
	return reverseProxyStatusPlatform(s)
}

func saveReverseProxyPanelSettings(settings *SettingService, domain, publicURL string) error {
	if err := settings.SetWebListen("127.0.0.1"); err != nil {
		return err
	}
	if err := settings.SetWebDomain(domain); err != nil {
		return err
	}
	return settings.SetWebURI(publicURL)
}

func serviceIsActive(name string) bool {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return false
	}
	return exec.Command("systemctl", "is-active", "--quiet", name).Run() == nil
}

func proxyConfigIsManaged(engine, path string) bool {
	content, err := os.ReadFile(path)
	return err == nil && strings.TrimSpace(string(content)) != "" &&
		canReplaceReverseProxyConfig(engine, content)
}

func readOptionalFile(path string) ([]byte, bool, error) {
	content, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	return content, err == nil, err
}

func writeAtomicFile(path string, content []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".1s-ui-proxy-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func restoreProxyConfig(path string, content []byte, existed bool) {
	if existed {
		if err := writeAtomicFile(path, content, 0644); err != nil {
			logger.Error("restore reverse proxy config failed: ", err)
		}
		return
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		logger.Error("remove failed reverse proxy config failed: ", err)
	}
}

func validateReverseProxy(engine, configPath string) ([]byte, error) {
	if engine == "caddy" {
		return exec.Command("caddy", "validate", "--config", configPath, "--adapter", "caddyfile").CombinedOutput()
	}
	return exec.Command("nginx", "-t").CombinedOutput()
}

func enableNginxSite(configPath string) error {
	const enabledPath = "/etc/nginx/sites-enabled/s-ui.conf"
	if err := os.MkdirAll(filepath.Dir(enabledPath), 0755); err != nil {
		return err
	}
	if target, err := os.Readlink(enabledPath); err == nil && target == configPath {
		return nil
	}
	if err := os.Remove(enabledPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Symlink(configPath, enabledPath)
}

func commandOutput(out []byte, err error) string {
	message := strings.TrimSpace(string(out))
	if message != "" {
		return message
	}
	if err != nil {
		return err.Error()
	}
	return "unknown error"
}
