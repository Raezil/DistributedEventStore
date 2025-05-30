# DistributedEventStore

A peer-to-peer distributed event store for Go, built on top of [GoEventBus](https://github.com/Raezil/GoEventBus) and [libp2p](https://github.com/libp2p/go-libp2p).

This library enables you to publish and subscribe to events across a decentralized network of Go applications, without a central broker.

## Features

* **mDNS Peer Discovery**: Automatically discovers peers on the local network.
* **Configurable Filters & Routing**: Apply custom filters and routing rules for event distribution.
* **Backpressure Handling**: Integrates GoEventBus overrun policies (e.g., DropOldest, Block).
* **Relay & Bootstrap**: Enable relay hop support or connect to known bootstrap peers.
* **Statistics**: Track sent/received events and current peer connections.

## Installation

```bash
# Add to your module
go get github.com/Raezil/DistributedEventStore
```

## Usage
A complete example is provided in `examples/main.go`. To run it:

```bash
go run examples/server/main.go
```

### `examples/main.go`

```go
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/Raezil/DistributedEventStore"
	"github.com/Raezil/GoEventBus"
	"github.com/valyala/fasthttp"
)

// Application holds the main application state
type Application struct {
	subscribedEvents []GoEventBus.Event
	eventBus         *DistributedEventStore.DistributedEventBus
	ctx              context.Context
	cancel           context.CancelFunc
	wg               sync.WaitGroup
	config           *AppConfig
	userCounter      int
}

// AppConfig holds application configuration
type AppConfig struct {
	PublishInterval  time.Duration
	MaxUsers         int
	EnableStatistics bool
	NodeName         string
}

func main() {
	// Initialize configuration
	config := &AppConfig{
		PublishInterval:  5 * time.Second,
		MaxUsers:         100,
		EnableStatistics: true,
		NodeName:         fmt.Sprintf("node-%d", os.Getpid()),
	}

	// Create application instance
	app, err := NewApplication(config)
	if err != nil {
		log.Fatalf("Failed to create application: %v", err)
	}

	// Start the application
	if err := app.Start(); err != nil {
		log.Fatalf("Failed to start application: %v", err)
	}

	// Wait for shutdown signal
	app.WaitForShutdown()

	// Graceful shutdown
	if err := app.Shutdown(); err != nil {
		log.Printf("Error during shutdown: %v", err)
	}

	log.Println("Application shut down successfully")
}

// NewApplication creates a new application instance
func NewApplication(config *AppConfig) (*Application, error) {
	ctx, cancel := context.WithCancel(context.Background())

	// Create event dispatcher with multiple handlers
	dispatcher := createEventDispatcher()

	// Create distributed event bus configuration
	debConfig := &DistributedEventStore.Config{
		ListenAddrs:  []string{"/ip4/0.0.0.0/tcp/0"},
		SyncInterval: 3 * time.Second,
		MaxPeers:     20,
		EventFilters: []DistributedEventStore.EventFilter{
			DistributedEventStore.FilterLocalOnly,
		},
		RoutingRules: []DistributedEventStore.RoutingRule{
			DistributedEventStore.BroadcastToAll,
		},
		EnableRelay: true,
	}

	// Create distributed evedeb.EventStore.OnAftert bus
	deb, err := DistributedEventStore.NewDistributedEventBus(
		dispatcher,
		256, // buffer size
		GoEventBus.DropOldest,
		debConfig,
	)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create distributed event bus: %w", err)
	}

	app := &Application{
		eventBus: deb,
		ctx:      ctx,
		cancel:   cancel,
		config:   config,
	}

	return app, nil
}

// createEventDispatcher sets up event handlers for different event types
func createEventDispatcher() *GoEventBus.Dispatcher {
	return &GoEventBus.Dispatcher{
		"user_created": handleUserCreated,
		"user_updated": handleUserUpdated,
		"user_deleted": handleUserDeleted,
	}
}

// Event handlers
func handleUserCreated(ctx context.Context, args map[string]interface{}) (GoEventBus.Result, error) {
	username, _ := args["username"].(string)
	email, _ := args["email"].(string)
	userID, _ := args["user_id"].(int)

	if remote, ok := args["_remote"].(bool); ok && remote {
		sourceNode, _ := args["_source_node"].(string)
		timestamp := time.Unix(args["_timestamp"].(int64), 0)

		log.Printf("🌐 Remote user created - Node: %s, Time: %v, User: %s (%s) ID: %d",
			sourceNode, timestamp, username, email, userID)
	} else {
		log.Printf("👤 Local user created - User: %s (%s) ID: %d", username, email, userID)
	}

	return GoEventBus.Result{
		Message: fmt.Sprintf("User %s (ID: %d) created successfully", username, userID),
	}, nil
}

func handleUserUpdated(ctx context.Context, args map[string]interface{}) (GoEventBus.Result, error) {
	username, _ := args["username"].(string)
	userID, _ := args["user_id"].(int)

	if remote, ok := args["_remote"].(bool); ok && remote {
		sourceNode, _ := args["_source_node"].(string)
		log.Printf("🌐 Remote user updated - Node: %s, User: %s ID: %d", sourceNode, username, userID)
	} else {
		log.Printf("✏️ Local user updated - User: %s ID: %d", username, userID)
	}

	return GoEventBus.Result{
		Message: fmt.Sprintf("User %s (ID: %d) updated successfully", username, userID),
	}, nil
}

func handleUserDeleted(ctx context.Context, args map[string]interface{}) (GoEventBus.Result, error) {
	username, _ := args["username"].(string)
	userID, _ := args["user_id"].(int)

	if remote, ok := args["_remote"].(bool); ok && remote {
		sourceNode, _ := args["_source_node"].(string)
		log.Printf("🌐 Remote user deleted - Node: %s, User: %s ID: %d", sourceNode, username, userID)
	} else {
		log.Printf("🗑️ Local user deleted - User: %s ID: %d", username, userID)
	}

	return GoEventBus.Result{
		Message: fmt.Sprintf("User %s (ID: %d) deleted successfully", username, userID),
	}, nil
}

func handleSystemStats(ctx context.Context, args map[string]interface{}) (GoEventBus.Result, error) {
	if remote, ok := args["_remote"].(bool); ok && remote {
		sourceNode, _ := args["_source_node"].(string)
		peerCount, _ := args["peer_count"].(int)
		eventsSent, _ := args["events_sent"].(uint64)
		eventsReceived, _ := args["events_received"].(uint64)

		log.Printf("📊 Remote stats - Node: %s, Peers: %d, Sent: %d, Received: %d",
			sourceNode, peerCount, eventsSent, eventsReceived)
	}

	return GoEventBus.Result{Message: "System stats processed"}, nil
}

// -- Existing event handlers omitted for brevity (see original code) --

// Start begins the application's main operations
func (app *Application) Start() error {
	port := flag.String("port", "8080", "HTTP server port")
	flag.Parse()
	log.Printf("🚀 Starting application with node name: %s", app.config.NodeName)

	// Add hooks for monitoring
	app.eventBus.EventStore.OnBefore(app.beforeEventHook)
	app.eventBus.EventStore.OnAfter(app.afterEventHook)
	app.eventBus.EventStore.OnError(app.errorEventHook)

	// Start event publishing routine
	app.wg.Add(1)

	// Start network monitoring
	app.wg.Add(1)
	// Start HTTP server for subscribing events
	app.wg.Add(1)
	go func() {
		defer app.wg.Done()
		log.Printf("🌐 HTTP server listening on :%s", *port)
		if err := fasthttp.ListenAndServe(":"+*port, app.subscribeHandler); err != nil {
			log.Printf("❌ HTTP server error: %v", err)
		}
	}()

	log.Println("✅ Application started successfully")
	return nil
}

// subscribeHandler handles HTTP subscriptions to events
func (app *Application) subscribeHandler(ctx *fasthttp.RequestCtx) {
	if string(ctx.Method()) != fasthttp.MethodPost {
		ctx.Error("Method Not Allowed", fasthttp.StatusMethodNotAllowed)
		return
	}
	// Parse subscription request
	var req struct {
		ID         string                 `json:"id"`
		Projection string                 `json:"projection"`
		Args       map[string]interface{} `json:"args"`
	}
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		ctx.Error("Invalid JSON: "+err.Error(), fasthttp.StatusBadRequest)
		return
	}
	evt := GoEventBus.Event{
		ID:         req.ID,
		Projection: req.Projection,
		Args:       req.Args,
	}
	if err := app.eventBus.Subscribe(app.ctx, evt); err != nil {
		ctx.Error("Failed to subscribe event: "+err.Error(), fasthttp.StatusInternalServerError)
		return
	}
	app.subscribedEvents = append(app.subscribedEvents, evt)
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetBodyString(fmt.Sprintf("Event %s subscribed", req.Projection))
}

// Middleware and hooks
func (app *Application) loggingMiddleware(next GoEventBus.HandlerFunc) GoEventBus.HandlerFunc {
	return func(ctx context.Context, args map[string]any) (GoEventBus.Result, error) {
		start := time.Now()
		result, err := next(ctx, args)
		duration := time.Since(start)

		if duration > 100*time.Millisecond {
			log.Printf("⚠️ Slow event handler: %v took %v", args["projection"], duration)
		}

		return result, err
	}
}

func (app *Application) beforeEventHook(ctx context.Context, event GoEventBus.Event) {
	// Optional: Add pre-processing logic here
}

func (app *Application) afterEventHook(ctx context.Context, event GoEventBus.Event, result GoEventBus.Result, err error) {
	if err != nil {
		log.Printf("❌ Event handler error for %v: %v", event.Projection, err)
	}
}

func (app *Application) errorEventHook(ctx context.Context, event GoEventBus.Event, err error) {
	log.Printf("🚨 Event error hook triggered for %v: %v", event.Projection, err)
}

// WaitForShutdown waits for interrupt signals
func (app *Application) WaitForShutdown() {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		log.Printf("🛑 Received signal: %v, initiating shutdown...", sig)
	case <-app.ctx.Done():
		log.Println("🛑 Context cancelled, initiating shutdown...")
	}
}

// Shutdown gracefully stops the application
func (app *Application) Shutdown() error {
	log.Println("🔄 Starting graceful shutdown...")

	// Cancel context to stop all goroutines
	app.cancel()

	// Wait for all goroutines to complete
	done := make(chan struct{})
	go func() {
		app.wg.Wait()
		close(done)
	}()

	// Wait with timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	select {
	case <-done:
		log.Println("✅ All goroutines stopped")
	case <-shutdownCtx.Done():
		log.Println("⚠️ Shutdown timeout reached")
	}

	// Close the distributed event bus
	if err := app.eventBus.Close(shutdownCtx); err != nil {
		return fmt.Errorf("failed to close event bus: %w", err)
	}

	log.Println("✅ Event bus closed")
	return nil
}
```

## Configuration Options

| Option         | Type            | Default                | Description                                |
| -------------- | --------------- | ---------------------- | ------------------------------------------ |
| ListenAddrs    | `[]string`      | `[/ip4/0.0.0.0/tcp/0]` | Multiaddrs to listen on                    |
| SyncInterval   | `time.Duration` | `5s`                   | Interval between syncs                     |
| MaxPeers       | `int`           | `50`                   | Maximum number of peers to maintain        |
| EventFilters   | `[]EventFilter` | `[]`                   | Filters to apply before broadcasting       |
| RoutingRules   | `[]RoutingRule` | `[BroadcastToAll]`     | Rules to select target peers               |
| EnableRelay    | `bool`          | `false`                | Enable libp2p relay                        |
| BootstrapPeers | `[]string`      | `[]`                   | Addresses of peers to bootstrap connect to |

## License

MIT © Your Name
