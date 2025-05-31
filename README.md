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

### `examples/server/main.go`

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/Raezil/DistributedEventStore"
	"github.com/Raezil/GoEventBus"
)

func main() {
	// Initialize configuration
	config := &DistributedEventStore.AppConfig{
		PublishInterval:  5 * time.Second,
		MaxUsers:         100,
		EnableStatistics: true,
		NodeName:         fmt.Sprintf("node-%d", os.Getpid()),
	}

	// Create application instance
	dispatcher := createEventDispatcher()
	app, err := DistributedEventStore.NewApplication(dispatcher, config)
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

MIT © Raezil
