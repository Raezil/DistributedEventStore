package DistributedEventStore

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"github.com/Raezil/GoEventBus"
	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/libp2p/go-libp2p/p2p/discovery/mdns"
	"github.com/libp2p/go-libp2p/p2p/net/connmgr"
	"github.com/multiformats/go-multiaddr"
)

const (
	// Protocol ID for distributed event bus
	EventBusProtocol protocol.ID = "/eventbus/1.0.0"
	// mDNS service name
	ServiceName = "eventbus-discovery"
	// Default sync interval
	DefaultSyncInterval = 5 * time.Second
)

// NetworkEvent represents an event that can be transmitted over the network
type NetworkEvent struct {
	ID         string                 `json:"id"`
	Projection string                 `json:"projection"`
	Args       map[string]interface{} `json:"args"`
	Timestamp  int64                  `json:"timestamp"`
	NodeID     string                 `json:"node_id"`
	Signature  string                 `json:"signature,omitempty"`
}

// PeerInfo holds information about discovered peers
type PeerInfo struct {
	ID        peer.ID
	Addresses []multiaddr.Multiaddr
	LastSeen  time.Time
}

// DistributedEventBus extends GoEventBus with P2P capabilities
type DistributedEventBus struct {
	*GoEventBus.EventStore

	// libp2p components
	host        host.Host
	mdnsService mdns.Service

	// Peer management
	peers    map[peer.ID]*PeerInfo
	peersMux sync.RWMutex

	// Configuration
	nodeID       string
	syncInterval time.Duration

	// Event filtering and routing
	eventFilters []EventFilter
	routingRules []RoutingRule

	// Statistics
	sentEvents     uint64
	receivedEvents uint64

	// Control channels
	ctx          context.Context
	cancel       context.CancelFunc
	shutdownOnce sync.Once
}

// EventFilter determines if an event should be synchronized
type EventFilter func(event NetworkEvent) bool

// RoutingRule determines which peers should receive an event
type RoutingRule func(event NetworkEvent, peers []peer.ID) []peer.ID

// Config holds configuration for the distributed event bus
type Config struct {
	ListenAddrs    []string
	SyncInterval   time.Duration
	MaxPeers       int
	EventFilters   []EventFilter
	RoutingRules   []RoutingRule
	EnableRelay    bool
	BootstrapPeers []string
}

// DefaultConfig returns a sensible default configuration
func DefaultConfig() *Config {
	return &Config{
		ListenAddrs:  []string{"/ip4/0.0.0.0/tcp/0"},
		SyncInterval: DefaultSyncInterval,
		MaxPeers:     50,
		EventFilters: []EventFilter{},
		RoutingRules: []RoutingRule{BroadcastToAll},
		EnableRelay:  false,
	}
}

// NewDistributedEventBus creates a new distributed event bus
func NewDistributedEventBus(
	dispatcher *GoEventBus.Dispatcher,
	bufferSize uint64,
	policy GoEventBus.OverrunPolicy,
	config *Config,
) (*DistributedEventBus, error) {
	if config == nil {
		config = DefaultConfig()
	}

	// Create base event store
	eventStore := GoEventBus.NewEventStore(dispatcher, bufferSize, policy)

	// Create context for shutdown coordination
	ctx, cancel := context.WithCancel(context.Background())

	// Initialize distributed event bus
	deb := &DistributedEventBus{
		EventStore:   eventStore,
		peers:        make(map[peer.ID]*PeerInfo),
		nodeID:       generateNodeID(),
		syncInterval: config.SyncInterval,
		eventFilters: config.EventFilters,
		routingRules: config.RoutingRules,
		ctx:          ctx,
		cancel:       cancel,
	}

	// Create libp2p host
	host, err := deb.createHost(config)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create libp2p host: %w", err)
	}
	deb.host = host

	// Set up stream handler for incoming events
	host.SetStreamHandler(EventBusProtocol, deb.handleIncomingStream)

	// Start mDNS discovery
	if err := deb.startMDNSDiscovery(); err != nil {
		cancel()
		host.Close()
		return nil, fmt.Errorf("failed to start mDNS discovery: %w", err)
	}

	// Connect to bootstrap peers
	if len(config.BootstrapPeers) > 0 {
		go deb.connectToBootstrapPeers(config.BootstrapPeers)
	}

	// Start background sync routine
	go deb.syncRoutine()

	// Add middleware to intercept and distribute events
	deb.EventStore.OnAfter(func(ctx context.Context, evt GoEventBus.Event, result GoEventBus.Result, err error) {
		if err != nil {
			return
		}

		if remote, ok := evt.Args["_remote"].(bool); ok && remote {
			return
		}

		networkEvent := NetworkEvent{
			ID:         evt.ID,
			Projection: evt.Projection.(string),
			Args:       evt.Args,
			Timestamp:  time.Now().Unix(),
			NodeID:     deb.nodeID,
		}

		for _, filter := range deb.eventFilters {
			if !filter(networkEvent) {
				return
			}
		}
		go deb.distributeEvent(networkEvent)
	})

	log.Printf("Distributed EventBus started with ID: %s", deb.nodeID)
	log.Printf("Listening on: %v", host.Addrs())

	return deb, nil
}

// createHost sets up the libp2p host with appropriate options
func (deb *DistributedEventBus) createHost(config *Config) (host.Host, error) {
	var opts []libp2p.Option

	// Listen addresses
	for _, addr := range config.ListenAddrs {
		opts = append(opts, libp2p.ListenAddrStrings(addr))
	}

	// Connection manager to limit peer connections
	if config.MaxPeers > 0 {
		connMgr, err := connmgr.NewConnManager(
			config.MaxPeers/2, // low watermark
			config.MaxPeers,   // high watermark
			connmgr.WithGracePeriod(time.Minute),
		)
		if err != nil {
			return nil, err
		}
		opts = append(opts, libp2p.ConnectionManager(connMgr))
	}

	// Enable relay if configured
	if config.EnableRelay {
		opts = append(opts, libp2p.EnableRelay())
	}

	return libp2p.New(opts...)
}

// startMDNSDiscovery initializes mDNS peer discovery
func (deb *DistributedEventBus) startMDNSDiscovery() error {
	mdnsService := mdns.NewMdnsService(deb.host, ServiceName, deb)
	deb.mdnsService = mdnsService
	return mdnsService.Start()
}

// HandlePeerFound is called when a new peer is discovered via mDNS
func (deb *DistributedEventBus) HandlePeerFound(peerInfo peer.AddrInfo) {
	deb.peersMux.Lock()
	defer deb.peersMux.Unlock()

	if peerInfo.ID == deb.host.ID() {
		return // Skip self
	}

	// Update peer info
	deb.peers[peerInfo.ID] = &PeerInfo{
		ID:        peerInfo.ID,
		Addresses: peerInfo.Addrs,
		LastSeen:  time.Now(),
	}

	// Connect to the peer
	go func() {
		ctx, cancel := context.WithTimeout(deb.ctx, 30*time.Second)
		defer cancel()

		if err := deb.host.Connect(ctx, peerInfo); err != nil {
			log.Printf("Failed to connect to discovered peer %s: %v", peerInfo.ID, err)
		} else {
			log.Printf("Connected to peer: %s", peerInfo.ID)
		}
	}()
}

// connectToBootstrapPeers connects to specified bootstrap peers
func (deb *DistributedEventBus) connectToBootstrapPeers(bootstrapPeers []string) {
	for _, peerAddr := range bootstrapPeers {
		addr, err := multiaddr.NewMultiaddr(peerAddr)
		if err != nil {
			log.Printf("Invalid bootstrap peer address %s: %v", peerAddr, err)
			continue
		}

		peerInfo, err := peer.AddrInfoFromP2pAddr(addr)
		if err != nil {
			log.Printf("Failed to parse bootstrap peer %s: %v", peerAddr, err)
			continue
		}

		ctx, cancel := context.WithTimeout(deb.ctx, 30*time.Second)
		if err := deb.host.Connect(ctx, *peerInfo); err != nil {
			log.Printf("Failed to connect to bootstrap peer %s: %v", peerAddr, err)
		} else {
			log.Printf("Connected to bootstrap peer: %s", peerInfo.ID)
		}
		cancel()
	}
}

// distributionMiddleware intercepts events and distributes them to peers
func (deb *DistributedEventBus) DistributionMiddleware(next GoEventBus.HandlerFunc) GoEventBus.HandlerFunc {
	return func(ctx context.Context, args map[string]any) (GoEventBus.Result, error) {
		// Execute the handler first
		result, err := next(ctx, args)

		// Extract event information for distribution
		if eventID, ok := args["event_id"].(string); ok {
			if projection, ok := args["projection"].(string); ok {
				networkEvent := NetworkEvent{
					ID:         eventID,
					Projection: projection,
					Args:       args,
					Timestamp:  time.Now().Unix(),
					NodeID:     deb.nodeID,
				}

				// Apply filters
				shouldDistribute := true
				for _, filter := range deb.eventFilters {
					if !filter(networkEvent) {
						shouldDistribute = false
						break
					}
				}

				if shouldDistribute {
					go deb.distributeEvent(networkEvent)
				}
			}
		}

		return result, err
	}
}

// distributeEvent sends an event to appropriate peers
func (deb *DistributedEventBus) distributeEvent(event NetworkEvent) {
	deb.peersMux.RLock()
	peerIDs := make([]peer.ID, 0, len(deb.peers))
	for peerID := range deb.peers {
		peerIDs = append(peerIDs, peerID)
	}
	deb.peersMux.RUnlock()

	if len(peerIDs) == 0 {
		return
	}

	// Apply routing rules
	targetPeers := peerIDs
	for _, rule := range deb.routingRules {
		targetPeers = rule(event, targetPeers)
	}

	// Send to target peers
	for _, peerID := range targetPeers {
		go deb.sendEventToPeer(peerID, event)
	}
}

// sendEventToPeer sends a single event to a specific peer
func (deb *DistributedEventBus) sendEventToPeer(peerID peer.ID, event NetworkEvent) {
	ctx, cancel := context.WithTimeout(deb.ctx, 10*time.Second)
	defer cancel()

	stream, err := deb.host.NewStream(ctx, peerID, EventBusProtocol)
	if err != nil {
		log.Printf("Failed to create stream to peer %s: %v", peerID, err)
		return
	}
	defer stream.Close()

	encoder := json.NewEncoder(stream)
	if err := encoder.Encode(event); err != nil {
		log.Printf("Failed to send event to peer %s: %v", peerID, err)
		return
	}

	deb.sentEvents++
}

// handleIncomingStream processes events received from other peers
func (deb *DistributedEventBus) handleIncomingStream(stream network.Stream) {
	defer stream.Close()

	decoder := json.NewDecoder(stream)
	for {
		var event NetworkEvent
		if err := decoder.Decode(&event); err != nil {
			if err != io.EOF {
				log.Printf("Failed to decode incoming event: %v", err)
			}
			return
		}

		// Skip events from ourselves
		if event.NodeID == deb.nodeID {
			continue
		}

		deb.receivedEvents++
		deb.handleReceivedEvent(event)
	}
}

// handleReceivedEvent processes an event received from a peer
func (deb *DistributedEventBus) handleReceivedEvent(event NetworkEvent) {
	// Convert NetworkEvent back to GoEventBus.Event
	localEvent := GoEventBus.Event{
		ID:         event.ID,
		Projection: event.Projection,
		Args:       event.Args,
		Ctx:        deb.ctx,
	}

	// Add metadata about the source
	if localEvent.Args == nil {
		localEvent.Args = make(map[string]any)
	}
	localEvent.Args["_remote"] = true
	localEvent.Args["_source_node"] = event.NodeID
	localEvent.Args["_timestamp"] = event.Timestamp

	// Subscribe the event to our local event store
	if err := deb.EventStore.Subscribe(deb.ctx, localEvent); err != nil {
		log.Printf("Failed to subscribe received event: %v", err)
	}
}

// syncRoutine periodically publishes pending events
func (deb *DistributedEventBus) syncRoutine() {
	ticker := time.NewTicker(deb.syncInterval)
	defer ticker.Stop()

	for {
		select {
		case <-deb.ctx.Done():
			return
		case <-ticker.C:
			deb.EventStore.Publish()
			deb.cleanupStaleConnections()
		}
	}
}

// cleanupStaleConnections removes peers that haven't been seen recently
func (deb *DistributedEventBus) cleanupStaleConnections() {
	deb.peersMux.Lock()
	defer deb.peersMux.Unlock()

	staleThreshold := time.Now().Add(-5 * time.Minute)
	for peerID, peerInfo := range deb.peers {
		if peerInfo.LastSeen.Before(staleThreshold) {
			delete(deb.peers, peerID)
			// Close connection if it exists
			if deb.host.Network().Connectedness(peerID) == network.Connected {
				deb.host.Network().ClosePeer(peerID)
			}
		}
	}
}

// GetPeers returns information about currently connected peers
func (deb *DistributedEventBus) GetPeers() map[peer.ID]*PeerInfo {
	deb.peersMux.RLock()
	defer deb.peersMux.RUnlock()

	peers := make(map[peer.ID]*PeerInfo)
	for id, info := range deb.peers {
		peers[id] = &PeerInfo{
			ID:        info.ID,
			Addresses: append([]multiaddr.Multiaddr{}, info.Addresses...),
			LastSeen:  info.LastSeen,
		}
	}
	return peers
}

// NetworkStats returns network-related statistics
func (deb *DistributedEventBus) NetworkStats() (sent, received uint64, peerCount int) {
	deb.peersMux.RLock()
	peerCount = len(deb.peers)
	deb.peersMux.RUnlock()

	return deb.sentEvents, deb.receivedEvents, peerCount
}

// Close gracefully shuts down the distributed event bus
func (deb *DistributedEventBus) Close(ctx context.Context) error {
	var err error
	deb.shutdownOnce.Do(func() {
		// Cancel background routines
		deb.cancel()

		// Stop mDNS service
		if deb.mdnsService != nil {
			err = deb.mdnsService.Close()
		}

		// Close libp2p host
		if deb.host != nil {
			err = deb.host.Close()
		}

		// Close underlying event store
		if deb.EventStore != nil {
			if closeErr := deb.EventStore.Close(ctx); closeErr != nil && err == nil {
				err = closeErr
			}
		}
	})

	return err
}

// Utility functions

// generateNodeID creates a unique identifier for this node
func generateNodeID() string {
	return fmt.Sprintf("node-%d", time.Now().UnixNano())
}

// BroadcastToAll is a routing rule that sends events to all connected peers
func BroadcastToAll(event NetworkEvent, peers []peer.ID) []peer.ID {
	return peers
}

// FilterLocalOnly is an event filter that only allows local events
func FilterLocalOnly(event NetworkEvent) bool {
	return event.NodeID != "" // Only distribute events with a node ID
}

// FilterByProjection creates a filter that only allows specific projections
func FilterByProjection(allowedProjections ...string) EventFilter {
	allowed := make(map[string]bool)
	for _, proj := range allowedProjections {
		allowed[proj] = true
	}

	return func(event NetworkEvent) bool {
		return allowed[event.Projection]
	}
}

// RouteToRandomPeers creates a routing rule that sends to N random peers
func RouteToRandomPeers(n int) RoutingRule {
	return func(event NetworkEvent, peers []peer.ID) []peer.ID {
		if len(peers) <= n {
			return peers
		}

		// Simple random selection (not cryptographically secure)
		selected := make([]peer.ID, 0, n)
		for i := 0; i < n; i++ {
			idx := time.Now().UnixNano() % int64(len(peers))
			selected = append(selected, peers[idx])
			// Remove selected peer to avoid duplicates
			peers = append(peers[:idx], peers[idx+1:]...)
		}

		return selected
	}
}
