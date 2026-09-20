package core

import (
	"context"
	"net"
	"net/netip"
	"time"

	"github.com/sagernet/sing-box/adapter"
	sbOutbound "github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/common/dialer"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"
)

const RelayFallbackOutboundType = "relay-fallback"

const defaultRelayIPv6Timeout = 3 * time.Second

type RelayFallbackOutboundOptions struct {
	IPv6Outbound string `json:"ipv6_outbound"`
	IPv4Outbound string `json:"ipv4_outbound"`
	IPv6Timeout  string `json:"ipv6_timeout,omitempty"`
}

type relayFallbackOutbound struct {
	sbOutbound.Adapter
	manager      adapter.OutboundManager
	ipv6Tag      string
	ipv4Tag      string
	ipv6Timeout  time.Duration
	ipv6Outbound adapter.Outbound
	ipv4Outbound adapter.Outbound
	dnsRouter    adapter.DNSRouter
}

var _ adapter.Outbound = (*relayFallbackOutbound)(nil)
var _ dialer.ParallelNetworkDialer = (*relayFallbackOutbound)(nil)

func registerRelayFallbackOutbound(registry *sbOutbound.Registry) {
	sbOutbound.Register[RelayFallbackOutboundOptions](registry, RelayFallbackOutboundType, newRelayFallbackOutbound)
}

func newRelayFallbackOutbound(ctx context.Context, _ adapter.Router, _ log.ContextLogger, tag string, options RelayFallbackOutboundOptions) (adapter.Outbound, error) {
	if options.IPv6Outbound == "" || options.IPv4Outbound == "" {
		return nil, E.New("relay fallback requires IPv6 and IPv4 outbound tags")
	}
	if options.IPv6Outbound == options.IPv4Outbound {
		return nil, E.New("relay fallback outbound tags must be different")
	}
	timeout := defaultRelayIPv6Timeout
	if options.IPv6Timeout != "" {
		parsed, err := time.ParseDuration(options.IPv6Timeout)
		if err != nil || parsed <= 0 || parsed > 30*time.Second {
			return nil, E.New("relay fallback IPv6 timeout must be between 1ns and 30s")
		}
		timeout = parsed
	}
	manager := service.FromContext[adapter.OutboundManager](ctx)
	if manager == nil {
		return nil, E.New("missing outbound manager")
	}
	return &relayFallbackOutbound{
		Adapter:     sbOutbound.NewAdapter(RelayFallbackOutboundType, tag, []string{N.NetworkTCP, N.NetworkUDP}, []string{options.IPv6Outbound, options.IPv4Outbound}),
		manager:     manager,
		ipv6Tag:     options.IPv6Outbound,
		ipv4Tag:     options.IPv4Outbound,
		ipv6Timeout: timeout,
		dnsRouter:   service.FromContext[adapter.DNSRouter](ctx),
	}, nil
}

func (o *relayFallbackOutbound) Start() error {
	var loaded bool
	o.ipv6Outbound, loaded = o.manager.Outbound(o.ipv6Tag)
	if !loaded {
		return E.New("IPv6 outbound not found: ", o.ipv6Tag)
	}
	o.ipv4Outbound, loaded = o.manager.Outbound(o.ipv4Tag)
	if !loaded {
		return E.New("IPv4 outbound not found: ", o.ipv4Tag)
	}
	return nil
}

func (o *relayFallbackOutbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	if destination.IsIP() {
		if destination.Addr.Is6() && !destination.Addr.Is4In6() {
			return o.ipv6Outbound.DialContext(ctx, network, destination)
		}
		return o.ipv4Outbound.DialContext(ctx, network, destination)
	}
	ipv6Ctx, cancel := context.WithTimeout(ctx, o.ipv6Timeout)
	conn, ipv6Err := o.ipv6Outbound.DialContext(ipv6Ctx, network, destination)
	cancel()
	if ipv6Err == nil {
		return conn, nil
	}
	conn, ipv4Err := o.dialIPv4(ctx, network, destination, nil, nil, nil, nil, 0)
	if ipv4Err == nil {
		return conn, nil
	}
	return nil, E.Errors(E.Cause(ipv6Err, "IPv6 attempt failed"), E.Cause(ipv4Err, "IPv4 SOCKS5 fallback failed"))
}

func (o *relayFallbackOutbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	if destination.IsIP() {
		if destination.Addr.Is6() && !destination.Addr.Is4In6() {
			return o.ipv6Outbound.ListenPacket(ctx, destination)
		}
		return o.ipv4Outbound.ListenPacket(ctx, destination)
	}
	conn, ipv6Err := o.ipv6Outbound.ListenPacket(ctx, destination)
	if ipv6Err == nil {
		return conn, nil
	}
	conn, _, ipv4Err := o.listenIPv4Packet(ctx, destination, nil)
	if ipv4Err == nil {
		return conn, nil
	}
	return nil, E.Errors(E.Cause(ipv6Err, "IPv6 packet attempt failed"), E.Cause(ipv4Err, "IPv4 SOCKS5 packet fallback failed"))
}

func (o *relayFallbackOutbound) DialParallelNetwork(ctx context.Context, network string, destination M.Socksaddr, destinationAddresses []netip.Addr, strategy *C.NetworkStrategy, networkType []C.InterfaceType, fallbackNetworkType []C.InterfaceType, fallbackDelay time.Duration) (net.Conn, error) {
	addresses6, addresses4 := splitRelayDestinationAddresses(destination, destinationAddresses)
	var ipv6Err error
	if len(addresses6) > 0 {
		ipv6Ctx, cancel := context.WithTimeout(ctx, o.ipv6Timeout)
		conn, err := dialRelayAddresses(ipv6Ctx, o.ipv6Outbound, network, destination, addresses6, strategy, networkType, fallbackNetworkType, fallbackDelay)
		cancel()
		if err == nil {
			return conn, nil
		}
		ipv6Err = err
	}
	if len(addresses4) > 0 {
		conn, err := o.dialIPv4(ctx, network, destination, addresses4, strategy, networkType, fallbackNetworkType, fallbackDelay)
		if err == nil {
			return conn, nil
		}
		if ipv6Err != nil {
			return nil, E.Errors(E.Cause(ipv6Err, "IPv6 attempt failed"), E.Cause(err, "IPv4 SOCKS5 fallback failed"))
		}
		return nil, E.Cause(err, "IPv4 SOCKS5 connection failed")
	}
	if ipv6Err != nil {
		return nil, E.Cause(ipv6Err, "IPv6 connection failed and the destination has no IPv4 address")
	}
	return o.DialContext(ctx, network, destination)
}

func (o *relayFallbackOutbound) ListenSerialNetworkPacket(ctx context.Context, destination M.Socksaddr, destinationAddresses []netip.Addr, _ *C.NetworkStrategy, _ []C.InterfaceType, _ []C.InterfaceType, _ time.Duration) (net.PacketConn, netip.Addr, error) {
	addresses6, addresses4 := splitRelayDestinationAddresses(destination, destinationAddresses)
	var ipv6Err error
	for _, address := range addresses6 {
		conn, err := o.ipv6Outbound.ListenPacket(ctx, M.SocksaddrFrom(address, destination.Port))
		if err == nil {
			return conn, address, nil
		}
		ipv6Err = err
	}
	if len(addresses4) > 0 {
		conn, address, ipv4Err := o.listenIPv4Packet(ctx, destination, addresses4)
		if ipv4Err == nil {
			return conn, address, nil
		}
		if ipv6Err != nil {
			return nil, netip.Addr{}, E.Errors(E.Cause(ipv6Err, "IPv6 packet attempt failed"), E.Cause(ipv4Err, "IPv4 SOCKS5 packet fallback failed"))
		}
		return nil, netip.Addr{}, E.Cause(ipv4Err, "IPv4 SOCKS5 packet connection failed")
	}
	if ipv6Err != nil {
		return nil, netip.Addr{}, ipv6Err
	}
	conn, err := o.ListenPacket(ctx, destination)
	return conn, destination.Addr, err
}

func (o *relayFallbackOutbound) dialIPv4(ctx context.Context, network string, destination M.Socksaddr, addresses []netip.Addr, strategy *C.NetworkStrategy, networkType []C.InterfaceType, fallbackNetworkType []C.InterfaceType, fallbackDelay time.Duration) (net.Conn, error) {
	addresses4, err := o.ipv4Addresses(ctx, destination, addresses)
	if err != nil {
		return nil, err
	}
	if len(addresses4) == 0 {
		return o.ipv4Outbound.DialContext(ctx, network, destination)
	}
	return dialRelayAddresses(ctx, o.ipv4Outbound, network, destination, addresses4, strategy, networkType, fallbackNetworkType, fallbackDelay)
}

func (o *relayFallbackOutbound) listenIPv4Packet(ctx context.Context, destination M.Socksaddr, addresses []netip.Addr) (net.PacketConn, netip.Addr, error) {
	addresses4, err := o.ipv4Addresses(ctx, destination, addresses)
	if err != nil {
		return nil, netip.Addr{}, err
	}
	if len(addresses4) == 0 {
		conn, err := o.ipv4Outbound.ListenPacket(ctx, destination)
		return conn, destination.Addr, err
	}
	var packetErrors []error
	for _, address := range addresses4 {
		conn, err := o.ipv4Outbound.ListenPacket(ctx, M.SocksaddrFrom(address, destination.Port))
		if err == nil {
			return conn, address, nil
		}
		packetErrors = append(packetErrors, err)
	}
	return nil, netip.Addr{}, E.Errors(packetErrors...)
}

func (o *relayFallbackOutbound) ipv4Addresses(ctx context.Context, destination M.Socksaddr, addresses []netip.Addr) ([]netip.Addr, error) {
	_, addresses4 := splitRelayDestinationAddresses(destination, addresses)
	if len(addresses4) > 0 || !destination.IsDomain() || o.dnsRouter == nil {
		return addresses4, nil
	}
	resolved, err := o.dnsRouter.Lookup(ctx, destination.Fqdn, adapter.DNSQueryOptions{Strategy: C.DomainStrategyIPv4Only})
	if err != nil {
		return nil, E.Cause(err, "IPv4 DNS lookup failed")
	}
	_, addresses4 = splitRelayDestinationAddresses(destination, resolved)
	if len(addresses4) == 0 {
		return nil, E.New("destination has no IPv4 address")
	}
	return addresses4, nil
}

func splitRelayDestinationAddresses(destination M.Socksaddr, addresses []netip.Addr) ([]netip.Addr, []netip.Addr) {
	if len(addresses) == 0 && destination.IsIP() {
		addresses = []netip.Addr{destination.Addr}
	}
	addresses6 := make([]netip.Addr, 0, len(addresses))
	addresses4 := make([]netip.Addr, 0, len(addresses))
	for _, address := range addresses {
		if address.Is6() && !address.Is4In6() {
			addresses6 = append(addresses6, address)
		} else if address.Is4() || address.Is4In6() {
			addresses4 = append(addresses4, address)
		}
	}
	return addresses6, addresses4
}

func dialRelayAddresses(ctx context.Context, outbound adapter.Outbound, network string, destination M.Socksaddr, addresses []netip.Addr, strategy *C.NetworkStrategy, networkType []C.InterfaceType, fallbackNetworkType []C.InterfaceType, fallbackDelay time.Duration) (net.Conn, error) {
	if parallel, ok := outbound.(dialer.ParallelNetworkDialer); ok {
		return parallel.DialParallelNetwork(ctx, network, destination, addresses, strategy, networkType, fallbackNetworkType, fallbackDelay)
	}
	if parallel, ok := outbound.(N.ParallelDialer); ok {
		return parallel.DialParallel(ctx, network, destination, addresses)
	}
	var errors []error
	for _, address := range addresses {
		conn, err := outbound.DialContext(ctx, network, M.SocksaddrFrom(address, destination.Port))
		if err == nil {
			return conn, nil
		}
		errors = append(errors, err)
	}
	return nil, E.Errors(errors...)
}
