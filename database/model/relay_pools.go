package model

import "encoding/json"

// RelayPool stores the resources created by the one-click relay workflow.
// Items intentionally remain JSON because a pool is created and removed as a
// single unit and the item credentials are needed for browser export.
type RelayPool struct {
	Id                      uint            `json:"id" gorm:"primaryKey;autoIncrement"`
	Name                    string          `json:"name"`
	Source                  string          `json:"source,omitempty"`
	Mode                    string          `json:"mode"`
	Protocol                string          `json:"protocol"`
	CoreType                string          `json:"core_type"`
	TlsID                   uint            `json:"tls_id,omitempty"`
	Transport               string          `json:"transport,omitempty"`
	DomainStrategy          string          `json:"domain_strategy,omitempty"`
	ListenHost              string          `json:"listen_host"`
	PortStart               int             `json:"port_start"`
	Count                   int             `json:"count"`
	Items                   json.RawMessage `json:"items"`
	CreatedAt               int64           `json:"created_at"`
	RotationEnabled         bool            `json:"rotation_enabled" gorm:"index"`
	RotationIntervalMinutes int             `json:"rotation_interval_minutes"`
	LastRotatedAt           int64           `json:"last_rotated_at,omitempty"`
	NextRotateAt            int64           `json:"next_rotate_at,omitempty" gorm:"index"`
}

type RelayItem struct {
	InboundID        uint   `json:"inbound_id"`
	InboundTag       string `json:"inbound_tag"`
	OutboundTag      string `json:"outbound_tag"`
	IPv6OutboundTag  string `json:"ipv6_outbound_tag,omitempty"`
	IPv4OutboundTag  string `json:"ipv4_outbound_tag,omitempty"`
	AppleIDIPv4Only  bool   `json:"apple_id_ipv4_only,omitempty"`
	ClientID         uint   `json:"client_id"`
	ListenPort       int    `json:"listen_port"`
	Username         string `json:"username"`
	Password         string `json:"password"`
	InboundPassword  string `json:"inbound_password,omitempty"`
	Protocol         string `json:"protocol,omitempty"`
	UUID             string `json:"uuid,omitempty"`
	Method           string `json:"method,omitempty"`
	Export           string `json:"export,omitempty"`
	IPv6             string `json:"ipv6,omitempty"`
	Interface        string `json:"interface,omitempty"`
	Prefix           int    `json:"prefix,omitempty"`
	AddedByUs        bool   `json:"added_by_us,omitempty"`
	RefreshToken     string `json:"refresh_token,omitempty"`
	UpstreamServer   string `json:"upstream_server,omitempty"`
	UpstreamPort     int    `json:"upstream_port,omitempty"`
	UpstreamUsername string `json:"upstream_username,omitempty"`
	UpstreamPassword string `json:"upstream_password,omitempty"`
}

// RelayRefreshLink maps one stable, bearer-style refresh URL to one relay
// item. The token remains stable while the IPv6 address rotates.
type RelayRefreshLink struct {
	Id            uint   `json:"id" gorm:"primaryKey;autoIncrement"`
	PoolID        uint   `json:"pool_id" gorm:"uniqueIndex:idx_relay_refresh_pool_item"`
	InboundTag    string `json:"inbound_tag" gorm:"uniqueIndex:idx_relay_refresh_pool_item"`
	Token         string `json:"token" gorm:"uniqueIndex;size:64"`
	CreatedAt     int64  `json:"created_at"`
	LastRotatedAt int64  `json:"last_rotated_at,omitempty"`
}
