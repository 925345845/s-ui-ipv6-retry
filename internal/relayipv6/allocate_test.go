package relayipv6

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeNetwork struct {
	mu                sync.Mutex
	present           map[string]bool
	checks            map[string]int
	deleted           []string
	added             []string
	check             func(Slot) error
	addErr, deleteErr error
}

func (n *fakeNetwork) ops() Operations {
	return Operations{
		Add: func(s Slot) error {
			n.mu.Lock()
			defer n.mu.Unlock()
			if n.addErr != nil {
				return n.addErr
			}
			if n.present[s.Address] {
				return fmt.Errorf("duplicate address %s", s.Address)
			}
			n.present[s.Address] = true
			n.added = append(n.added, s.Address)
			return nil
		},
		Delete: func(s Slot) error {
			n.mu.Lock()
			defer n.mu.Unlock()
			if n.deleteErr != nil {
				return n.deleteErr
			}
			delete(n.present, s.Address)
			n.deleted = append(n.deleted, s.Address)
			return nil
		},
		Check: func(ctx context.Context, s Slot) error {
			n.mu.Lock()
			defer n.mu.Unlock()
			if !n.present[s.Address] {
				return errors.New("address not bound")
			}
			n.checks[s.Address]++
			if n.check != nil {
				return n.check(s)
			}
			return nil
		},
	}
}

func network() *fakeNetwork {
	return &fakeNetwork{present: map[string]bool{}, checks: map[string]int{}}
}
func deadline(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)
	return ctx
}
func slot(i int) Slot {
	return Slot{Address: fmt.Sprintf("2001:db8::%x", i+1), Interface: fmt.Sprintf("row-%d", i), Prefix: 64}
}

func TestFillReplacesOnlyFailedSlotsAndPreserves500Rows(t *testing.T) {
	n := network()
	slots := make([]Slot, 500)
	original := make([]Slot, len(slots))
	failures := map[string]int{}
	for i := range slots {
		slots[i] = slot(i)
		if i%7 == 0 {
			failures[slots[i].Interface] = 3
		}
	}
	copy(original, slots)
	n.check = func(s Slot) error {
		if failures[s.Interface] > 0 {
			failures[s.Interface]--
			return errors.New("egress unavailable")
		}
		return nil
	}
	added, err := Fill(deadline(t), slots, nil, true, 8, n.ops())
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 500 || len(n.present) != 500 {
		t.Fatalf("owned=%d present=%d", len(added), len(n.present))
	}
	seen := map[string]bool{}
	for i, s := range slots {
		if !s.Ready || !s.Owned || s.Interface != original[i].Interface || s.Prefix != original[i].Prefix {
			t.Fatalf("row %d changed identity or is not ready: %+v", i, s)
		}
		if seen[s.Address] {
			t.Fatal("duplicate replacement", s.Address)
		}
		seen[s.Address] = true
		if i%7 != 0 && (s.Address != original[i].Address || n.checks[s.Address] != 1) {
			t.Fatalf("healthy row %d was replaced/rechecked", i)
		}
		if !netip.MustParsePrefix("2001:db8::/64").Contains(netip.MustParseAddr(s.Address)) {
			t.Fatal("replacement escaped prefix")
		}
	}
	if len(n.deleted) != 72*3 {
		t.Fatalf("deleted %d failed addresses, want %d", len(n.deleted), 72*3)
	}
}

func TestFillDoesNotDeleteExistingAddress(t *testing.T) {
	n := network()
	s := slot(0)
	n.present[s.Address] = true
	existing := map[string]bool{s.Address: true}
	n.check = func(candidate Slot) error {
		if candidate.Address == s.Address {
			return errors.New("bad")
		}
		return nil
	}
	slots := []Slot{s}
	owned, err := Fill(deadline(t), slots, existing, true, 8, n.ops())
	if err != nil {
		t.Fatal(err)
	}
	if len(owned) != 1 || len(n.deleted) != 0 || !n.present[s.Address] {
		t.Fatal("modified existing system address")
	}
	if !reflect.DeepEqual(existing, map[string]bool{s.Address: true}) {
		t.Fatal("mutated existing address snapshot")
	}
}

func TestFillWithoutAddPermission(t *testing.T) {
	for _, assigned := range []bool{false, true} {
		t.Run(fmt.Sprint(assigned), func(t *testing.T) {
			n := network()
			s := slot(0)
			n.present[s.Address] = assigned
			n.check = func(Slot) error { return errors.New("bad") }
			_, err := Fill(deadline(t), []Slot{s}, map[string]bool{s.Address: assigned}, false, 1, n.ops())
			if err == nil || !strings.Contains(err.Error(), "enable system address creation") {
				t.Fatalf("unexpected error %v", err)
			}
			if len(n.added)+len(n.deleted) != 0 {
				t.Fatal("changed system addresses without permission")
			}
		})
	}
}

func TestFillExhaustedPrefixTerminatesAndCleansFailedAddresses(t *testing.T) {
	n := network()
	n.check = func(Slot) error { return errors.New("bad") }
	s := slot(0)
	s.Prefix = 127
	owned, err := Fill(deadline(t), []Slot{s}, nil, true, 1, n.ops())
	if err == nil || !strings.Contains(err.Error(), "no unused replacement") {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(owned) != 0 || len(n.present) != 0 || len(n.deleted) != 2 {
		t.Fatalf("leaked failed candidates: %+v", n)
	}
}

func TestFillCancellationNeverReportsSuccessAndReturnsCleanup(t *testing.T) {
	n := network()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ops := n.ops()
	ops.Check = func(context.Context, Slot) error { cancel(); return nil }
	slots := []Slot{slot(0)}
	owned, err := Fill(ctx, slots, nil, true, 1, ops)
	if !errors.Is(err, context.Canceled) || len(owned) != 1 || slots[0].Ready {
		t.Fatalf("err=%v owned=%v slots=%v", err, owned, slots)
	}
}

func TestFillDeadlineDuringProbe(t *testing.T) {
	n := network()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	ops := n.ops()
	ops.Check = func(ctx context.Context, _ Slot) error { <-ctx.Done(); return ctx.Err() }
	owned, err := Fill(ctx, []Slot{slot(0)}, nil, true, 1, ops)
	if !errors.Is(err, context.DeadlineExceeded) || len(owned) != 1 {
		t.Fatalf("err=%v owned=%v", err, owned)
	}
}

func TestFillDeleteFailureReturnsAddressForCleanup(t *testing.T) {
	n := network()
	n.deleteErr = errors.New("delete failed")
	n.check = func(Slot) error { return errors.New("bad") }
	owned, err := Fill(deadline(t), []Slot{slot(0)}, nil, true, 1, n.ops())
	if !errors.Is(err, n.deleteErr) || len(owned) != 1 || len(n.added) != 1 {
		t.Fatalf("err=%v owned=%v", err, owned)
	}
}

func TestFillAddFailureStopsWithoutRetrying(t *testing.T) {
	n := network()
	n.addErr = errors.New("permission denied")
	owned, err := Fill(deadline(t), []Slot{slot(0)}, nil, true, 1, n.ops())
	if !errors.Is(err, n.addErr) || len(owned) != 0 {
		t.Fatalf("err=%v owned=%v", err, owned)
	}
}

func TestFillChecksAllRowsBeforeRetrying(t *testing.T) {
	n := network()
	var order []string
	n.check = func(s Slot) error {
		order = append(order, s.Interface)
		if len(order) == 1 {
			return errors.New("bad")
		}
		return nil
	}
	_, err := Fill(deadline(t), []Slot{slot(0), slot(1), slot(2)}, nil, true, 1, n.ops())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(order, []string{"row-0", "row-1", "row-2", "row-0"}) {
		t.Fatalf("unfair retries: %v", order)
	}
}

func TestFillRequiresDeadlineAndUniqueSlots(t *testing.T) {
	n := network()
	if _, err := Fill(context.Background(), []Slot{slot(0)}, nil, true, 1, n.ops()); err == nil {
		t.Fatal("accepted unbounded context")
	}
	if _, err := Fill(deadline(t), []Slot{slot(0), slot(0)}, nil, true, 1, n.ops()); err == nil {
		t.Fatal("accepted duplicate slots")
	}
}
