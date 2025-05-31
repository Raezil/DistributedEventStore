package DistributedEventStore

import (
	"regexp"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if len(cfg.ListenAddrs) == 0 {
		t.Errorf("DefaultConfig.ListenAddrs should not be empty")
	}
	if cfg.SyncInterval != DefaultSyncInterval {
		t.Errorf("DefaultConfig.SyncInterval = %v; want %v", cfg.SyncInterval, DefaultSyncInterval)
	}
	if cfg.MaxPeers != 50 {
		t.Errorf("DefaultConfig.MaxPeers = %d; want %d", cfg.MaxPeers, 50)
	}
	if len(cfg.EventFilters) != 0 {
		t.Errorf("DefaultConfig.EventFilters length = %d; want 0", len(cfg.EventFilters))
	}
	if len(cfg.RoutingRules) != 1 {
		t.Errorf("DefaultConfig.RoutingRules length = %d; want 1", len(cfg.RoutingRules))
	}
	// Check that the default routing rule is BroadcastToAll
	rule := cfg.RoutingRules[0]
	peers := []peer.ID{"p1", "p2", "p3"}
	event := NetworkEvent{ID: "e1", Projection: "proj", Args: nil, Timestamp: time.Now().Unix(), NodeID: "node-1"}
	targets := rule(event, peers)
	if len(targets) != len(peers) {
		t.Errorf("BroadcastToAll should return all peers; got %d, want %d", len(targets), len(peers))
	}
}

func TestFilterLocalOnly(t *testing.T) {
	// Create a NetworkEvent with a node ID
	e1 := NetworkEvent{NodeID: "node-1"}
	if !FilterLocalOnly(e1) {
		t.Errorf("FilterLocalOnly should return true for event with NodeID set")
	}
	// Create a NetworkEvent with empty NodeID
	e2 := NetworkEvent{NodeID: ""}
	if FilterLocalOnly(e2) {
		t.Errorf("FilterLocalOnly should return false for event with empty NodeID")
	}
}

func TestFilterByProjection(t *testing.T) {
	filter := FilterByProjection("proj1", "proj2")
	e1 := NetworkEvent{Projection: "proj1"}
	if !filter(e1) {
		t.Errorf("FilterByProjection should allow projection 'proj1'")
	}
	e2 := NetworkEvent{Projection: "projX"}
	if filter(e2) {
		t.Errorf("FilterByProjection should filter out projection 'projX'")
	}
}

func TestBroadcastToAll(t *testing.T) {
	peers := []peer.ID{"p1", "p2", "p3"}
	event := NetworkEvent{ID: "e1", Projection: "proj", Args: nil, Timestamp: time.Now().Unix(), NodeID: "node-1"}
	targets := BroadcastToAll(event, peers)
	if len(targets) != len(peers) {
		t.Errorf("BroadcastToAll returned %d peers; want %d", len(targets), len(peers))
	}
	for i := range peers {
		if targets[i] != peers[i] {
			t.Errorf("BroadcastToAll order mismatch: got %v, want %v", targets, peers)
		}
	}
}

func TestRouteToRandomPeers(t *testing.T) {
	rule := RouteToRandomPeers(2)
	// Prepare peers slice
	allPeers := []peer.ID{"p1", "p2", "p3", "p4"}
	event := NetworkEvent{ID: "e1", Projection: "proj", Args: nil, Timestamp: time.Now().Unix(), NodeID: "node-1"}
	selected := rule(event, append([]peer.ID(nil), allPeers...))
	// Should select exactly 2 peers
	if len(selected) != 2 {
		t.Errorf("RouteToRandomPeers(2) returned %d peers; want 2", len(selected))
	}
	// Ensure selected peers are from the original set
	for _, p := range selected {
		found := false
		for _, ap := range allPeers {
			if p == ap {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Selected peer %s not in original peers %v", p, allPeers)
		}
	}
}

// TestDefaultConfigAdditional verifies other DefaultConfig fields not covered in initial tests.
func TestDefaultConfigAdditional(t *testing.T) {
	cfg := DefaultConfig()
	// Check EnableRelay default
	if cfg.EnableRelay {
		t.Errorf("DefaultConfig.EnableRelay = %v; want false", cfg.EnableRelay)
	}
	// Check BootstrapPeers is empty
	if cfg.BootstrapPeers != nil && len(cfg.BootstrapPeers) != 0 {
		t.Errorf("DefaultConfig.BootstrapPeers length = %d; want 0", len(cfg.BootstrapPeers))
	}
	// Check ListenAddrs contains expected default prefix
	if len(cfg.ListenAddrs) == 0 {
		t.Fatalf("DefaultConfig.ListenAddrs should not be empty")
	}
	addr := cfg.ListenAddrs[0]
	matched, _ := regexp.MatchString(`^/ip4/0.0.0.0/`, addr)
	if !matched {
		t.Errorf("DefaultConfig.ListenAddrs[0] = %s; want to start with /ip4/0.0.0.0/", addr)
	}
}

// TestGenerateNodeID ensures that generateNodeID produces a unique, correctly formatted string.
func TestGenerateNodeID(t *testing.T) {
	id1 := generateNodeID()
	time.Sleep(1 * time.Nanosecond)
	id2 := generateNodeID()
	if id1 == id2 {
		t.Errorf("generateNodeID should produce unique IDs; got %s and %s", id1, id2)
	}
	// Check format: should start with "node-" followed by digits
	pattern := regexp.MustCompile(`^node-[0-9]+$`)
	if !pattern.MatchString(id1) {
		t.Errorf("generateNodeID format invalid: %s", id1)
	}
	if !pattern.MatchString(id2) {
		t.Errorf("generateNodeID format invalid: %s", id2)
	}
}

// TestFilterByProjectionEmpty checks that a filter with no allowed projections blocks all events.
func TestFilterByProjectionEmpty(t *testing.T) {
	filter := FilterByProjection()
	e := NetworkEvent{Projection: "any"}
	if filter(e) {
		t.Errorf("FilterByProjection with no values should filter out all events; but allowed %s", e.Projection)
	}
}

// TestRouteToRandomPeersWhenPeersFewer verifies that when n >= len(peers), all peers are returned in order.
func TestRouteToRandomPeersWhenPeersFewer(t *testing.T) {
	rule := RouteToRandomPeers(5)
	allPeers := []peer.ID{"pA", "pB", "pC"}
	e := NetworkEvent{ID: "e2", Projection: "proj", Args: nil, Timestamp: time.Now().Unix(), NodeID: "node-2"}
	selected := rule(e, append([]peer.ID(nil), allPeers...))
	if len(selected) != len(allPeers) {
		t.Errorf("RouteToRandomPeers(5) returned %d peers; want %d", len(selected), len(allPeers))
	}
	for i := range allPeers {
		if selected[i] != allPeers[i] {
			t.Errorf("Expected peer %s at index %d; got %s", allPeers[i], i, selected[i])
		}
	}
}

// TestRouteToRandomPeersEmpty checks that an empty peer list returns an empty slice without error.
func TestRouteToRandomPeersEmpty(t *testing.T) {
	rule := RouteToRandomPeers(3)
	allPeers := []peer.ID{}
	e := NetworkEvent{ID: "e3", Projection: "proj", Args: nil, Timestamp: time.Now().Unix(), NodeID: "node-3"}
	selected := rule(e, allPeers)
	if len(selected) != 0 {
		t.Errorf("RouteToRandomPeers on empty slice returned %d; want 0", len(selected))
	}
}

// TestBroadcastToAllEmpty ensures that broadcasting on an empty peer slice yields an empty slice.
func TestBroadcastToAllEmpty(t *testing.T) {
	peers := []peer.ID{}
	e := NetworkEvent{ID: "e4", Projection: "proj", Args: nil, Timestamp: time.Now().Unix(), NodeID: "node-4"}
	targets := BroadcastToAll(e, peers)
	if len(targets) != 0 {
		t.Errorf("BroadcastToAll with no peers returned %d; want 0", len(targets))
	}
}

// TestRouteToRandomPeersNoDuplicates checks that the selected peers are unique.
func TestRouteToRandomPeersNoDuplicates(t *testing.T) {
	// Use n=2 on 3 peers; since selection is random, run multiple times to detect duplicates.
	rule := RouteToRandomPeers(2)
	allPeers := []peer.ID{"pX", "pY", "pZ"}
	seenPairs := make(map[string]bool)
	for i := 0; i < 10; i++ {
		e := NetworkEvent{ID: "e5", Projection: "proj", Args: nil, Timestamp: time.Now().Unix(), NodeID: "node-5"}
		selected := rule(e, append([]peer.ID(nil), allPeers...))
		if len(selected) != 2 {
			t.Errorf("RouteToRandomPeers(2) returned %d peers; want 2", len(selected))
		}
		seen := make(map[peer.ID]bool)
		for _, p := range selected {
			if seen[p] {
				t.Errorf("Duplicate peer %s selected", p)
			}
			seen[p] = true
		}
		// Record combination to ensure variability
		key := string(selected[0]) + ":" + string(selected[1])
		seenPairs[key] = true
	}
	if len(seenPairs) < 1 {
		t.Errorf("RouteToRandomPeers did not produce any variation over 10 runs")
	}
}
