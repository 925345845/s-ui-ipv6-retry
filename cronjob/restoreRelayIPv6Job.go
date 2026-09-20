package cronjob

import (
	"github.com/Hhz0823/1s-ui/logger"
	"github.com/Hhz0823/1s-ui/service"
)

// RestoreRelayIPv6Job periodically repairs IPv6 addresses that were removed
// from an interface by a provider network restart or VPS reboot. The source
// of truth is the persisted relay pool, so users never need to maintain a
// second address list by hand.
type RestoreRelayIPv6Job struct {
	service.ConfigService
}

func NewRestoreRelayIPv6Job() *RestoreRelayIPv6Job {
	return &RestoreRelayIPv6Job{}
}

func (s *RestoreRelayIPv6Job) Run() {
	if err := s.ConfigService.EnsureRelayIPv6Addresses(); err != nil {
		logger.Warning("periodic relay IPv6 restore failed: ", err)
	}
}
