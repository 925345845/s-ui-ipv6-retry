package service

import (
	"context"
	"fmt"
	"net/netip"
	"time"

	"github.com/Hhz0823/1s-ui/database/model"
	"github.com/Hhz0823/1s-ui/internal/relayipv6"
	"github.com/Hhz0823/1s-ui/logger"
)

// Keep the request bounded below the local control server's ten-minute timeout.
// There is no per-address retry limit: only failed slots consume replacements.
const relayPairedAllocationTimeout = 8 * time.Minute

func fillPairedRelayIPv6(items []model.RelayItem, existing map[string]bool, allowAdd bool) ([]model.RelayItem, error) {
	ctx, cancel := context.WithTimeout(context.Background(), relayPairedAllocationTimeout)
	defer cancel()
	slots := make([]relayipv6.Slot, len(items))
	for i, item := range items {
		slots[i] = relayipv6.Slot{Address: item.IPv6, Interface: item.Interface, Prefix: item.Prefix}
	}
	added, err := relayipv6.Fill(ctx, slots, existing, allowAdd, relayIPv6ProbeWorkers, relayipv6.Operations{
		Add:    func(slot relayipv6.Slot) error { return addRelayAddress(slot.Interface, slot.Address, slot.Prefix) },
		Delete: func(slot relayipv6.Slot) error { return deleteRelayAddress(slot.Interface, slot.Address, slot.Prefix) },
		Check: func(ctx context.Context, slot relayipv6.Slot) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := waitRelayAddressReady(slot.Interface, slot.Address); err != nil {
				return err
			}
			return probeRelayIPv6Egress(ctx, netip.MustParseAddr(slot.Address))
		},
		Failed: func(index int, slot relayipv6.Slot, err error) {
			logger.Warningf("relay upstream %d: IPv6 %s failed; allocating a replacement: %v", index+1, slot.Address, err)
		},
	})
	owned := make([]model.RelayItem, len(added))
	for i, slot := range added {
		owned[i] = model.RelayItem{IPv6: slot.Address, Interface: slot.Interface, Prefix: slot.Prefix, AddedByUs: true}
	}
	if err != nil {
		ready := 0
		for _, slot := range slots {
			if slot.Ready {
				ready++
			}
		}
		return owned, fmt.Errorf("IPv6 自动补齐未完成（已检测通过 %d/%d，最长等待 8 分钟）：%w。请检查网卡、已路由 IPv6 前缀和系统地址创建开关；本次未保存新批次", ready, len(slots), err)
	}
	for i, slot := range slots {
		// All upstream fields, credentials and stable row positions are retained.
		items[i].IPv6 = slot.Address
		items[i].AddedByUs = slot.Owned
	}
	return owned, nil
}
