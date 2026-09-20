package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Hhz0823/1s-ui/core"
	"github.com/Hhz0823/1s-ui/database"
	"github.com/Hhz0823/1s-ui/database/model"
	"github.com/Hhz0823/1s-ui/logger"
	"github.com/Hhz0823/1s-ui/util"
	"github.com/Hhz0823/1s-ui/util/common"
	"github.com/gofrs/uuid/v5"
	"github.com/xuri/excelize/v2"

	"gorm.io/gorm"
)

const (
	relayModeIPv6                 = "ipv6"
	relayModeUpstream             = "upstream"
	relayModePaired               = "paired"
	relayModeDualStack            = "dualstack"
	relaySourceAutoAddIPv6        = "help660vip/auto-add-ipv6"
	relayDomainStrategyIPv4Only   = "ipv4_only"
	relayDomainStrategyIPv6Only   = "ipv6_only"
	relayDomainStrategyPreferIPv6 = "prefer_ipv6"
	relayPairedDNSResolverTag     = "relay-paired-local-dns"
	relayAddressReady             = "ready"
	relayAddressTentative         = "tentative"
	relayAddressDADFailed         = "dadfailed"
	relayAddressMissing           = "missing"
	relayIPv6EgressErrorCode      = "relay_ipv6_egress_unreachable"
	relayIPv6ProbeWorkers         = 8
	relayDualStackIPv6Timeout     = "3s"
	relayIPv6DisplayLimit         = 128
	relayRotationMinMinutes       = 5
	relayRotationMaxMinutes       = 7 * 24 * 60
	relayRotationDefaultMinutes   = 60
	// A relay pool may contain up to 500 upstreams. Keep this bounded because
	// each item creates an inbound, outbound and route entry in sing-box.
	maxRelayItems    = 500
	relayCoreSingBox = model.CoreTypeSingBox
)

// Apple ID authentication uses a small, stable set of hosts. Keep these on
// the paired IPv4 SOCKS5 path so the login flow does not mix addresses with
// the VPS IPv6 used by the rest of the browser session.
var relayAppleIDDomains = []string{
	"appleid.apple.com",
	"idmsa.apple.com",
	"gsa.apple.com",
}

// Vinted's DataDome challenge is served from a small set of hosts that may be
// IPv4-only. Keep this exception narrow: Vinted itself and all other ordinary
// traffic remain on the per-item IPv6 direct outbound.
var relayVintedCaptchaDomains = []string{
	"geo.captcha-delivery.com",
	"captcha-delivery.com",
	"js.datadome.co",
	"api-js.datadome.co",
}

func relayModeUsesUpstream(mode string) bool {
	return mode == relayModeUpstream || mode == relayModePaired || mode == relayModeDualStack
}

func relayModeUsesIPv6(mode string) bool {
	return mode == relayModeIPv6 || mode == relayModePaired || mode == relayModeDualStack
}

func relayModePairsUpstream(mode string) bool {
	return mode == relayModePaired || mode == relayModeDualStack
}

var relayIPv6EgressTargets = []string{
	"[2606:4700:4700::1111]:443",
	"[2001:4860:4860::8888]:443",
}

var relayProtocols = map[string]bool{
	"socks": true, "http": true, "mixed": true, "shadowsocks": true,
	"vless": true, "vmess": true, "trojan": true, "hysteria2": true,
	"tuic": true, "naive": true, "anytls": true,
}

var relayTLSProtocols = map[string]bool{
	"trojan": true, "hysteria2": true, "tuic": true, "naive": true, "anytls": true,
}

var relayTLSSupportedProtocols = map[string]bool{
	"vless": true, "vmess": true, "trojan": true, "hysteria2": true, "tuic": true, "naive": true, "anytls": true,
}

var relayShadowsocksMethods = map[string]bool{
	"aes-128-gcm": true, "aes-192-gcm": true, "aes-256-gcm": true,
	"chacha20-ietf-poly1305": true, "xchacha20-ietf-poly1305": true,
	"2022-blake3-aes-128-gcm": true, "2022-blake3-aes-256-gcm": true,
	"2022-blake3-chacha20-poly1305": true,
}

var relayMu sync.Mutex

type RelayIPv6 struct {
	Interface string `json:"interface"`
	Address   string `json:"address"`
	Prefix    int    `json:"prefix"`
}

type RelayUpstream struct {
	Server   string `json:"server"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type RelayCreateRequest struct {
	Name               string          `json:"name"`
	Source             string          `json:"source"`
	Mode               string          `json:"mode"`
	PortStart          int             `json:"port_start"`
	Count              int             `json:"count"`
	UsernamePrefix     string          `json:"username_prefix"`
	PasswordLength     int             `json:"password_length"`
	PublicHost         string          `json:"public_host"`
	Interface          string          `json:"interface"`
	BaseIPv6           string          `json:"base_ipv6"`
	Prefix             int             `json:"prefix"`
	AddSystemAddresses bool            `json:"add_system_addresses"`
	IPv6Addresses      []string        `json:"ipv6_addresses"`
	Upstreams          []RelayUpstream `json:"upstreams"`
	UpstreamText       string          `json:"upstream_text"`
	Protocol           string          `json:"protocol"`
	CoreType           string          `json:"core_type"`
	TlsID              uint            `json:"tls_id"`
	Transport          string          `json:"transport"`
	DomainStrategy     string          `json:"domain_strategy"`
	ShadowsocksMethod  string          `json:"shadowsocks_method"`
	AppleIDIPv4Only    bool            `json:"apple_id_ipv4_only"`
}

type RelayData struct {
	Pools             []model.RelayPool `json:"pools"`
	IPv6              []RelayIPv6       `json:"ipv6"`
	IPv6Total         int               `json:"ipv6_total"`
	IPv6Truncated     bool              `json:"ipv6_truncated"`
	RotationSupported bool              `json:"rotation_supported"`
	Capabilities      RelayCapabilities `json:"capabilities"`
}

type RelayRotationRequest struct {
	Enabled         bool `json:"enabled"`
	IntervalMinutes int  `json:"interval_minutes"`
}

type RelayRotationResult struct {
	PoolID        uint     `json:"pool_id"`
	Rotated       int      `json:"rotated"`
	IPv6          []string `json:"ipv6"`
	ItemIndex     int      `json:"item_index,omitempty"`
	RefreshToken  string   `json:"refresh_token,omitempty"`
	LastRotatedAt int64    `json:"last_rotated_at"`
	NextRotateAt  int64    `json:"next_rotate_at,omitempty"`
}

type RelayCapabilities struct {
	OS                string `json:"os"`
	CanAddSystemIPv6  bool   `json:"can_add_system_ipv6"`
	UnavailableReason string `json:"unavailable_reason,omitempty"`
}

func (s *ConfigService) GetRelayPools() ([]model.RelayPool, error) {
	var pools []model.RelayPool
	err := database.GetDB().Order("id desc").Find(&pools).Error
	if err != nil {
		return nil, err
	}
	return pools, nil
}

func (s *ConfigService) GetRelayIPv6() ([]RelayIPv6, error) {
	return discoverRelayIPv6()
}

func (s *ConfigService) GetRelayData() (*RelayData, error) {
	pools, err := s.GetRelayPools()
	if err != nil {
		return nil, err
	}
	if err := s.ensureRelayRefreshLinks(pools); err != nil {
		return nil, err
	}
	if err := s.populateRelayRefreshTokens(pools); err != nil {
		return nil, err
	}
	managed := make(map[string]bool)
	for _, pool := range pools {
		var items []model.RelayItem
		if json.Unmarshal(pool.Items, &items) != nil {
			continue
		}
		for _, item := range items {
			if item.IPv6 != "" {
				managed[item.IPv6] = true
			}
		}
	}
	ipv6, total, err := discoverRelayIPv6Summary(managed, relayIPv6DisplayLimit)
	if err != nil {
		return nil, err
	}
	return &RelayData{
		Pools: pools, IPv6: ipv6, IPv6Total: total, IPv6Truncated: total > len(ipv6), RotationSupported: true,
		Capabilities: getRelayCapabilities(),
	}, nil
}

func (s *ConfigService) ensureRelayRefreshLinks(pools []model.RelayPool) error {
	db := database.GetDB()
	return db.Transaction(func(tx *gorm.DB) error {
		poolIDs := make([]uint, 0, len(pools))
		for _, pool := range pools {
			poolIDs = append(poolIDs, pool.Id)
		}
		var links []model.RelayRefreshLink
		if len(poolIDs) > 0 {
			if err := tx.Where("pool_id IN ?", poolIDs).Find(&links).Error; err != nil {
				return err
			}
		}
		existing := make(map[string]bool, len(links))
		for _, link := range links {
			existing[fmt.Sprintf("%d:%s", link.PoolID, link.InboundTag)] = true
		}
		for _, pool := range pools {
			if !relayModeUsesIPv6(pool.Mode) {
				continue
			}
			var items []model.RelayItem
			if err := json.Unmarshal(pool.Items, &items); err != nil {
				return fmt.Errorf("relay pool %q: invalid items: %w", pool.Name, err)
			}
			for _, item := range items {
				if item.IPv6 == "" || item.InboundTag == "" {
					continue
				}
				key := fmt.Sprintf("%d:%s", pool.Id, item.InboundTag)
				if existing[key] {
					continue
				}
				link := model.RelayRefreshLink{
					PoolID: pool.Id, InboundTag: item.InboundTag, Token: common.Random(48), CreatedAt: time.Now().Unix(),
				}
				if err := tx.Create(&link).Error; err != nil {
					return err
				}
				existing[key] = true
			}
		}
		return nil
	})
}

func ensureRelayRefreshLinksTx(tx *gorm.DB, poolID uint, items []model.RelayItem) error {
	for _, item := range items {
		if item.IPv6 == "" || item.InboundTag == "" {
			continue
		}
		var link model.RelayRefreshLink
		err := tx.Where("pool_id = ? AND inbound_tag = ?", poolID, item.InboundTag).First(&link).Error
		if err == nil {
			continue
		}
		if !database.IsNotFound(err) {
			return err
		}
		link = model.RelayRefreshLink{
			PoolID: poolID, InboundTag: item.InboundTag, Token: common.Random(48), CreatedAt: time.Now().Unix(),
		}
		if err := tx.Create(&link).Error; err != nil {
			return err
		}
	}
	return nil
}

func (s *ConfigService) populateRelayRefreshTokens(pools []model.RelayPool) error {
	ids := make([]uint, 0, len(pools))
	for _, pool := range pools {
		ids = append(ids, pool.Id)
	}
	if len(ids) == 0 {
		return nil
	}
	var links []model.RelayRefreshLink
	if err := database.GetDB().Where("pool_id IN ?", ids).Find(&links).Error; err != nil {
		return err
	}
	byItem := make(map[string]string, len(links))
	for _, link := range links {
		byItem[fmt.Sprintf("%d:%s", link.PoolID, link.InboundTag)] = link.Token
	}
	for index := range pools {
		var items []model.RelayItem
		if err := json.Unmarshal(pools[index].Items, &items); err != nil {
			return fmt.Errorf("relay pool %q: invalid items: %w", pools[index].Name, err)
		}
		changed := false
		for itemIndex := range items {
			token := byItem[fmt.Sprintf("%d:%s", pools[index].Id, items[itemIndex].InboundTag)]
			if token != "" && items[itemIndex].RefreshToken != token {
				items[itemIndex].RefreshToken = token
				changed = true
			}
		}
		if changed {
			pools[index].Items = mustJSON(items)
		}
	}
	return nil
}

func (s *ConfigService) GetRelayBitBrowserExport(id uint, refreshBaseURL string) ([]byte, error) {
	var pool model.RelayPool
	if err := database.GetDB().First(&pool, id).Error; err != nil {
		return nil, err
	}
	pools := []model.RelayPool{pool}
	if err := s.ensureRelayRefreshLinks(pools); err != nil {
		return nil, err
	}
	if err := s.populateRelayRefreshTokens(pools); err != nil {
		return nil, err
	}
	return buildRelayBitBrowserWorkbook(pools[0], refreshBaseURL)
}

func buildRelayBitBrowserWorkbook(pool model.RelayPool, refreshBaseURL string) ([]byte, error) {
	var items []model.RelayItem
	if err := json.Unmarshal(pool.Items, &items); err != nil {
		return nil, common.NewError("invalid relay pool items: ", err.Error())
	}
	if len(items) == 0 {
		return nil, common.NewError("relay pool has no items")
	}

	file := excelize.NewFile()
	defer file.Close()
	const sheet = "批量导入窗口"
	if err := file.SetSheetName("Sheet1", sheet); err != nil {
		return nil, err
	}
	file.SetActiveSheet(0)

	headers := []string{
		"窗口名称", "用户名", "密码", "cookie", "代理类型", "代理信息",
		"国家/地区（针对动态IP）", "州/省（针对动态IP）", "城市（针对动态IP）",
		"窗口备注", "User Agent", "窗口尺寸",
	}
	descriptions := []string{
		"必填；用于区分浏览器窗口", "网站登录用户名（可选）", "网站登录密码（可选）",
		"支持 JSON/Netscape/Name=Value 格式（可选）", "填写 socks5",
		"代理主机:代理端口:代理用户名:代理密码；IPv6 在最前增加 ipv6:",
		"动态代理可选", "动态代理可选", "动态代理可选", "有 IPv6 轮转功能时显示该窗口独立轮转链接", "可选", "例如 1920*1030（可选）",
	}
	for column := 1; column <= len(headers); column++ {
		cell, _ := excelize.CoordinatesToCellName(column, 1)
		file.SetCellStr(sheet, cell, headers[column-1])
		cell, _ = excelize.CoordinatesToCellName(column, 2)
		file.SetCellStr(sheet, cell, descriptions[column-1])
	}
	file.SetCellStr(sheet, "A3", "说明：数据从第4行开始导入；代理账号和密码必须写在“代理信息”列。")

	poolName := sanitizeBitBrowserCell(pool.Name)
	if poolName == "" {
		poolName = "1S-UI Relay"
	}
	for itemIndex, item := range items {
		proxyInfo, err := relayBitBrowserProxyInfo(pool, item)
		if err != nil {
			return nil, common.NewErrorf("relay item %d: %v", itemIndex+1, err)
		}
		row := itemIndex + 4
		windowName := fmt.Sprintf("%s-%03d", poolName, itemIndex+1)
		refreshURL := relayItemRefreshURL(refreshBaseURL, item.RefreshToken)
		note := "1S-UI " + poolName
		if refreshURL != "" {
			note = refreshURL
		}
		values := []string{windowName, "", "", "", "socks5", proxyInfo, "", "", "", note, "", ""}
		for column, value := range values {
			cell, _ := excelize.CoordinatesToCellName(column+1, row)
			file.SetCellStr(sheet, cell, value)
		}
		if refreshURL != "" {
			cell, _ := excelize.CoordinatesToCellName(10, row)
			if err := file.SetCellHyperLink(sheet, cell, refreshURL, "External"); err != nil {
				return nil, err
			}
		}
	}

	headerStyle, err := file.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Color: "#102A43"},
		Fill:      excelize.Fill{Type: "pattern", Color: []string{"#A9D6F5"}, Pattern: 1},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center", WrapText: true},
		Border:    []excelize.Border{{Type: "left", Color: "#7A8A99", Style: 1}, {Type: "top", Color: "#7A8A99", Style: 1}, {Type: "right", Color: "#7A8A99", Style: 1}, {Type: "bottom", Color: "#7A8A99", Style: 1}},
	})
	if err != nil {
		return nil, err
	}
	instructionStyle, err := file.NewStyle(&excelize.Style{
		Alignment: &excelize.Alignment{Vertical: "top", WrapText: true},
		Border:    []excelize.Border{{Type: "left", Color: "#B8C2CC", Style: 1}, {Type: "top", Color: "#B8C2CC", Style: 1}, {Type: "right", Color: "#B8C2CC", Style: 1}, {Type: "bottom", Color: "#B8C2CC", Style: 1}},
	})
	if err != nil {
		return nil, err
	}
	warningStyle, err := file.NewStyle(&excelize.Style{Font: &excelize.Font{Color: "#E53935", Bold: true}})
	if err != nil {
		return nil, err
	}
	file.SetCellStyle(sheet, "A1", "L1", headerStyle)
	file.SetCellStyle(sheet, "A2", "L2", instructionStyle)
	file.SetCellStyle(sheet, "A3", "A3", warningStyle)
	file.SetRowHeight(sheet, 1, 24)
	file.SetRowHeight(sheet, 2, 72)
	file.SetRowHeight(sheet, 3, 22)
	file.SetColWidth(sheet, "A", "A", 24)
	file.SetColWidth(sheet, "B", "D", 18)
	file.SetColWidth(sheet, "E", "E", 14)
	file.SetColWidth(sheet, "F", "F", 54)
	file.SetColWidth(sheet, "G", "I", 22)
	file.SetColWidth(sheet, "J", "J", 54)
	file.SetColWidth(sheet, "K", "L", 22)
	if err := file.SetPanes(sheet, &excelize.Panes{Freeze: true, YSplit: 3, TopLeftCell: "A4", ActivePane: "bottomLeft"}); err != nil {
		return nil, err
	}

	buffer, err := file.WriteToBuffer()
	if err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func relayItemRefreshURL(baseURL, token string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	token = strings.TrimSpace(token)
	if baseURL == "" || token == "" {
		return ""
	}
	return baseURL + "/refresh/" + token
}

func relayBitBrowserProxyInfo(pool model.RelayPool, item model.RelayItem) (string, error) {
	protocol := item.Protocol
	if protocol == "" {
		protocol = pool.Protocol
	}
	if protocol == "" {
		protocol = "socks"
	}
	if protocol != "socks" && protocol != "mixed" {
		return "", common.NewErrorf("BitBrowser export only supports SOCKS5 or Mixed, got %q", protocol)
	}
	host := pool.ListenHost
	proxyInfo := relaySOCKSExport(pool.ListenHost, item.ListenPort, item.Username, item.Password)
	if address, err := netip.ParseAddr(strings.Trim(host, "[]")); err == nil && address.Is6() {
		return "ipv6:" + proxyInfo, nil
	}
	return proxyInfo, nil
}

func sanitizeBitBrowserCell(value string) string {
	value = strings.TrimSpace(strings.NewReplacer("\r", " ", "\n", " ", "\t", " ").Replace(value))
	if len([]rune(value)) > 60 {
		value = string([]rune(value)[:60])
	}
	return value
}

func getRelayCapabilities() RelayCapabilities {
	_, ipCommandErr := exec.LookPath("ip")
	return buildRelayCapabilities(runtime.GOOS, relayHasRoot(), ipCommandErr == nil)
}

func buildRelayCapabilities(goos string, hasRoot, hasIPCommand bool) RelayCapabilities {
	capabilities := RelayCapabilities{OS: goos}
	switch {
	case goos != "linux":
		capabilities.UnavailableReason = "unsupported_os"
	case !hasRoot:
		capabilities.UnavailableReason = "root_required"
	case !hasIPCommand:
		capabilities.UnavailableReason = "iproute2_required"
	default:
		capabilities.CanAddSystemIPv6 = true
	}
	return capabilities
}

func (s *ConfigService) RestoreRelayIPv6() error {
	relayMu.Lock()
	defer relayMu.Unlock()

	if err := database.GetDB().Model(&model.RelayPool{}).
		Where("rotation_enabled = ? OR next_rotate_at <> 0", true).
		Updates(map[string]interface{}{"rotation_enabled": false, "next_rotate_at": 0}).Error; err != nil {
		return err
	}
	pools, err := s.GetRelayPools()
	if err != nil {
		return err
	}
	if err := s.ensureRelayRefreshLinks(pools); err != nil {
		return err
	}
	if runtime.GOOS != "linux" {
		return nil
	}
	if err := s.repairRelayIPv6OutboundStrategies(); err != nil {
		return err
	}
	detected, err := discoverRelayIPv6()
	if err != nil {
		return err
	}
	existing := make(map[string]bool, len(detected))
	for _, address := range detected {
		existing[address.Address] = true
	}
	restored := make([]model.RelayItem, 0)
	for _, pool := range pools {
		var items []model.RelayItem
		if err := json.Unmarshal(pool.Items, &items); err != nil {
			return fmt.Errorf("relay pool %q: invalid items: %w", pool.Name, err)
		}
		for _, item := range items {
			if !item.AddedByUs || item.IPv6 == "" {
				continue
			}
			if !existing[item.IPv6] {
				if err := addRelayAddress(item.Interface, item.IPv6, item.Prefix); err != nil {
					logger.Warningf("restore relay IPv6 %s: %v", item.IPv6, err)
					continue
				}
				existing[item.IPv6] = true
			}
			restored = append(restored, item)
		}
	}
	if err := waitRelayAddressesReady(restored); err != nil {
		// Do not remove an address here.  A temporary DAD/routing failure after
		// a VPS restart must not erase the address from the persisted relay pool.
		// The periodic health job will retry once the interface is ready.
		logger.Warning("relay IPv6 readiness check is not complete; keeping persisted addresses for retry: ", err)
	}
	return nil
}

// EnsureRelayIPv6Addresses re-adds IPv6 addresses owned by relay items when a
// network restart or provider reboot removed them from the interface. It is
// intentionally non-destructive: transient network/DAD failures must not
// delete the addresses from the database.
func (s *ConfigService) EnsureRelayIPv6Addresses() error {
	if runtime.GOOS != "linux" {
		return nil
	}
	relayMu.Lock()
	defer relayMu.Unlock()

	pools, err := s.GetRelayPools()
	if err != nil {
		return err
	}
	detected, err := discoverRelayIPv6()
	if err != nil {
		return err
	}
	existing := make(map[string]bool, len(detected))
	for _, address := range detected {
		existing[address.Address] = true
	}
	restored := make([]model.RelayItem, 0)
	for _, pool := range pools {
		var items []model.RelayItem
		if err := json.Unmarshal(pool.Items, &items); err != nil {
			return fmt.Errorf("relay pool %q: invalid items: %w", pool.Name, err)
		}
		for _, item := range items {
			if !item.AddedByUs || item.IPv6 == "" || item.Interface == "" {
				continue
			}
			if !existing[item.IPv6] {
				if err := addRelayAddress(item.Interface, item.IPv6, item.Prefix); err != nil {
					logger.Warningf("ensure relay IPv6 %s: %v", item.IPv6, err)
					continue
				}
				existing[item.IPv6] = true
			}
			restored = append(restored, item)
		}
	}
	if err := waitRelayAddressesReady(restored); err != nil {
		logger.Warning("relay IPv6 addresses are pending readiness; will retry automatically: ", err)
	}
	return nil
}

// repairRelayIPv6OutboundStrategies upgrades relay pools created before the
// address-family selector existed. It runs before sing-box starts, so the
// repaired options are included in the first runtime configuration.
func (s *ConfigService) repairRelayIPv6OutboundStrategies() error {
	db := database.GetDB()
	var pools []model.RelayPool
	if err := db.Where("mode = ?", relayModeIPv6).Find(&pools).Error; err != nil {
		return err
	}
	// Paired and dual-stack pools created before the Apple ID route was added
	// need their generated IPv4 rules backfilled on the next startup.
	var pairedPools []model.RelayPool
	if err := db.Where("mode IN ?", []string{relayModePaired, relayModeDualStack}).Find(&pairedPools).Error; err != nil {
		return err
	}
	return db.Transaction(func(tx *gorm.DB) error {
		var ipv6OnlyItems, dualStackItems []model.RelayItem
		for _, pool := range pools {
			strategy, err := normalizeRelayDomainStrategy(relayModeIPv6, pool.DomainStrategy)
			if err != nil {
				return fmt.Errorf("relay pool %q: %w", pool.Name, err)
			}
			if pool.DomainStrategy != strategy {
				if err := tx.Model(&model.RelayPool{}).Where("id = ?", pool.Id).Update("domain_strategy", strategy).Error; err != nil {
					return err
				}
			}
			var items []model.RelayItem
			if err := json.Unmarshal(pool.Items, &items); err != nil {
				return fmt.Errorf("relay pool %q: invalid items: %w", pool.Name, err)
			}
			if strategy == relayDomainStrategyIPv6Only {
				ipv6OnlyItems = append(ipv6OnlyItems, items...)
			} else {
				dualStackItems = append(dualStackItems, items...)
			}
			for _, item := range items {
				if item.IPv6 == "" || item.OutboundTag == "" {
					continue
				}
				var outbound model.Outbound
				if err := tx.Where("tag = ?", item.OutboundTag).First(&outbound).Error; err != nil {
					if database.IsNotFound(err) {
						logger.Warningf("relay pool %q outbound %q was not found", pool.Name, item.OutboundTag)
						continue
					}
					return err
				}
				if outbound.Type != "direct" {
					logger.Warningf("relay pool %q outbound %q is %s, leaving it unchanged", pool.Name, item.OutboundTag, outbound.Type)
					continue
				}
				var options map[string]interface{}
				if len(outbound.Options) > 0 {
					if err := json.Unmarshal(outbound.Options, &options); err != nil {
						return err
					}
				}
				if options == nil {
					options = map[string]interface{}{}
				}
				if options["inet6_bind_address"] == item.IPv6 && options["domain_strategy"] == strategy {
					continue
				}
				options["inet6_bind_address"] = item.IPv6
				options["domain_strategy"] = strategy
				if err := tx.Model(&model.Outbound{}).Where("id = ?", outbound.Id).Update("options", mustJSON(options)).Error; err != nil {
					return err
				}
			}
			if err := repairRelayIPv6ConnectionHost(tx, pool, items); err != nil {
				return err
			}
		}
		for _, pool := range pairedPools {
			var items []model.RelayItem
			if err := json.Unmarshal(pool.Items, &items); err != nil {
				return fmt.Errorf("relay pool %q: invalid items: %w", pool.Name, err)
			}
			// Rebuild every paired IPv4 SOCKS5 outbound from the upstream
			// credentials persisted on its own RelayItem.  This repairs pools
			// created by older versions where the runtime configuration could
			// retain one shared upstream even though the items were different.
			usedIPv4Tags := make(map[string]bool, len(items))
			itemsChanged := false
			for index := range items {
				item := &items[index]
				if item.IPv4OutboundTag == "" || item.UpstreamServer == "" || item.UpstreamPort < 1 {
					continue
				}
				var ipv4Outbound model.Outbound
				if err := tx.Where("tag = ?", item.IPv4OutboundTag).First(&ipv4Outbound).Error; err != nil {
					if database.IsNotFound(err) {
						logger.Warningf("relay pool %q item %d IPv4 outbound %q was not found", pool.Name, index+1, item.IPv4OutboundTag)
						continue
					}
					return err
				}
				if ipv4Outbound.Type != "socks" {
					return fmt.Errorf("relay pool %q item %d IPv4 outbound %q is %s, expected socks", pool.Name, index+1, item.IPv4OutboundTag, ipv4Outbound.Type)
				}
				desired := mustJSON(map[string]interface{}{
					"server": item.UpstreamServer, "server_port": item.UpstreamPort,
					"version": "5", "username": item.UpstreamUsername, "password": item.UpstreamPassword,
					"domain_strategy": relayDomainStrategyIPv4Only,
				})
				// A buggy/very old pool may have several items pointing at one
				// IPv4 outbound tag. Never overwrite that shared outbound: clone
				// it for this item and persist the new tag in the pool mapping.
				if usedIPv4Tags[item.IPv4OutboundTag] {
					clone := model.Outbound{Type: "socks", Tag: fmt.Sprintf("relay-ipv4-%s", common.Random(7)), Options: desired}
					if err := tx.Create(&clone).Error; err != nil {
						return err
					}
					item.IPv4OutboundTag = clone.Tag
					ipv4Outbound = clone
					itemsChanged = true
				}
				usedIPv4Tags[item.IPv4OutboundTag] = true
				if string(ipv4Outbound.Options) != string(desired) {
					if err := tx.Model(&model.Outbound{}).Where("id = ?", ipv4Outbound.Id).Update("options", desired).Error; err != nil {
						return err
					}
				}
			}
			if itemsChanged {
				if err := tx.Model(&model.RelayPool{}).Where("id = ?", pool.Id).Update("items", mustJSON(items)).Error; err != nil {
					return err
				}
			}
			// Older paired/dual-stack pools may have been created before the
			// Apple-ID-only switch was introduced.  Keep their generated IPv6
			// direct outbounds IPv6-only whenever the item is marked for the
			// Apple-ID IPv4 exception; otherwise a pure IPv4 hostname could be
			// resolved and sent through the VPS's native IPv4 route.
			for _, item := range items {
				if !item.AppleIDIPv4Only || item.IPv6 == "" || item.OutboundTag == "" {
					continue
				}
				var outbound model.Outbound
				if err := tx.Where("tag = ?", item.OutboundTag).First(&outbound).Error; err != nil {
					if database.IsNotFound(err) {
						continue
					}
					return err
				}
				if outbound.Type != "direct" {
					continue
				}
				var options map[string]interface{}
				if len(outbound.Options) > 0 {
					if err := json.Unmarshal(outbound.Options, &options); err != nil {
						return err
					}
				}
				if options == nil {
					options = map[string]interface{}{}
				}
				options["inet6_bind_address"] = item.IPv6
				options["domain_strategy"] = relayDomainStrategyIPv6Only
				if err := tx.Model(&model.Outbound{}).Where("id = ?", outbound.Id).Update("options", mustJSON(options)).Error; err != nil {
					return err
				}
			}
			dualStackItems = append(dualStackItems, items...)
		}
		if len(dualStackItems) > 0 {
			if err := updateRelayRouteRules(tx, dualStackItems, false, false); err != nil {
				return err
			}
		}
		if len(ipv6OnlyItems) > 0 {
			if err := updateRelayRouteRules(tx, ipv6OnlyItems, true, false); err != nil {
				return err
			}
		}
		return nil
	})
}

// repairRelayIPv6ConnectionHost upgrades pools that exposed each generated
// IPv6 as the client endpoint. The generated address belongs only on the
// direct outbound; clients connect to the pool's public host instead.
func repairRelayIPv6ConnectionHost(tx *gorm.DB, pool model.RelayPool, items []model.RelayItem) error {
	if strings.TrimSpace(pool.ListenHost) == "" {
		return nil
	}
	publicHost, err := normalizeRelayPublicHost(pool.ListenHost)
	if err != nil {
		logger.Warningf("relay pool %q has invalid public host %q; connection host was not repaired", pool.Name, pool.ListenHost)
		return nil
	}

	itemsChanged := false
	for index := range items {
		item := &items[index]
		protocol := item.Protocol
		if protocol == "" {
			protocol = pool.Protocol
		}
		if protocol == "" {
			protocol = "socks"
		}
		req := RelayCreateRequest{Mode: relayModeIPv6, Protocol: protocol}

		var inbound *model.Inbound
		if item.InboundID > 0 {
			current := model.Inbound{}
			if err := tx.Preload("Tls").First(&current, item.InboundID).Error; err != nil {
				if database.IsNotFound(err) {
					logger.Warningf("relay pool %q inbound %d was not found", pool.Name, item.InboundID)
				} else {
					return err
				}
			} else {
				var options map[string]interface{}
				if err := json.Unmarshal(current.Options, &options); err != nil {
					return fmt.Errorf("relay pool %q inbound %d has invalid options: %w", pool.Name, item.InboundID, err)
				}
				desiredListen := relayInboundListenAddress(relayModeIPv6, publicHost)
				desiredAddrs := mustJSON([]map[string]interface{}{{
					"server": publicHost, "server_port": item.ListenPort,
				}})
				inboundUpdates := map[string]interface{}{}
				if fmt.Sprint(options["listen"]) != desiredListen {
					options["listen"] = desiredListen
					current.Options = mustJSON(options)
					inboundUpdates["options"] = current.Options
				}
				if string(current.Addrs) != string(desiredAddrs) {
					current.Addrs = desiredAddrs
					inboundUpdates["addrs"] = current.Addrs
				}
				if len(inboundUpdates) > 0 {
					if err := tx.Model(&model.Inbound{}).Where("id = ?", current.Id).Updates(inboundUpdates).Error; err != nil {
						return err
					}
				}
				inbound = &current
			}
		}

		var links []map[string]string
		if item.ClientID > 0 && inbound != nil {
			var client model.Client
			if err := tx.First(&client, item.ClientID).Error; err != nil {
				if database.IsNotFound(err) {
					logger.Warningf("relay pool %q client %d was not found", pool.Name, item.ClientID)
				} else {
					return err
				}
			} else {
				var clientConfig map[string]interface{}
				if err := json.Unmarshal(client.Config, &clientConfig); err != nil {
					return fmt.Errorf("relay pool %q client %d has invalid config: %w", pool.Name, item.ClientID, err)
				}
				links = relayClientLinks(req, *inbound, clientConfig, publicHost)
				desiredLinks := mustJSON(links)
				if string(client.Links) != string(desiredLinks) {
					if err := tx.Model(&model.Client{}).Where("id = ?", client.Id).Update("links", desiredLinks).Error; err != nil {
						return err
					}
				}
			}
		}

		desiredExport := item.Export
		if protocol == "socks" || protocol == "mixed" {
			desiredExport = relaySOCKSExport(publicHost, item.ListenPort, item.Username, item.Password)
		} else if len(links) > 0 {
			desiredExport = links[0]["uri"]
		}
		if item.Export != desiredExport {
			item.Export = desiredExport
			itemsChanged = true
		}
	}
	if itemsChanged {
		return tx.Model(&model.RelayPool{}).Where("id = ?", pool.Id).Update("items", mustJSON(items)).Error
	}
	return nil
}

func (s *ConfigService) CreateRelay(req RelayCreateRequest, actor, publicHost string) (*model.RelayPool, error) {
	relayMu.Lock()
	defer relayMu.Unlock()

	if err := applyRelaySourcePreset(&req); err != nil {
		return nil, err
	}
	req.Mode = strings.ToLower(strings.TrimSpace(req.Mode))
	if req.Mode != relayModeIPv6 && req.Mode != relayModeUpstream && req.Mode != relayModePaired && req.Mode != relayModeDualStack {
		return nil, common.NewError("relay mode must be ipv6, upstream, paired, or dualstack")
	}
	if relayModeUsesUpstream(req.Mode) && len(req.Upstreams) == 0 {
		parsed, parseErr := parseRelayUpstreams(req.UpstreamText)
		if parseErr != nil {
			return nil, parseErr
		}
		req.Upstreams = parsed
	}
	if req.Mode == relayModeIPv6 && req.Count <= 0 {
		return nil, common.NewError("relay count must be greater than zero")
	}
	if relayModeUsesUpstream(req.Mode) {
		for i := range req.Upstreams {
			req.Upstreams[i].Server = strings.Trim(req.Upstreams[i].Server, "[]")
		}
		req.Count = len(req.Upstreams)
	}
	var strategyErr error
	req.DomainStrategy, strategyErr = normalizeRelayDomainStrategy(req.Mode, req.DomainStrategy)
	if strategyErr != nil {
		return nil, strategyErr
	}
	req.Protocol = strings.ToLower(strings.TrimSpace(req.Protocol))
	if req.Protocol == "" {
		req.Protocol = "socks"
	}
	if !relayProtocols[req.Protocol] {
		return nil, common.NewErrorf("relay protocol %q is not supported", req.Protocol)
	}
	req.CoreType = strings.TrimSpace(req.CoreType)
	if req.CoreType == "" {
		req.CoreType = relayCoreSingBox
	}
	if req.CoreType != relayCoreSingBox {
		return nil, common.NewError("relay batches currently require sing-box; Xray inbound batches use a separate routing model")
	}
	if relayTLSProtocols[req.Protocol] && req.TlsID == 0 {
		return nil, common.NewErrorf("relay protocol %s requires an existing TLS configuration", req.Protocol)
	}
	if req.TlsID > 0 && !relayTLSSupportedProtocols[req.Protocol] {
		return nil, common.NewErrorf("relay protocol %s does not support TLS", req.Protocol)
	}
	if req.Transport == "" {
		req.Transport = "http"
	}
	if req.Protocol != "vless" && req.Protocol != "vmess" && req.Protocol != "trojan" {
		req.Transport = ""
	}
	if req.ShadowsocksMethod == "" {
		req.ShadowsocksMethod = "2022-blake3-aes-256-gcm"
	}
	if req.Protocol == "shadowsocks" && !relayShadowsocksMethods[req.ShadowsocksMethod] {
		return nil, common.NewError("unsupported Shadowsocks method")
	}
	if req.Count > maxRelayItems {
		return nil, common.NewErrorf("relay count cannot exceed %d", maxRelayItems)
	}
	if req.PortStart < 1 || req.PortStart > 65535 || req.Count > 65535-req.PortStart+1 {
		return nil, common.NewError("relay port range is invalid")
	}
	if req.PasswordLength < 8 || req.PasswordLength > 64 {
		req.PasswordLength = 12
	}
	if strings.TrimSpace(req.UsernamePrefix) == "" {
		req.UsernamePrefix = "relay"
	}
	if strings.TrimSpace(req.Name) == "" {
		req.Name = "relay-" + common.Random(5)
	}
	requestedHost := strings.TrimSpace(req.PublicHost)
	if requestedHost == "" {
		requestedHost = strings.TrimSpace(publicHost)
	}
	if requestedHost == "" {
		requestedHost = "127.0.0.1"
	}
	var err error
	publicHost, err = normalizeRelayPublicHost(requestedHost)
	if err != nil {
		return nil, err
	}
	req.PublicHost = publicHost

	items, err := s.prepareRelayItems(req)
	if err != nil {
		return nil, err
	}
	// Pairing must be based on the prepared item, not on a second lookup into
	// the request slice.  This keeps the IPv6/upstream relationship stable
	// even when a remote agent or an older frontend sends an empty/rewritten
	// `upstreams` array together with `upstream_text`.
	if relayModePairsUpstream(req.Mode) {
		if len(items) != len(req.Upstreams) {
			return nil, common.NewErrorf("relay pairing requires exactly one upstream for each IPv6 item (got %d items, %d upstreams)", len(items), len(req.Upstreams))
		}
		for i := range items {
			if items[i].UpstreamServer == "" || items[i].UpstreamPort < 1 {
				return nil, common.NewErrorf("relay pairing item %d has no upstream SOCKS5 endpoint", i+1)
			}
		}
	}
	added := make([]model.RelayItem, 0)
	cleanup := true
	defer func() {
		if cleanup {
			for _, item := range added {
				if item.AddedByUs {
					_ = deleteRelayAddress(item.Interface, item.IPv6, item.Prefix)
				}
			}
		}
	}()
	existingAddresses := make(map[string]bool)
	if relayModeUsesIPv6(req.Mode) {
		detected, discoverErr := discoverRelayIPv6()
		if discoverErr != nil {
			return nil, discoverErr
		}
		for _, address := range detected {
			existingAddresses[address.Address] = true
		}
	}
	if relayModePairsUpstream(req.Mode) && runtime.GOOS == "linux" {
		added, err = fillPairedRelayIPv6(items, existingAddresses, req.AddSystemAddresses)
		if err != nil {
			return nil, err
		}
	} else {
		for i := range items {
			if items[i].IPv6 == "" {
				continue
			}
			already := existingAddresses[items[i].IPv6]
			if !already && !req.AddSystemAddresses {
				return nil, common.NewErrorf("IPv6 %s is not currently assigned; enable system address creation", items[i].IPv6)
			}
			if !already {
				if err := addRelayAddress(items[i].Interface, items[i].IPv6, items[i].Prefix); err != nil {
					return nil, err
				}
				items[i].AddedByUs = true
				added = append(added, items[i])
				existingAddresses[items[i].IPv6] = true
			}
		}
		if err := waitRelayAddressesReady(items); err != nil {
			return nil, err
		}
		if relayModeUsesIPv6(req.Mode) && runtime.GOOS == "linux" {
			if err := validateRelayIPv6Egress(context.Background(), items, probeRelayIPv6Egress); err != nil {
				return nil, err
			}
		}
	}

	db := database.GetDB()
	tx := db.Begin()
	if tx.Error != nil {
		return nil, tx.Error
	}
	committed := false
	var oldConfig []byte
	var newConfig []byte
	defer func() {
		if !committed {
			_ = tx.Rollback().Error
			if len(oldConfig) > 0 && corePtr != nil {
				if restoreErr := s.restoreSingBoxConfig(oldConfig); restoreErr != nil {
					logger.Error("restore core after relay create failed: ", restoreErr)
				}
			}
		}
	}()

	usedPorts, err := relayUsedPorts(tx)
	if err != nil {
		return nil, err
	}
	portStart, err := nextRelayPortRange(usedPorts, req.PortStart, req.Count)
	if err != nil {
		return nil, err
	}
	req.PortStart = portStart
	for i := range items {
		items[i].ListenPort = portStart + i
		usedPorts[items[i].ListenPort] = true
	}

	pool := model.RelayPool{
		Name:           req.Name,
		Source:         req.Source,
		Mode:           req.Mode,
		Protocol:       req.Protocol,
		CoreType:       req.CoreType,
		TlsID:          req.TlsID,
		Transport:      req.Transport,
		DomainStrategy: req.DomainStrategy,
		ListenHost:     publicHost,
		PortStart:      req.PortStart,
		Count:          req.Count,
		CreatedAt:      time.Now().Unix(),
	}
	var relayTLS *model.Tls
	if req.TlsID > 0 {
		relayTLS = &model.Tls{}
		if err := tx.First(relayTLS, req.TlsID).Error; err != nil {
			return nil, common.NewErrorf("TLS configuration %d was not found", req.TlsID)
		}
	}
	for i := range items {
		listenAddress := "::"
		if relayModeUsesIPv6(req.Mode) {
			listenAddress = relayInboundListenAddress(req.Mode, publicHost)
		}
		inboundOptions, clientConfig, err := relayProtocolConfig(req, &items[i])
		if err != nil {
			return nil, err
		}
		inbound := model.Inbound{
			Type:     req.Protocol,
			Tag:      fmt.Sprintf("relay-%s-%d", common.Random(5), items[i].ListenPort),
			CoreType: req.CoreType,
			Addrs:    mustJSON([]map[string]interface{}{{"server": publicHost, "server_port": items[i].ListenPort}}),
			OutJson:  json.RawMessage("{}"),
			TlsId:    req.TlsID,
			Options:  mustJSON(relayInboundOptions(req, inboundOptions, listenAddress, items[i].ListenPort)),
		}
		inbound.Tls = relayTLS
		if err := tx.Create(&inbound).Error; err != nil {
			return nil, err
		}
		items[i].InboundID = inbound.Id
		items[i].InboundTag = inbound.Tag
		outbound := model.Outbound{
			Type: "direct",
			Tag:  fmt.Sprintf("relay-out-%s", common.Random(7)),
		}
		if req.Mode == relayModeUpstream {
			outbound.Type = "socks"
			outbound.Options, err = json.Marshal(map[string]interface{}{
				"server": items[i].UpstreamServer, "server_port": items[i].UpstreamPort,
				"version": "5", "username": items[i].UpstreamUsername, "password": items[i].UpstreamPassword,
			})
		} else {
			outbound.Options, err = json.Marshal(relayDirectOutboundOptions(req, items[i]))
		}
		if err != nil {
			return nil, err
		}
		if err := tx.Create(&outbound).Error; err != nil {
			return nil, err
		}
		items[i].OutboundTag = outbound.Tag
		if relayModePairsUpstream(req.Mode) {
			items[i].AppleIDIPv4Only = req.AppleIDIPv4Only
			ipv4Outbound := model.Outbound{
				Type: "socks",
				Tag:  fmt.Sprintf("relay-ipv4-%s", common.Random(7)),
				Options: mustJSON(map[string]interface{}{
					"server": items[i].UpstreamServer, "server_port": items[i].UpstreamPort,
					"version": "5", "username": items[i].UpstreamUsername, "password": items[i].UpstreamPassword,
					"domain_strategy": relayDomainStrategyIPv4Only,
				}),
			}
			if err := tx.Create(&ipv4Outbound).Error; err != nil {
				return nil, err
			}
			items[i].IPv4OutboundTag = ipv4Outbound.Tag
			if req.Mode == relayModeDualStack {
				items[i].IPv6OutboundTag = outbound.Tag
				fallbackOutbound := model.Outbound{
					Type: core.RelayFallbackOutboundType,
					Tag:  fmt.Sprintf("relay-dual-%s", common.Random(7)),
					Options: mustJSON(map[string]interface{}{
						"ipv6_outbound": items[i].IPv6OutboundTag,
						"ipv4_outbound": items[i].IPv4OutboundTag,
						"ipv6_timeout":  relayDualStackIPv6Timeout,
					}),
				}
				if err := tx.Create(&fallbackOutbound).Error; err != nil {
					return nil, err
				}
				items[i].OutboundTag = fallbackOutbound.Tag
			}
		}
		client := model.Client{
			Enable: true, Name: items[i].Username,
			Config:    mustJSON(clientConfig),
			Inbounds:  mustJSON([]uint{inbound.Id}),
			Links:     mustJSON(relayClientLinks(req, inbound, clientConfig, publicHost)),
			CreatedAt: time.Now().Unix(),
		}
		if err := tx.Create(&client).Error; err != nil {
			return nil, err
		}
		items[i].ClientID = client.Id
		items[i].Protocol = req.Protocol
		items[i].Method = req.ShadowsocksMethod
		if cfg, ok := clientConfig[req.Protocol].(map[string]interface{}); ok {
			items[i].UUID, _ = cfg["uuid"].(string)
		}
		if req.Protocol == "socks" || req.Protocol == "mixed" {
			// Browser-oriented SOCKS importers commonly expect four colon-separated
			// fields rather than a socks5:// URI.
			items[i].Export = relaySOCKSExport(publicHost, items[i].ListenPort, items[i].Username, items[i].Password)
		} else {
			items[i].Export = relayClientLink(req, inbound, clientConfig, publicHost)
		}
	}
	pool.Items = mustJSON(items)
	if err := tx.Create(&pool).Error; err != nil {
		return nil, err
	}
	if err := ensureRelayRefreshLinksTx(tx, pool.Id, items); err != nil {
		return nil, err
	}
	if err := updateRelayRouteRules(tx, items, req.DomainStrategy == relayDomainStrategyIPv6Only, false); err != nil {
		return nil, err
	}
	pool.Items = mustJSON(items)
	if err := tx.Model(&model.RelayPool{}).Where("id = ?", pool.Id).Update("items", pool.Items).Error; err != nil {
		return nil, err
	}

	if corePtr != nil && corePtr.IsRunning() {
		oldConfigPtr, err := s.GetConfig("")
		if err != nil {
			return nil, err
		}
		oldConfig = *oldConfigPtr
		newConfigPtr, err := s.GetConfigWithDB("", tx)
		if err != nil {
			return nil, err
		}
		if err = corePtr.Stop(); err != nil {
			return nil, err
		}
		newConfig = *newConfigPtr
		if err = corePtr.Start(newConfig); err != nil {
			return nil, common.NewErrorf("relay configuration rejected by sing-box: %v", err)
		}
	}
	changeData := mustJSON(req)
	if err := tx.Create(&model.Changes{DateTime: time.Now().Unix(), Actor: actor, Key: "relay", Action: "create", Obj: changeData}).Error; err != nil {
		return nil, err
	}
	if err := tx.Commit().Error; err != nil {
		return nil, err
	}
	committed = true
	cleanup = false
	LastUpdate.Store(time.Now().UnixMilli())
	if corePtr != nil && !corePtr.IsRunning() {
		if err := s.StartCore(); err != nil {
			return &pool, common.NewErrorf("relay saved, but core update failed: %v", err)
		}
	}
	returnedPools := []model.RelayPool{pool}
	if err := s.populateRelayRefreshTokens(returnedPools); err == nil {
		// Return the generated per-item links without storing bearer tokens in
		// the large relay pool JSON column.
		pool = returnedPools[0]
	} else {
		logger.Warning("populate relay refresh tokens after create failed: ", err)
	}
	return &pool, nil
}

func normalizeRelayDomainStrategy(mode, value string) (string, error) {
	if mode == relayModePaired || mode == relayModeDualStack {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" || value == relayDomainStrategyPreferIPv6 {
			return relayDomainStrategyPreferIPv6, nil
		}
		return "", common.NewError("paired and dualstack relay modes require prefer_ipv6 domain strategy")
	}
	if mode != relayModeIPv6 {
		return "", nil
	}
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return relayDomainStrategyIPv6Only, nil
	}
	switch value {
	case relayDomainStrategyIPv6Only, relayDomainStrategyPreferIPv6:
		return value, nil
	default:
		return "", common.NewErrorf("unsupported IPv6 address strategy %q", value)
	}
}

func relayDirectOutboundOptions(req RelayCreateRequest, item model.RelayItem) map[string]interface{} {
	strategy := req.DomainStrategy
	if req.Mode == relayModeDualStack || req.AppleIDIPv4Only {
		strategy = relayDomainStrategyIPv6Only
	}
	if strategy == "" {
		strategy = relayDomainStrategyIPv6Only
	}
	return map[string]interface{}{
		"inet6_bind_address": item.IPv6,
		"domain_strategy":    strategy,
	}
}

func applyRelaySourcePreset(req *RelayCreateRequest) error {
	req.Source = strings.TrimSpace(req.Source)
	switch req.Source {
	case "":
		return nil
	case relaySourceAutoAddIPv6:
		req.Mode = relayModeIPv6
		req.Protocol = "socks"
		req.CoreType = relayCoreSingBox
		req.AddSystemAddresses = true
		req.TlsID = 0
		req.Transport = ""
		return nil
	default:
		return common.NewErrorf("unsupported relay source %q", req.Source)
	}
}

func (s *ConfigService) DeleteRelay(id uint, actor string) error {
	relayMu.Lock()
	defer relayMu.Unlock()

	var pool model.RelayPool
	db := database.GetDB()
	if err := db.First(&pool, id).Error; err != nil {
		return err
	}
	var items []model.RelayItem
	if err := json.Unmarshal(pool.Items, &items); err != nil {
		return err
	}
	tx := db.Begin()
	if tx.Error != nil {
		return tx.Error
	}
	committed := false
	var oldConfig []byte
	defer func() {
		if !committed {
			_ = tx.Rollback().Error
			if len(oldConfig) > 0 && corePtr != nil {
				if restoreErr := s.restoreSingBoxConfig(oldConfig); restoreErr != nil {
					logger.Error("restore core after relay delete failed: ", restoreErr)
				}
			}
		}
	}()
	if err := updateRelayRouteRules(tx, items, false, true); err != nil {
		return err
	}
	var inboundTags, outboundTags []string
	var clientIDs []uint
	for _, item := range items {
		inboundTags = append(inboundTags, item.InboundTag)
		outboundTags = append(outboundTags, item.OutboundTag)
		if item.IPv6OutboundTag != "" {
			outboundTags = append(outboundTags, item.IPv6OutboundTag)
		}
		if item.IPv4OutboundTag != "" {
			outboundTags = append(outboundTags, item.IPv4OutboundTag)
		}
		if item.ClientID > 0 {
			clientIDs = append(clientIDs, item.ClientID)
		}
	}
	if len(clientIDs) > 0 {
		if err := tx.Where("id IN ?", clientIDs).Delete(&model.Client{}).Error; err != nil {
			return err
		}
	}
	if len(inboundTags) > 0 {
		if err := tx.Where("tag IN ?", inboundTags).Delete(&model.Inbound{}).Error; err != nil {
			return err
		}
	}
	if len(outboundTags) > 0 {
		if err := tx.Where("tag IN ?", outboundTags).Delete(&model.Outbound{}).Error; err != nil {
			return err
		}
	}
	if err := tx.Where("pool_id = ?", pool.Id).Delete(&model.RelayRefreshLink{}).Error; err != nil {
		return err
	}
	if err := tx.Delete(&pool).Error; err != nil {
		return err
	}
	if corePtr != nil && corePtr.IsRunning() {
		oldConfigPtr, err := s.GetConfig("")
		if err != nil {
			return err
		}
		oldConfig = *oldConfigPtr
		newConfig, err := s.GetConfigWithDB("", tx)
		if err != nil {
			return err
		}
		if err = corePtr.Stop(); err != nil {
			return err
		}
		if err = corePtr.Start(*newConfig); err != nil {
			return common.NewErrorf("remaining configuration rejected by sing-box: %v", err)
		}
	}
	if err := tx.Create(&model.Changes{DateTime: time.Now().Unix(), Actor: actor, Key: "relay", Action: "delete", Obj: mustJSON(id)}).Error; err != nil {
		return err
	}
	if err := tx.Commit().Error; err != nil {
		return err
	}
	committed = true
	LastUpdate.Store(time.Now().UnixMilli())
	for _, item := range items {
		if item.AddedByUs {
			if err := deleteRelayAddress(item.Interface, item.IPv6, item.Prefix); err != nil {
				logger.Warningf("remove relay IPv6 %s: %v", item.IPv6, err)
			}
		}
	}
	return nil
}

func (s *ConfigService) SetRelayRotation(id uint, request RelayRotationRequest, actor string) (*model.RelayPool, error) {
	relayMu.Lock()
	defer relayMu.Unlock()

	var pool model.RelayPool
	if err := database.GetDB().First(&pool, id).Error; err != nil {
		return nil, err
	}
	if !relayModeUsesIPv6(pool.Mode) {
		return nil, common.NewError("IPv4 upstream-only relay pools cannot rotate IPv6 addresses")
	}
	if request.Enabled {
		return nil, common.NewError("scheduled pool rotation is disabled; use the per-item manual refresh links")
	}
	interval, err := normalizeRelayRotationInterval(request.Enabled, request.IntervalMinutes)
	if err != nil {
		return nil, err
	}
	nextRotateAt := int64(0)
	if request.Enabled {
		nextRotateAt = time.Now().Add(time.Duration(interval) * time.Minute).Unix()
	}
	updates := map[string]interface{}{
		"rotation_enabled":          request.Enabled,
		"rotation_interval_minutes": interval,
		"next_rotate_at":            nextRotateAt,
	}
	db := database.GetDB()
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.RelayPool{}).Where("id = ?", pool.Id).Updates(updates).Error; err != nil {
			return err
		}
		change := map[string]interface{}{
			"pool_id": pool.Id, "enabled": request.Enabled, "interval_minutes": interval,
		}
		return tx.Create(&model.Changes{
			DateTime: time.Now().Unix(), Actor: actor, Key: "relay", Action: "rotation", Obj: mustJSON(change),
		}).Error
	}); err != nil {
		return nil, err
	}
	pool.RotationEnabled = request.Enabled
	pool.RotationIntervalMinutes = interval
	pool.NextRotateAt = nextRotateAt
	LastUpdate.Store(time.Now().UnixMilli())
	return &pool, nil
}

func normalizeRelayRotationInterval(enabled bool, interval int) (int, error) {
	if interval == 0 {
		interval = relayRotationDefaultMinutes
	}
	if enabled && (interval < relayRotationMinMinutes || interval > relayRotationMaxMinutes) {
		return 0, common.NewErrorf(
			"rotation interval must be between %d and %d minutes",
			relayRotationMinMinutes, relayRotationMaxMinutes,
		)
	}
	if interval < relayRotationMinMinutes || interval > relayRotationMaxMinutes {
		interval = relayRotationDefaultMinutes
	}
	return interval, nil
}

func (s *ConfigService) RotateRelay(id uint, actor string) (*RelayRotationResult, error) {
	relayMu.Lock()
	defer relayMu.Unlock()
	return s.rotateRelayItemsLocked(id, "", actor)
}

// RotateRelayItemByToken rotates exactly one IPv6 address selected by its
// bearer refresh token. The token is stable across rotations.
func (s *ConfigService) RotateRelayItemByToken(token, actor string) (*RelayRotationResult, error) {
	token = strings.TrimSpace(token)
	if token == "" || len(token) > 64 {
		return nil, common.NewError("invalid relay refresh token")
	}
	relayMu.Lock()
	defer relayMu.Unlock()
	var link model.RelayRefreshLink
	if err := database.GetDB().Where("token = ?", token).First(&link).Error; err != nil {
		if database.IsNotFound(err) {
			return nil, common.NewError("relay refresh link not found")
		}
		return nil, err
	}
	result, err := s.rotateRelayItemsLocked(link.PoolID, link.InboundTag, actor)
	if err == nil {
		result.RefreshToken = token
	}
	return result, err
}

func (s *ConfigService) rotateRelayItemsLocked(id uint, inboundTag, actor string) (*RelayRotationResult, error) {
	if runtime.GOOS != "linux" {
		return nil, common.NewError("IPv6 rotation is supported on Linux only")
	}
	if !relayHasRoot() {
		return nil, common.NewError("root permission is required to rotate IPv6 addresses")
	}

	db := database.GetDB()
	var pool model.RelayPool
	if err := db.First(&pool, id).Error; err != nil {
		return nil, err
	}
	if !relayModeUsesIPv6(pool.Mode) {
		return nil, common.NewError("IPv4 upstream-only relay pools cannot rotate IPv6 addresses")
	}
	var oldItems []model.RelayItem
	if err := json.Unmarshal(pool.Items, &oldItems); err != nil {
		return nil, common.NewError("invalid relay pool items: ", err.Error())
	}
	if len(oldItems) == 0 {
		return nil, common.NewError("relay pool has no IPv6 items")
	}
	detected, err := discoverRelayIPv6()
	if err != nil {
		return nil, err
	}
	occupied := make(map[string]bool, len(detected)+len(oldItems))
	for _, address := range detected {
		occupied[address.Address] = true
	}
	selectedIndexes := make([]int, 0, len(oldItems))
	if inboundTag == "" {
		for index := range oldItems {
			selectedIndexes = append(selectedIndexes, index)
		}
	} else {
		for index := range oldItems {
			if oldItems[index].InboundTag == inboundTag {
				selectedIndexes = append(selectedIndexes, index)
				break
			}
		}
		if len(selectedIndexes) != 1 {
			return nil, common.NewError("relay refresh link item no longer exists")
		}
	}
	selectedOldItems := make([]model.RelayItem, len(selectedIndexes))
	for index, itemIndex := range selectedIndexes {
		selectedOldItems[index] = oldItems[itemIndex]
	}
	rotatedItems, err := prepareRelayRotatedItems(selectedOldItems, occupied)
	if err != nil {
		return nil, err
	}
	newItems := append([]model.RelayItem(nil), oldItems...)
	for index, itemIndex := range selectedIndexes {
		newItems[itemIndex] = rotatedItems[index]
	}

	added := make([]model.RelayItem, 0, len(rotatedItems))
	cleanupNew := true
	defer func() {
		if !cleanupNew {
			return
		}
		for _, item := range added {
			_ = deleteRelayAddress(item.Interface, item.IPv6, item.Prefix)
		}
	}()
	for _, item := range rotatedItems {
		if err := addRelayAddress(item.Interface, item.IPv6, item.Prefix); err != nil {
			return nil, err
		}
		added = append(added, item)
	}
	if err := waitRelayAddressesReady(rotatedItems); err != nil {
		return nil, err
	}
	if err := validateRelayIPv6Egress(context.Background(), rotatedItems, probeRelayIPv6Egress); err != nil {
		return nil, err
	}
	coreWasRunning := false
	coreOperation := false
	if corePtr != nil {
		startCoreMu.Lock()
		if startCoreInProgress {
			startCoreMu.Unlock()
			return nil, common.NewError("core operation already in progress; retry IPv6 rotation shortly")
		}
		startCoreInProgress = true
		coreOperation = true
		coreWasRunning = corePtr.IsRunning()
		startCoreMu.Unlock()
	}
	defer func() {
		if coreOperation {
			startCoreMu.Lock()
			startCoreInProgress = false
			startCoreMu.Unlock()
		}
	}()

	tx := db.Begin()
	if tx.Error != nil {
		return nil, tx.Error
	}
	committed := false
	var oldConfig []byte
	defer func() {
		if committed {
			return
		}
		_ = tx.Rollback().Error
		if len(oldConfig) > 0 && corePtr != nil {
			if restoreErr := s.restoreSingBoxConfig(oldConfig); restoreErr != nil {
				logger.Error("restore core after relay rotation failed: ", restoreErr)
			}
		}
	}()

	if err := updateRelayRotatedOutbounds(tx, pool.Mode, rotatedItems); err != nil {
		return nil, err
	}
	for _, item := range rotatedItems {
		if err := tx.Model(&model.RelayRefreshLink{}).
			Where("pool_id = ? AND inbound_tag = ?", pool.Id, item.InboundTag).
			Update("last_rotated_at", time.Now().Unix()).Error; err != nil {
			return nil, err
		}
	}

	now := time.Now()
	nextRotateAt := int64(0)
	if pool.RotationEnabled && inboundTag == "" {
		interval, intervalErr := normalizeRelayRotationInterval(true, pool.RotationIntervalMinutes)
		if intervalErr != nil {
			return nil, intervalErr
		}
		pool.RotationIntervalMinutes = interval
		nextRotateAt = now.Add(time.Duration(interval) * time.Minute).Unix()
	}
	pool.Items = mustJSON(newItems)
	pool.LastRotatedAt = now.Unix()
	pool.NextRotateAt = nextRotateAt
	if err := tx.Model(&model.RelayPool{}).Where("id = ?", pool.Id).Updates(map[string]interface{}{
		"items": pool.Items, "last_rotated_at": pool.LastRotatedAt,
		"next_rotate_at": pool.NextRotateAt, "rotation_interval_minutes": pool.RotationIntervalMinutes,
	}).Error; err != nil {
		return nil, err
	}

	if coreWasRunning {
		oldConfigPtr, err := s.GetConfig("")
		if err != nil {
			return nil, err
		}
		oldConfig = *oldConfigPtr
		newConfig, err := s.GetConfigWithDB("", tx)
		if err != nil {
			return nil, err
		}
		if err := corePtr.Stop(); err != nil {
			return nil, err
		}
		if err := corePtr.Start(*newConfig); err != nil {
			return nil, common.NewErrorf("rotated relay configuration rejected by sing-box: %v", err)
		}
	}
	change := map[string]interface{}{"pool_id": pool.Id, "rotated": len(rotatedItems)}
	if inboundTag != "" {
		change["inbound_tag"] = inboundTag
	}
	if err := tx.Create(&model.Changes{
		DateTime: now.Unix(), Actor: actor, Key: "relay", Action: "rotate", Obj: mustJSON(change),
	}).Error; err != nil {
		return nil, err
	}
	if err := tx.Commit().Error; err != nil {
		return nil, err
	}
	committed = true
	cleanupNew = false
	LastUpdate.Store(time.Now().UnixMilli())

	for _, itemIndex := range selectedIndexes {
		item := oldItems[itemIndex]
		if item.AddedByUs {
			if err := deleteRelayAddress(item.Interface, item.IPv6, item.Prefix); err != nil {
				logger.Warningf("remove rotated relay IPv6 %s: %v", item.IPv6, err)
			}
		}
	}
	addresses := make([]string, 0, len(rotatedItems))
	for _, item := range rotatedItems {
		addresses = append(addresses, item.IPv6)
	}
	itemIndex := 0
	if len(selectedIndexes) == 1 {
		itemIndex = selectedIndexes[0] + 1
	}
	return &RelayRotationResult{
		PoolID: pool.Id, Rotated: len(rotatedItems), IPv6: addresses, ItemIndex: itemIndex,
		LastRotatedAt: pool.LastRotatedAt, NextRotateAt: pool.NextRotateAt,
	}, nil
}

func updateRelayRotatedOutbounds(tx *gorm.DB, mode string, items []model.RelayItem) error {
	for index := range items {
		outboundTag := relayItemIPv6OutboundTag(mode, items[index])
		if outboundTag == "" {
			return common.NewErrorf("relay item %d has no IPv6 outbound", index+1)
		}
		var outbound model.Outbound
		if err := tx.Where("tag = ?", outboundTag).First(&outbound).Error; err != nil {
			return err
		}
		if outbound.Type != "direct" {
			return common.NewErrorf("relay IPv6 outbound %q is not direct", outboundTag)
		}
		var options map[string]interface{}
		if len(outbound.Options) > 0 {
			if err := json.Unmarshal(outbound.Options, &options); err != nil {
				return err
			}
		}
		if options == nil {
			options = make(map[string]interface{})
		}
		options["inet6_bind_address"] = items[index].IPv6
		if items[index].AppleIDIPv4Only {
			options["domain_strategy"] = relayDomainStrategyIPv6Only
		}
		if err := tx.Model(&model.Outbound{}).Where("id = ?", outbound.Id).Update("options", mustJSON(options)).Error; err != nil {
			return err
		}
	}
	return nil
}

func relayItemIPv6OutboundTag(mode string, item model.RelayItem) string {
	if mode == relayModeDualStack {
		return item.IPv6OutboundTag
	}
	return item.OutboundTag
}

func prepareRelayRotatedItems(items []model.RelayItem, occupied map[string]bool) ([]model.RelayItem, error) {
	rotated := make([]model.RelayItem, len(items))
	copy(rotated, items)
	for index := range rotated {
		item := &rotated[index]
		oldAddress, err := netip.ParseAddr(item.IPv6)
		if err != nil || !oldAddress.Is6() || oldAddress.Is4In6() {
			return nil, common.NewErrorf("relay item %d has invalid IPv6 address", index+1)
		}
		if item.Interface == "" || item.Prefix < 1 || item.Prefix >= 128 {
			return nil, common.NewErrorf("relay item %d has an IPv6 prefix that cannot rotate", index+1)
		}
		base := netip.PrefixFrom(oldAddress, item.Prefix).Masked().Addr()
		var replacement netip.Addr
		for attempt := 0; attempt < 4096; attempt++ {
			candidate, err := randomRelayIPv6(base, item.Prefix)
			if err != nil {
				return nil, err
			}
			if candidate == oldAddress || candidate == base || occupied[candidate.String()] || !candidate.IsGlobalUnicast() {
				continue
			}
			replacement = candidate
			break
		}
		if !replacement.IsValid() {
			return nil, common.NewErrorf("unable to allocate a new IPv6 address for relay item %d", index+1)
		}
		item.IPv6 = replacement.String()
		item.AddedByUs = true
		occupied[item.IPv6] = true
	}
	return rotated, nil
}

func (s *ConfigService) RotateDueRelays(now time.Time) {
	var pools []model.RelayPool
	if err := database.GetDB().Where(
		"rotation_enabled = ? AND next_rotate_at > 0 AND next_rotate_at <= ?", true, now.Unix(),
	).Order("next_rotate_at asc").Limit(20).Find(&pools).Error; err != nil {
		logger.Warning("load due relay rotations failed: ", err)
		return
	}
	for _, pool := range pools {
		if _, err := s.RotateRelay(pool.Id, "scheduler"); err != nil {
			logger.Warningf("rotate relay pool %d failed: %v", pool.Id, err)
			retryAt := now.Add(5 * time.Minute).Unix()
			if updateErr := database.GetDB().Model(&model.RelayPool{}).Where("id = ?", pool.Id).Update("next_rotate_at", retryAt).Error; updateErr != nil {
				logger.Warningf("schedule relay pool %d rotation retry failed: %v", pool.Id, updateErr)
			}
		}
	}
}

func relayProtocolConfig(req RelayCreateRequest, item *model.RelayItem) (map[string]interface{}, map[string]interface{}, error) {
	password := item.Password
	username := item.Username
	options := map[string]interface{}{}
	client := map[string]interface{}{}

	switch req.Protocol {
	case "socks", "http":
		client[req.Protocol] = map[string]interface{}{"username": username, "password": password}
	case "mixed":
		client["mixed"] = map[string]interface{}{"username": username, "password": password}
		client["socks"] = map[string]interface{}{"username": username, "password": password}
		client["http"] = map[string]interface{}{"username": username, "password": password}
	case "shadowsocks":
		method := req.ShadowsocksMethod
		options["method"] = method
		if strings.HasPrefix(method, "2022") {
			item.Password = relayShadowsocksKey(method)
			item.InboundPassword = relayShadowsocksKey(method)
			password = item.Password
			options["password"] = item.InboundPassword
		} else {
			options["password"] = password
		}
		key := "shadowsocks"
		if method == "2022-blake3-aes-128-gcm" {
			key = "shadowsocks16"
		}
		client[key] = map[string]interface{}{"name": username, "password": password}
	case "vless":
		item.UUID = randomUUID()
		client["vless"] = map[string]interface{}{"name": username, "uuid": item.UUID, "flow": ""}
	case "vmess":
		item.UUID = randomUUID()
		client["vmess"] = map[string]interface{}{"name": username, "uuid": item.UUID, "alterId": 0}
	case "trojan":
		client["trojan"] = map[string]interface{}{"name": username, "password": password}
	case "hysteria2":
		client["hysteria2"] = map[string]interface{}{"name": username, "password": password}
	case "tuic":
		item.UUID = randomUUID()
		client["tuic"] = map[string]interface{}{"name": username, "uuid": item.UUID, "password": password}
		options["congestion_control"] = "cubic"
	case "naive":
		client["naive"] = map[string]interface{}{"username": username, "password": password}
	case "anytls":
		client["anytls"] = map[string]interface{}{"name": username, "password": password}
		options["padding_scheme"] = []string{"stop=8", "0=30-30", "1=100-400", "2=400-500,c,500-1000,c,500-1000,c,500-1000,c,500-1000", "3=9-9,500-1000", "4=500-1000", "5=500-1000", "6=500-1000", "7=500-1000"}
	default:
		return nil, nil, common.NewErrorf("relay protocol %q is not supported", req.Protocol)
	}
	if req.Protocol == "vless" || req.Protocol == "vmess" || req.Protocol == "trojan" {
		options["transport"] = relayTransport(req.Transport)
	}
	return options, client, nil
}

func relayShadowsocksKey(method string) string {
	length := 32
	if method == "2022-blake3-aes-128-gcm" {
		length = 16
	}
	key := make([]byte, length)
	if _, err := rand.Read(key); err != nil {
		return base64.StdEncoding.EncodeToString([]byte(common.Random(length)))
	}
	return base64.StdEncoding.EncodeToString(key)
}

func relayInboundOptions(req RelayCreateRequest, protocolOptions map[string]interface{}, listen string, port int) map[string]interface{} {
	options := map[string]interface{}{"listen": listen, "listen_port": port}
	for key, value := range protocolOptions {
		options[key] = value
	}
	return options
}

func relayTransport(value string) map[string]interface{} {
	switch value {
	case "ws":
		return map[string]interface{}{"type": "ws", "path": "/"}
	case "grpc":
		return map[string]interface{}{"type": "grpc", "service_name": "relay"}
	case "httpupgrade":
		return map[string]interface{}{"type": "httpupgrade", "path": "/"}
	case "quic":
		return map[string]interface{}{"type": "quic"}
	default:
		return map[string]interface{}{"type": "http", "path": "/"}
	}
}

func relayClientLink(req RelayCreateRequest, inbound model.Inbound, clientConfig map[string]interface{}, publicHost string) string {
	links := relayClientLinks(req, inbound, clientConfig, publicHost)
	if len(links) > 0 {
		return links[0]["uri"]
	}
	return ""
}

func relayClientLinks(req RelayCreateRequest, inbound model.Inbound, clientConfig map[string]interface{}, publicHost string) []map[string]string {
	host := publicHost
	copyInbound := inbound
	linkHost := host
	if req.Protocol != "vmess" {
		linkHost = formatRelayHost(host)
	}
	copyInbound.Addrs = mustJSON([]map[string]interface{}{{"server": linkHost, "server_port": relayListenPort(inbound)}})
	link := util.LinkGenerator(mustJSON(clientConfig), &copyInbound, publicHost, "")
	result := make([]map[string]string, 0, len(link))
	for _, uri := range link {
		result = append(result, map[string]string{"remark": inbound.Tag, "type": "local", "uri": uri})
	}
	return result
}

func relayListenPort(inbound model.Inbound) int {
	var options struct {
		ListenPort int `json:"listen_port"`
	}
	_ = json.Unmarshal(inbound.Options, &options)
	return options.ListenPort
}

func randomUUID() string {
	value, err := uuid.NewV4()
	if err != nil {
		return common.Random(32)
	}
	return value.String()
}

func (s *ConfigService) prepareRelayItems(req RelayCreateRequest) ([]model.RelayItem, error) {
	items := make([]model.RelayItem, req.Count)
	usedUsernames := make(map[string]bool)
	if req.Mode == relayModeUpstream {
		for i, upstream := range req.Upstreams {
			if err := validateUpstream(upstream); err != nil {
				return nil, fmt.Errorf("upstream line %d: %w", i+1, err)
			}
			items[i] = model.RelayItem{ListenPort: req.PortStart + i, Username: uniqueRelayUsername(req.UsernamePrefix, i, usedUsernames), Password: common.Random(req.PasswordLength), UpstreamServer: upstream.Server, UpstreamPort: upstream.Port, UpstreamUsername: upstream.Username, UpstreamPassword: upstream.Password}
		}
		return items, nil
	}
	base, prefix, iface, err := resolveRelayBase(req)
	if err != nil {
		return nil, err
	}
	addresses := make([]netip.Addr, 0, req.Count)
	usedAddresses := make(map[string]bool)
	if len(req.IPv6Addresses) > req.Count {
		return nil, common.NewErrorf("IPv6 address list cannot contain more than %d entries", req.Count)
	}
	for _, raw := range req.IPv6Addresses {
		ip, p, err := parseRelayAddress(raw, prefix)
		if err != nil {
			return nil, fmt.Errorf("invalid IPv6 address %q: %w", raw, err)
		}
		if p != prefix || !ip.IsGlobalUnicast() || ip.IsPrivate() || !netip.PrefixFrom(ip, prefix).Contains(base) {
			return nil, fmt.Errorf("IPv6 address %q is outside the selected public prefix", raw)
		}
		if usedAddresses[ip.String()] {
			return nil, fmt.Errorf("duplicate IPv6 address %q", raw)
		}
		usedAddresses[ip.String()] = true
		addresses = append(addresses, ip)
	}
	if len(addresses) > req.Count {
		addresses = addresses[:req.Count]
	}
	for len(addresses) < req.Count {
		ip, err := randomRelayIPv6(base, prefix)
		if err != nil {
			return nil, err
		}
		if usedAddresses[ip.String()] || ip == base || !ip.IsGlobalUnicast() {
			continue
		}
		usedAddresses[ip.String()] = true
		addresses = append(addresses, ip)
	}
	usedUsernames = make(map[string]bool)
	for i, ip := range addresses {
		items[i] = model.RelayItem{ListenPort: req.PortStart + i, Username: uniqueRelayUsername(req.UsernamePrefix, i, usedUsernames), Password: common.Random(req.PasswordLength), IPv6: ip.String(), Interface: iface, Prefix: prefix}
		if relayModePairsUpstream(req.Mode) {
			upstream := req.Upstreams[i]
			if err := validateUpstream(upstream); err != nil {
				return nil, fmt.Errorf("upstream line %d: %w", i+1, err)
			}
			items[i].UpstreamServer = upstream.Server
			items[i].UpstreamPort = upstream.Port
			items[i].UpstreamUsername = upstream.Username
			items[i].UpstreamPassword = upstream.Password
		}
	}
	return items, nil
}

func resolveRelayBase(req RelayCreateRequest) (netip.Addr, int, string, error) {
	if strings.TrimSpace(req.BaseIPv6) != "" {
		ip, prefix, err := parseRelayAddress(req.BaseIPv6, req.Prefix)
		if err != nil {
			return netip.Addr{}, 0, "", err
		}
		if req.Prefix > 0 {
			prefix = req.Prefix
		}
		if prefix < 1 || prefix > 128 || !ip.IsGlobalUnicast() || ip.IsPrivate() {
			return netip.Addr{}, 0, "", common.NewError("base IPv6 must be a public global-unicast address")
		}
		hostBits := 128 - prefix
		if hostBits == 0 || (hostBits < 63 && req.Count > (1<<hostBits)-1) {
			return netip.Addr{}, 0, "", common.NewError("IPv6 prefix does not contain enough addresses for this pool")
		}
		iface := req.Interface
		if iface == "" {
			iface = findRelayInterface(ip, prefix)
		}
		if iface == "" {
			return netip.Addr{}, 0, "", common.NewError("IPv6 interface was not found")
		}
		return ip, prefix, iface, nil
	}
	detected, err := discoverRelayIPv6()
	if err != nil {
		return netip.Addr{}, 0, "", err
	}
	for _, candidate := range detected {
		if req.Interface == "" || req.Interface == candidate.Interface {
			ip, _ := netip.ParseAddr(candidate.Address)
			return ip, candidate.Prefix, candidate.Interface, nil
		}
	}
	return netip.Addr{}, 0, "", common.NewError("no public IPv6 address was detected")
}

func discoverRelayIPv6() ([]RelayIPv6, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	result := make([]RelayIPv6, 0)
	for _, iface := range interfaces {
		if iface.Flags&net.FlagLoopback != 0 || strings.HasPrefix(iface.Name, "tun") || iface.Name == "docker0" {
			continue
		}
		addresses, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, raw := range addresses {
			prefix, err := netip.ParsePrefix(raw.String())
			if err != nil || !prefix.Addr().Is6() || !prefix.Addr().IsGlobalUnicast() || prefix.Addr().IsPrivate() {
				continue
			}
			result = append(result, RelayIPv6{Interface: iface.Name, Address: prefix.Addr().String(), Prefix: prefix.Bits()})
		}
	}
	return result, nil
}

func discoverRelayIPv6Summary(excluded map[string]bool, limit int) ([]RelayIPv6, int, error) {
	all, err := discoverRelayIPv6()
	if err != nil {
		return nil, 0, err
	}
	result, total := summarizeRelayIPv6(all, excluded, limit)
	return result, total, nil
}

func summarizeRelayIPv6(all []RelayIPv6, excluded map[string]bool, limit int) ([]RelayIPv6, int) {
	if limit < 1 {
		limit = relayIPv6DisplayLimit
	}
	available := make([]RelayIPv6, 0, len(all))
	for _, item := range all {
		if !excluded[item.Address] {
			available = append(available, item)
		}
	}
	if len(available) <= limit {
		return available, len(available)
	}

	result := make([]RelayIPv6, 0, limit)
	seenInterfaces := make(map[string]bool)
	selected := make(map[string]bool)
	for _, item := range available {
		if seenInterfaces[item.Interface] || len(result) >= limit {
			continue
		}
		seenInterfaces[item.Interface] = true
		selected[item.Interface+"\x00"+item.Address] = true
		result = append(result, item)
	}
	for _, item := range available {
		if len(result) >= limit {
			break
		}
		key := item.Interface + "\x00" + item.Address
		if selected[key] {
			continue
		}
		selected[key] = true
		result = append(result, item)
	}
	return result, len(available)
}

func parseRelayAddress(raw string, fallbackPrefix int) (netip.Addr, int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return netip.Addr{}, 0, common.NewError("empty IPv6 address")
	}
	if prefix, err := netip.ParsePrefix(raw); err == nil {
		return prefix.Addr(), prefix.Bits(), nil
	}
	ip, err := netip.ParseAddr(raw)
	if err != nil {
		return netip.Addr{}, 0, err
	}
	if fallbackPrefix == 0 {
		return netip.Addr{}, 0, common.NewError("IPv6 prefix is required")
	}
	return ip, fallbackPrefix, nil
}

func randomRelayIPv6(base netip.Addr, prefix int) (netip.Addr, error) {
	masked := base.As16()
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return netip.Addr{}, err
	}
	for bit := prefix; bit < 128; bit++ {
		byteIndex := bit / 8
		mask := byte(1 << (7 - (bit % 8)))
		if random[byteIndex]&mask != 0 {
			masked[byteIndex] |= mask
		} else {
			masked[byteIndex] &^= mask
		}
	}
	return netip.AddrFrom16(masked), nil
}

func addRelayAddress(iface, ip string, prefix int) error {
	if runtime.GOOS != "linux" {
		return common.NewError("adding IPv6 addresses is supported on Linux only")
	}
	if !relayHasRoot() {
		return common.NewError("root permission is required to add IPv6 addresses")
	}
	if iface == "" || prefix < 1 || prefix > 128 {
		return common.NewError("invalid IPv6 interface or prefix")
	}
	if _, err := net.InterfaceByName(iface); err != nil {
		return err
	}
	if _, err := exec.LookPath("ip"); err != nil {
		return common.NewError("iproute2 is required: ", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ip", "-6", "addr", "add", ip+"/"+strconv.Itoa(prefix), "dev", iface)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ip address add failed: %v: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func waitRelayAddressReady(iface, ip string) error {
	if runtime.GOOS != "linux" {
		return nil
	}
	if iface == "" {
		return common.NewError("IPv6 interface is required")
	}
	if _, err := netip.ParseAddr(ip); err != nil {
		return err
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		output, err := exec.CommandContext(ctx, "ip", "-6", "-o", "addr", "show", "dev", iface).CombinedOutput()
		cancel()
		if err != nil {
			return fmt.Errorf("check IPv6 address readiness failed: %v: %s", err, strings.TrimSpace(string(output)))
		}
		switch relayIPv6AddressState(string(output), ip) {
		case relayAddressReady:
			return nil
		case relayAddressDADFailed:
			return common.NewErrorf("IPv6 duplicate-address detection failed for %s", ip)
		}
		if time.Now().After(deadline) {
			return common.NewErrorf("IPv6 address %s did not become ready before timeout", ip)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func waitRelayAddressesReady(items []model.RelayItem) error {
	if runtime.GOOS != "linux" {
		return nil
	}
	pending := make(map[string]map[string]bool)
	for _, item := range items {
		if item.IPv6 == "" {
			continue
		}
		if item.Interface == "" {
			return common.NewError("IPv6 interface is required")
		}
		if _, err := netip.ParseAddr(item.IPv6); err != nil {
			return err
		}
		if pending[item.Interface] == nil {
			pending[item.Interface] = make(map[string]bool)
		}
		pending[item.Interface][item.IPv6] = true
	}
	if len(pending) == 0 {
		return nil
	}

	deadline := time.Now().Add(10 * time.Second)
	for {
		for iface, addresses := range pending {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			output, err := exec.CommandContext(ctx, "ip", "-6", "-o", "addr", "show", "dev", iface).CombinedOutput()
			cancel()
			if err != nil {
				return fmt.Errorf("check IPv6 address readiness failed: %v: %s", err, strings.TrimSpace(string(output)))
			}
			for address := range addresses {
				switch relayIPv6AddressState(string(output), address) {
				case relayAddressReady:
					delete(addresses, address)
				case relayAddressDADFailed:
					return common.NewErrorf("IPv6 duplicate-address detection failed for %s", address)
				}
			}
			if len(addresses) == 0 {
				delete(pending, iface)
			}
		}
		if len(pending) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			for _, addresses := range pending {
				for address := range addresses {
					return common.NewErrorf("IPv6 address %s did not become ready before timeout", address)
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
}

type relayIPv6EgressProbe func(context.Context, netip.Addr) error

func validateRelayIPv6Egress(ctx context.Context, items []model.RelayItem, probe relayIPv6EgressProbe) error {
	addresses := make([]netip.Addr, 0, len(items))
	seen := make(map[netip.Addr]bool, len(items))
	for _, item := range items {
		if item.IPv6 == "" {
			continue
		}
		address, err := netip.ParseAddr(item.IPv6)
		if err != nil || !address.Is6() {
			return common.NewErrorf("invalid relay IPv6 address %q", item.IPv6)
		}
		if !seen[address] {
			seen[address] = true
			addresses = append(addresses, address)
		}
	}
	if len(addresses) == 0 {
		return nil
	}

	probeContext, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan netip.Addr, len(addresses))
	for _, address := range addresses {
		jobs <- address
	}
	close(jobs)

	workerCount := relayIPv6ProbeWorkers
	if len(addresses) < workerCount {
		workerCount = len(addresses)
	}
	var workers sync.WaitGroup
	var firstFailure sync.Once
	var failedAddress netip.Addr
	var failedError error
	for range workerCount {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for {
				select {
				case <-probeContext.Done():
					return
				case address, ok := <-jobs:
					if !ok {
						return
					}
					if err := probe(probeContext, address); err != nil {
						firstFailure.Do(func() {
							failedAddress = address
							failedError = err
							cancel()
						})
						return
					}
				}
			}
		}()
	}
	workers.Wait()
	if failedError == nil {
		return nil
	}
	return common.NewErrorf(
		"%s|%s|IPv6 address is configured locally but cannot reach the IPv6 Internet; the VPS provider may only permit its assigned IPv6. Request a routed or authorized prefix. No relay was created: %v",
		relayIPv6EgressErrorCode,
		failedAddress,
		failedError,
	)
}

func probeRelayIPv6Egress(ctx context.Context, address netip.Addr) error {
	if !address.Is6() {
		return common.NewError("IPv6 egress probe requires an IPv6 address")
	}
	var lastError error
	for _, target := range relayIPv6EgressTargets {
		if err := ctx.Err(); err != nil {
			return err
		}
		attemptContext, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
		dialer := net.Dialer{
			Timeout:   2500 * time.Millisecond,
			LocalAddr: &net.TCPAddr{IP: net.IP(address.AsSlice())},
		}
		connection, err := dialer.DialContext(attemptContext, "tcp6", target)
		cancel()
		if err == nil {
			_ = connection.Close()
			return nil
		}
		lastError = err
	}
	return lastError
}

func relayIPv6AddressState(output, address string) string {
	want, err := netip.ParseAddr(address)
	if err != nil {
		return relayAddressMissing
	}
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		for index, field := range fields {
			if field != "inet6" || index+1 >= len(fields) {
				continue
			}
			prefix, err := netip.ParsePrefix(fields[index+1])
			if err != nil || prefix.Addr() != want {
				continue
			}
			tentative := false
			for _, flag := range fields[index+2:] {
				switch flag {
				case relayAddressDADFailed:
					return relayAddressDADFailed
				case relayAddressTentative:
					tentative = true
				}
			}
			if tentative {
				return relayAddressTentative
			}
			return relayAddressReady
		}
	}
	return relayAddressMissing
}

func deleteRelayAddress(iface, ip string, prefix int) error {
	if runtime.GOOS != "linux" || iface == "" || ip == "" {
		return nil
	}
	if !relayHasRoot() {
		return common.NewError("root permission is required to remove IPv6 addresses")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ip", "-6", "addr", "del", ip+"/"+strconv.Itoa(prefix), "dev", iface)
	if output, err := cmd.CombinedOutput(); err != nil {
		if strings.Contains(string(output), "Cannot assign requested address") || strings.Contains(string(output), "RTNETLINK answers: Cannot assign requested address") {
			return nil
		}
		return fmt.Errorf("ip address delete failed: %v: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func findRelayInterface(ip netip.Addr, prefix int) string {
	detected, _ := discoverRelayIPv6()
	network := netip.PrefixFrom(ip, prefix)
	for _, item := range detected {
		candidate, _ := netip.ParseAddr(item.Address)
		if item.Prefix == prefix && network.Contains(candidate) {
			return item.Interface
		}
	}
	return ""
}

func relayUsedPorts(tx *gorm.DB) (map[int]bool, error) {
	var inbounds []model.Inbound
	if err := tx.Find(&inbounds).Error; err != nil {
		return nil, err
	}
	used := make(map[int]bool)
	for _, inbound := range inbounds {
		var options struct {
			ListenPort int `json:"listen_port"`
		}
		if err := json.Unmarshal(inbound.Options, &options); err == nil && options.ListenPort > 0 {
			used[options.ListenPort] = true
		}
	}
	return used, nil
}

func nextRelayPortRange(used map[int]bool, preferredStart, count int) (int, error) {
	if preferredStart < 1 || preferredStart > 65535 || count < 1 || count > 65535-preferredStart+1 {
		return 0, common.NewError("relay port range is invalid")
	}
	lastStart := 65535 - count + 1
	for start := preferredStart; start <= lastStart; {
		conflict := 0
		for port := start; port < start+count; port++ {
			if used[port] {
				conflict = port
				break
			}
		}
		if conflict == 0 {
			return start, nil
		}
		start = conflict + 1
	}
	return 0, common.NewErrorf("no continuous relay port range of %d ports is available from %d", count, preferredStart)
}

func updateRelayRouteRules(tx *gorm.DB, items []model.RelayItem, ipv6Only, remove bool) error {
	var setting model.Setting
	if err := tx.Where("key = ?", "config").First(&setting).Error; err != nil {
		return err
	}
	var config map[string]interface{}
	if err := json.Unmarshal([]byte(setting.Value), &config); err != nil {
		return err
	}
	route, _ := config["route"].(map[string]interface{})
	if route == nil {
		route = map[string]interface{}{}
		config["route"] = route
	}
	rules, _ := route["rules"].([]interface{})
	if rules == nil {
		rules = []interface{}{}
	}
	targets := make(map[string]string, len(items))
	ipv6Targets := make(map[string]string, len(items))
	ipv4Targets := make(map[string]string, len(items))
	for _, item := range items {
		if item.InboundTag != "" && item.OutboundTag != "" {
			targets[item.InboundTag] = item.OutboundTag
			if item.IPv6OutboundTag != "" {
				ipv6Targets[item.InboundTag] = item.IPv6OutboundTag
			}
			if item.IPv4OutboundTag != "" {
				ipv4Targets[item.InboundTag] = item.IPv4OutboundTag
			}
		}
	}
	filtered := make([]interface{}, 0, len(rules))
	for _, raw := range rules {
		entry, ok := raw.(map[string]interface{})
		if !ok {
			filtered = append(filtered, raw)
			continue
		}
		matchedInbounds := relayRuleTargetInbounds(entry, targets)
		if len(matchedInbounds) == 0 {
			filtered = append(filtered, raw)
			continue
		}
		outbound := fmt.Sprint(entry["outbound"])
		generated := false
		for _, inbound := range matchedInbounds {
			if targets[inbound] == outbound || (ipv6Targets[inbound] != "" && ipv6Targets[inbound] == outbound) || (ipv4Targets[inbound] != "" && ipv4Targets[inbound] == outbound) {
				generated = true
			}
			if ipv4Targets[inbound] != "" && fmt.Sprint(entry["action"]) == "resolve" && fmt.Sprint(entry["server"]) == relayPairedDNSResolverTag {
				generated = true
			}
		}
		ipv4Reject := fmt.Sprint(entry["action"]) == "reject" && relayRuleIPVersion(entry) == 4
		if generated || ipv4Reject {
			continue
		}
		filtered = append(filtered, raw)
	}
	if !remove {
		newRules := make([]interface{}, 0, len(items)*3)
		for _, item := range items {
			if _, ok := targets[item.InboundTag]; !ok {
				continue
			}
			if item.AppleIDIPv4Only && item.IPv4OutboundTag != "" && (item.IPv6OutboundTag != "" || item.OutboundTag != "") {
				newRules = append(newRules, map[string]interface{}{
					"inbound": []string{item.InboundTag}, "domain_suffix": relayAppleIDDomains,
					"action": "route", "outbound": item.IPv4OutboundTag,
				})
				newRules = append(newRules, map[string]interface{}{
					"inbound": []string{item.InboundTag}, "domain_suffix": relayVintedCaptchaDomains,
					"action": "route", "outbound": item.IPv4OutboundTag,
				})
			}
			if item.AppleIDIPv4Only && item.IPv4OutboundTag != "" {
				ipv6Outbound := item.IPv6OutboundTag
				if ipv6Outbound == "" {
					ipv6Outbound = item.OutboundTag
				}
				newRules = append(newRules,
					map[string]interface{}{
						"inbound": []string{item.InboundTag}, "action": "resolve", "strategy": relayDomainStrategyIPv6Only, "server": relayPairedDNSResolverTag,
					},
					map[string]interface{}{
						"inbound": []string{item.InboundTag}, "ip_version": 4, "ip_cidr": []string{"0.0.0.0/0"}, "action": "reject",
					},
					map[string]interface{}{
						"inbound": []string{item.InboundTag}, "action": "route", "outbound": ipv6Outbound,
					},
				)
				continue
			}
			if item.IPv6OutboundTag != "" && item.IPv4OutboundTag != "" {
				newRules = append(newRules,
					map[string]interface{}{
						"inbound": []string{item.InboundTag}, "action": "resolve", "strategy": relayDomainStrategyPreferIPv6, "server": relayPairedDNSResolverTag,
					},
					map[string]interface{}{
						"inbound": []string{item.InboundTag}, "action": "route", "outbound": item.OutboundTag,
					},
				)
				continue
			}
			if item.IPv4OutboundTag != "" {
				newRules = append(newRules,
					map[string]interface{}{
						"inbound": []string{item.InboundTag}, "action": "resolve", "strategy": relayDomainStrategyPreferIPv6, "server": relayPairedDNSResolverTag,
					},
					map[string]interface{}{
						"inbound": []string{item.InboundTag}, "ip_cidr": []string{"::/0"}, "action": "route", "outbound": item.OutboundTag,
					},
					map[string]interface{}{
						"inbound": []string{item.InboundTag}, "ip_cidr": []string{"0.0.0.0/0"}, "action": "route", "outbound": item.IPv4OutboundTag,
					},
				)
				continue
			}
			if ipv6Only {
				newRules = append(newRules, map[string]interface{}{
					"inbound": []string{item.InboundTag}, "ip_version": 4, "action": "reject",
				})
			}
			newRules = append(newRules, map[string]interface{}{
				"inbound": []string{item.InboundTag}, "action": "route", "outbound": item.OutboundTag,
			})
		}
		rules = append(newRules, filtered...)
	} else {
		rules = filtered
	}
	route["rules"] = rules
	if err := updateRelayPairedDNSResolver(config, relayPairedDNSResolverRequired(rules)); err != nil {
		return err
	}
	updated, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return tx.Model(&model.Setting{}).Where("key = ?", "config").Update("value", string(updated)).Error
}

func relayPairedDNSResolverRequired(rules []interface{}) bool {
	for _, raw := range rules {
		entry, ok := raw.(map[string]interface{})
		if ok && fmt.Sprint(entry["action"]) == "resolve" && fmt.Sprint(entry["server"]) == relayPairedDNSResolverTag {
			return true
		}
	}
	return false
}

func updateRelayPairedDNSResolver(config map[string]interface{}, required bool) error {
	dns, _ := config["dns"].(map[string]interface{})
	if dns == nil {
		dns = map[string]interface{}{}
		config["dns"] = dns
	}
	servers, _ := dns["servers"].([]interface{})
	filtered := make([]interface{}, 0, len(servers)+1)
	found := false
	for _, raw := range servers {
		server, ok := raw.(map[string]interface{})
		if !ok || fmt.Sprint(server["tag"]) != relayPairedDNSResolverTag {
			filtered = append(filtered, raw)
			continue
		}
		if fmt.Sprint(server["type"]) != "local" {
			return common.NewErrorf("DNS server tag %q is reserved by paired relays", relayPairedDNSResolverTag)
		}
		found = true
		if required {
			filtered = append(filtered, raw)
		}
	}
	if required && !found {
		filtered = append(filtered, map[string]interface{}{"type": "local", "tag": relayPairedDNSResolverTag})
	}
	dns["servers"] = filtered
	return nil
}

func relayRuleTargetInbounds(entry map[string]interface{}, targets map[string]string) []string {
	var values []string
	switch inbound := entry["inbound"].(type) {
	case []interface{}:
		for _, value := range inbound {
			values = append(values, fmt.Sprint(value))
		}
	case []string:
		values = append(values, inbound...)
	case string:
		values = append(values, inbound)
	}
	matched := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := targets[value]; ok {
			matched = append(matched, value)
		}
	}
	return matched
}

func relayRuleIPVersion(entry map[string]interface{}) int {
	switch value := entry["ip_version"].(type) {
	case float64:
		return int(value)
	case int:
		return value
	case json.Number:
		version, _ := strconv.Atoi(value.String())
		return version
	default:
		version, _ := strconv.Atoi(fmt.Sprint(value))
		return version
	}
}

func (s *ConfigService) restoreSingBoxConfig(config []byte) error {
	if corePtr == nil {
		return nil
	}
	if corePtr.IsRunning() {
		if err := corePtr.Stop(); err != nil {
			return err
		}
	}
	return corePtr.Start(config)
}

func uniqueRelayUsername(prefix string, index int, used map[string]bool) string {
	for {
		candidate := fmt.Sprintf("%s-%d-%s", prefix, index+1, common.Random(4))
		if !used[candidate] {
			used[candidate] = true
			return candidate
		}
	}
}

func validateUpstream(upstream RelayUpstream) error {
	server := strings.TrimSpace(strings.Trim(upstream.Server, "[]"))
	if server == "" || upstream.Port < 1 || upstream.Port > 65535 {
		return common.NewError("SOCKS5 server or port is invalid")
	}
	if strings.ContainsAny(server, "/?#@") || strings.ContainsAny(server, " \t\r\n") {
		return common.NewError("SOCKS5 server is invalid")
	}
	if _, err := netip.ParseAddr(server); err != nil && (len(server) > 253 || strings.Contains(server, ":")) {
		return common.NewError("SOCKS5 server is invalid")
	}
	return nil
}

func parseRelayUpstreams(text string) ([]RelayUpstream, error) {
	text = strings.TrimSpace(strings.TrimPrefix(text, "\ufeff"))
	if text == "" {
		return nil, common.NewError("no valid SOCKS5 entries found")
	}
	// A number of proxy APIs return JSON instead of one proxy per line. Try it
	// first, but fall back to line parsing so bracketed IPv6 remains valid.
	if strings.HasPrefix(text, "{") || strings.HasPrefix(text, "[") {
		var raw interface{}
		if err := json.Unmarshal([]byte(text), &raw); err == nil {
			result, err := parseRelayUpstreamJSON(raw)
			if err != nil {
				return nil, err
			}
			if len(result) > 0 {
				return result, nil
			}
		}
	}
	var result []RelayUpstream
	for lineNo, raw := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		upstream, err := parseRelayUpstreamLine(line)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNo+1, err)
		}
		result = append(result, upstream)
	}
	if len(result) == 0 {
		return nil, common.NewError("no valid SOCKS5 entries found")
	}
	return result, nil
}

func parseRelayUpstreamLine(line string) (RelayUpstream, error) {
	line = strings.TrimSpace(strings.Trim(line, "\"'"))
	if line == "" {
		return RelayUpstream{}, common.NewError("empty SOCKS5 entry")
	}
	if strings.Contains(line, "://") {
		parsed, err := url.Parse(line)
		if err != nil || parsed.Hostname() == "" || parsed.Port() == "" {
			return RelayUpstream{}, common.NewError("invalid proxy URL")
		}
		if !isSupportedRelayProxyScheme(parsed.Scheme) {
			return RelayUpstream{}, common.NewErrorf("unsupported proxy scheme %q; use SOCKS5", parsed.Scheme)
		}
		port, err := strconv.Atoi(parsed.Port())
		if err != nil {
			return RelayUpstream{}, common.NewError("invalid SOCKS5 port")
		}
		username, password := "", ""
		if parsed.User != nil {
			username = parsed.User.Username()
			password, _ = parsed.User.Password()
		}
		upstream := RelayUpstream{Server: parsed.Hostname(), Port: port, Username: username, Password: password}
		return upstream, validateUpstream(upstream)
	}
	// Common non-URL authenticated forms: user:pass@host:port and
	// host:port@user:pass.
	if at := strings.LastIndex(line, "@"); at > 0 {
		left, right := line[:at], line[at+1:]
		if upstream, ok := parseRelayEndpointWithCredentials(right, left); ok {
			return upstream, validateUpstream(upstream)
		}
		if upstream, ok := parseRelayEndpointWithCredentials(left, right); ok {
			return upstream, validateUpstream(upstream)
		}
	}
	// Comma, pipe, semicolon and whitespace separated lists are common in
	// provider dashboards. The numeric port identifies which side is host.
	if fields := splitRelayFields(line); len(fields) == 4 {
		if upstream, ok := relayFieldsToUpstream(fields); ok {
			return upstream, validateUpstream(upstream)
		}
	}
	if server, portText, err := net.SplitHostPort(line); err == nil {
		port, err := strconv.Atoi(portText)
		if err != nil {
			return RelayUpstream{}, common.NewError("invalid SOCKS5 port")
		}
		upstream := RelayUpstream{Server: strings.Trim(server, "[]"), Port: port}
		return upstream, validateUpstream(upstream)
	}
	parts := strings.Split(line, ":")
	if len(parts) == 4 {
		// Prefer the long-standing host:port:user:pass form when both the port
		// and password are numeric. Otherwise accept user:pass:host:port.
		if port, err := strconv.Atoi(parts[1]); err == nil {
			upstream := RelayUpstream{Server: strings.Trim(parts[0], "[]"), Port: port, Username: parts[2], Password: parts[3]}
			return upstream, validateUpstream(upstream)
		}
		if port, err := strconv.Atoi(parts[3]); err == nil {
			upstream := RelayUpstream{Server: strings.Trim(parts[2], "[]"), Port: port, Username: parts[0], Password: parts[1]}
			return upstream, validateUpstream(upstream)
		}
	}
	if len(parts) < 4 {
		return RelayUpstream{}, common.NewError("expected host:port, host:port:user:pass, user:pass@host:port, or a SOCKS5 URL")
	}
	password := parts[len(parts)-1]
	username := parts[len(parts)-2]
	port, err := strconv.Atoi(parts[len(parts)-3])
	if err != nil {
		return RelayUpstream{}, common.NewError("invalid SOCKS5 port")
	}
	server := strings.Join(parts[:len(parts)-3], ":")
	server = strings.Trim(server, "[]")
	upstream := RelayUpstream{Server: server, Port: port, Username: username, Password: password}
	return upstream, validateUpstream(upstream)
}

func isSupportedRelayProxyScheme(scheme string) bool {
	switch strings.ToLower(strings.TrimSpace(scheme)) {
	case "socks", "socks5", "socks5h":
		return true
	default:
		return false
	}
}

func parseRelayEndpointWithCredentials(endpoint, credentials string) (RelayUpstream, bool) {
	host, portText, err := net.SplitHostPort(strings.TrimSpace(endpoint))
	if err != nil {
		return RelayUpstream{}, false
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return RelayUpstream{}, false
	}
	parts := strings.SplitN(credentials, ":", 2)
	if len(parts) != 2 || parts[0] == "" {
		return RelayUpstream{}, false
	}
	return RelayUpstream{Server: strings.Trim(host, "[]"), Port: port, Username: parts[0], Password: parts[1]}, true
}

func splitRelayFields(line string) []string {
	return strings.FieldsFunc(line, func(r rune) bool {
		return r == ',' || r == '|' || r == ';' || r == '\t' || r == ' '
	})
}

func relayFieldsToUpstream(fields []string) (RelayUpstream, bool) {
	if len(fields) != 4 {
		return RelayUpstream{}, false
	}
	if port, err := strconv.Atoi(fields[1]); err == nil {
		return RelayUpstream{Server: strings.Trim(fields[0], "[]"), Port: port, Username: fields[2], Password: fields[3]}, true
	}
	if port, err := strconv.Atoi(fields[3]); err == nil {
		return RelayUpstream{Server: strings.Trim(fields[2], "[]"), Port: port, Username: fields[0], Password: fields[1]}, true
	}
	return RelayUpstream{}, false
}

func parseRelayUpstreamJSON(raw interface{}) ([]RelayUpstream, error) {
	var result []RelayUpstream
	var walk func(interface{}) error
	walk = func(value interface{}) error {
		switch item := value.(type) {
		case string:
			upstream, err := parseRelayUpstreamLine(item)
			if err != nil {
				return err
			}
			result = append(result, upstream)
		case []interface{}:
			for _, child := range item {
				if err := walk(child); err != nil {
					return err
				}
			}
		case map[string]interface{}:
			fields := relayJSONFields(item)
			if host, ok := relayJSONString(fields, "host", "server", "ip", "address"); ok {
				port, ok := relayJSONPort(fields, "port", "server_port")
				if !ok {
					return common.NewErrorf("JSON proxy %q has no valid port", host)
				}
				user, _ := relayJSONString(fields, "username", "user", "login")
				pass, _ := relayJSONString(fields, "password", "pass", "pwd")
				result = append(result, RelayUpstream{Server: host, Port: port, Username: user, Password: pass})
				return nil
			}
			for _, key := range []string{"data", "proxies", "proxy", "list", "result", "items"} {
				if child, exists := fields[key]; exists {
					if err := walk(child); err != nil {
						return err
					}
					return nil
				}
			}
		}
		return nil
	}
	if err := walk(raw); err != nil {
		return nil, fmt.Errorf("invalid proxy JSON: %w", err)
	}
	if len(result) == 0 {
		return nil, common.NewError("proxy JSON contains no supported entries")
	}
	for index, upstream := range result {
		if err := validateUpstream(upstream); err != nil {
			return nil, fmt.Errorf("JSON proxy %d: %w", index+1, err)
		}
	}
	return result, nil
}

func relayJSONFields(item map[string]interface{}) map[string]interface{} {
	fields := make(map[string]interface{}, len(item))
	for key, value := range item {
		fields[strings.ToLower(strings.TrimSpace(key))] = value
	}
	return fields
}

func relayJSONString(item map[string]interface{}, keys ...string) (string, bool) {
	for _, key := range keys {
		if value, ok := item[key]; ok {
			if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
				return strings.TrimSpace(text), true
			}
		}
	}
	return "", false
}

func relayJSONPort(item map[string]interface{}, keys ...string) (int, bool) {
	for _, key := range keys {
		if value, ok := item[key]; ok {
			switch typed := value.(type) {
			case float64:
				return int(typed), typed == float64(int(typed))
			case json.Number:
				port, err := strconv.Atoi(typed.String())
				return port, err == nil
			case string:
				port, err := strconv.Atoi(strings.TrimSpace(typed))
				return port, err == nil
			}
		}
	}
	return 0, false
}

func mustJSON(value interface{}) json.RawMessage {
	data, _ := json.Marshal(value)
	return data
}

func relaySOCKSURI(host string, port int, username, password string) string {
	host = strings.Trim(host, "[]")
	return (&url.URL{
		Scheme: "socks5",
		User:   url.UserPassword(username, password),
		Host:   net.JoinHostPort(host, strconv.Itoa(port)),
	}).String()
}

func relaySOCKSExport(publicHost string, port int, username, password string) string {
	host := strings.Trim(publicHost, "[]")
	return fmt.Sprintf("%s:%d:%s:%s", host, port, username, password)
}

func normalizeRelayPublicHost(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", common.NewError("public host is required")
	}
	if strings.HasPrefix(value, "[") || strings.HasSuffix(value, "]") {
		if !strings.HasPrefix(value, "[") || !strings.HasSuffix(value, "]") {
			return "", common.NewError("public host has invalid IPv6 brackets")
		}
		value = strings.TrimSuffix(strings.TrimPrefix(value, "["), "]")
	}
	if ip, err := netip.ParseAddr(value); err == nil {
		return ip.String(), nil
	}
	if strings.ContainsAny(value, ":/@?#") || strings.ContainsAny(value, " \t\r\n") || len(value) > 253 {
		return "", common.NewError("public host must be a hostname or IP address without a port")
	}
	for _, label := range strings.Split(strings.TrimSuffix(value, "."), ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return "", common.NewError("public host is invalid")
		}
		for _, ch := range label {
			if (ch < 'a' || ch > 'z') && (ch < 'A' || ch > 'Z') && (ch < '0' || ch > '9') && ch != '-' {
				return "", common.NewError("public host is invalid")
			}
		}
	}
	return strings.TrimSuffix(value, "."), nil
}

func formatRelayHost(host string) string {
	host = strings.TrimSpace(host)
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		return "[" + host + "]"
	}
	return host
}

func relayInboundListenAddress(mode, publicHost string) string {
	if relayModeUsesIPv6(mode) {
		if address, err := netip.ParseAddr(strings.Trim(publicHost, "[]")); err == nil && address.Is6() {
			return "::"
		}
		return "0.0.0.0"
	}
	return "::"
}
