package database

import "testing"

func TestMaxOpenConnectionsForLowResourceHosts(t *testing.T) {
	if got := maxOpenConnections(1); got != 4 {
		t.Fatalf("single-core pool = %d, want 4", got)
	}
	if got := maxOpenConnections(8); got != 8 {
		t.Fatalf("multi-core pool = %d, want 8", got)
	}
}
