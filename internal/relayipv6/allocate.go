// Package relayipv6 fills fixed relay slots without changing their upstream order.
package relayipv6

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/netip"
	"sync"
)

type Slot struct {
	Address   string
	Interface string
	Prefix    int
	Owned     bool
	Ready     bool
}

type Operations struct {
	Add    func(Slot) error
	Delete func(Slot) error
	Check  func(context.Context, Slot) error
	// Failed receives the stable, zero-based upstream index. Never log credentials.
	Failed func(int, Slot, error)
}

// Fill checks every slot independently and replaces only failed addresses.
// The caller must provide a deadline. Successful slots are never checked again.
// Returned owned addresses must be removed by the caller if persistence fails.
// Existing system addresses are never removed. occupied is copied, not mutated.
func Fill(ctx context.Context, slots []Slot, occupied map[string]bool, allowAdd bool, workers int, ops Operations) ([]Slot, error) {
	if _, ok := ctx.Deadline(); !ok {
		return nil, fmt.Errorf("IPv6 allocation requires a deadline")
	}
	if workers < 1 {
		workers = 1
	}
	if workers > len(slots) {
		workers = len(slots)
	}
	used := make(map[string]bool, len(occupied)+len(slots))
	for address, present := range occupied {
		if present {
			used[address] = true
		}
	}
	initial := make(map[string]bool, len(slots))
	for _, slot := range slots {
		address, err := netip.ParseAddr(slot.Address)
		if err != nil || !address.Is6() || address.Is4In6() || slot.Prefix < 1 || slot.Prefix > 128 {
			return nil, fmt.Errorf("invalid IPv6 allocation slot %q/%d", slot.Address, slot.Prefix)
		}
		if initial[address.String()] || slot.Owned {
			return nil, fmt.Errorf("duplicate or already owned allocation slot %s", address)
		}
		initial[address.String()] = true
		used[address.String()] = true
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var mu sync.Mutex
	owned := make(map[string]Slot)
	var firstError error
	stop := func(err error) {
		mu.Lock()
		defer mu.Unlock()
		if firstError == nil {
			firstError = err
			cancel()
		}
	}
	pending := make([]int, len(slots))
	for i := range slots {
		pending[i] = i
	}
	for len(pending) > 0 && ctx.Err() == nil {
		jobs := make(chan int, len(pending))
		for _, index := range pending {
			jobs <- index
		}
		close(jobs)
		var retry []int
		var wg sync.WaitGroup
		for range workers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for index := range jobs {
					if err := ctx.Err(); err != nil {
						return
					}
					slot := slots[index]
					if !occupied[slot.Address] && !slot.Owned {
						if !allowAdd {
							stop(fmt.Errorf("upstream %d: IPv6 %s is not assigned; enable system address creation", index+1, slot.Address))
							return
						}
						if err := ops.Add(slot); err != nil {
							stop(fmt.Errorf("upstream %d: add IPv6: %w", index+1, err))
							return
						}
						slot.Owned = true
						slots[index] = slot
						mu.Lock()
						owned[slot.Address] = slot
						mu.Unlock()
					}
					err := ops.Check(ctx, slot)
					if err == nil {
						if ctx.Err() != nil {
							return
						}
						slots[index].Ready = true
						continue
					}
					if ctx.Err() != nil {
						return
					}
					if !allowAdd {
						stop(fmt.Errorf("upstream %d: IPv6 %s failed; enable system address creation to replace it: %w", index+1, slot.Address, err))
						return
					}
					if ops.Failed != nil {
						ops.Failed(index, slot, err)
					}
					if slot.Owned {
						if err := ops.Delete(slot); err != nil {
							stop(fmt.Errorf("upstream %d: remove failed IPv6: %w", index+1, err))
							return
						}
						mu.Lock()
						delete(owned, slot.Address)
						mu.Unlock()
					}
					mu.Lock()
					next, err := nextAddress(netip.MustParseAddr(slot.Address), slot.Prefix, used)
					if err == nil {
						used[next.String()] = true
					}
					mu.Unlock()
					if err != nil {
						stop(fmt.Errorf("upstream %d: %w", index+1, err))
						return
					}
					slots[index].Address = next.String()
					slots[index].Owned = false
					mu.Lock()
					retry = append(retry, index)
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		pending = retry
	}
	added := make([]Slot, 0, len(owned))
	for _, slot := range owned {
		added = append(added, slot)
	}
	if firstError != nil {
		return added, firstError
	}
	return added, ctx.Err()
}

// Start randomly, then scan past occupied addresses. Unlike random-only retries,
// this terminates even for exhausted /127 or /128 prefixes.
func nextAddress(base netip.Addr, bits int, used map[string]bool) (netip.Addr, error) {
	prefix := netip.PrefixFrom(base, bits).Masked()
	value := prefix.Addr().As16()
	var entropy [16]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return netip.Addr{}, err
	}
	for bit := bits; bit < 128; bit++ {
		mask := byte(1 << (7 - bit%8))
		value[bit/8] |= entropy[bit/8] & mask
	}
	start := netip.AddrFrom16(value)
	candidate := start
	for range len(used) + 1 {
		if !used[candidate.String()] {
			return candidate, nil
		}
		candidate = candidate.Next()
		if !candidate.IsValid() || !prefix.Contains(candidate) {
			candidate = prefix.Addr()
		}
		if candidate == start {
			break
		}
	}
	return netip.Addr{}, fmt.Errorf("IPv6 prefix %s has no unused replacement addresses", prefix)
}
