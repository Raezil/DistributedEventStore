package DistributedEventStore

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

	"github.com/Raezil/GoEventBus"
	"github.com/valyala/fasthttp"
)

// Application holds the main application state
type Application struct {
	subscribedEvents []GoEventBus.Event
	eventBus         *DistributedEventBus
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

// NewApplication creates a new application instance
func NewApplication(dispatcher *GoEventBus.Dispatcher, config *AppConfig) (*Application, error) {
	ctx, cancel := context.WithCancel(context.Background())

	// Create distributed event bus configuration
	debConfig := &Config{
		ListenAddrs:  []string{"/ip4/0.0.0.0/tcp/0"},
		SyncInterval: 3 * time.Second,
		MaxPeers:     20,
		EventFilters: []EventFilter{
			FilterLocalOnly,
		},
		RoutingRules: []RoutingRule{
			BroadcastToAll,
		},
		EnableRelay: true,
	}

	// Create distributed evedeb.EventStore.OnAftert bus
	deb, err := NewDistributedEventBus(
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
