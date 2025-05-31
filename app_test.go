package DistributedEventStore

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Raezil/GoEventBus"
)

func TestLoggingMiddleware_SlowHandler(t *testing.T) {
	app := &Application{}
	// Capture log output
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	slowHandler := func(ctx context.Context, args map[string]any) (GoEventBus.Result, error) {
		time.Sleep(150 * time.Millisecond)
		return GoEventBus.Result{}, nil
	}
	wrapped := app.loggingMiddleware(slowHandler)
	_, _ = wrapped(context.Background(), map[string]any{"projection": "test"})
	output := buf.String()
	if !strings.Contains(output, "Slow event handler") {
		t.Errorf("Expected slow handler warning, got %s", output)
	}
}

func TestLoggingMiddleware_FastHandler(t *testing.T) {
	app := &Application{}
	// Capture log output
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	fastHandler := func(ctx context.Context, args map[string]any) (GoEventBus.Result, error) {
		// Quick return
		return GoEventBus.Result{}, nil
	}
	wrapped := app.loggingMiddleware(fastHandler)
	_, _ = wrapped(context.Background(), map[string]any{"projection": "test"})
	output := buf.String()
	if strings.Contains(output, "Slow event handler") {
		t.Errorf("Did not expect slow handler warning, but got %s", output)
	}
}

func TestBeforeEventHook_NoPanic(t *testing.T) {
	app := &Application{}
	// Should not panic
	app.beforeEventHook(context.Background(), GoEventBus.Event{})
}

func TestAfterEventHook_ErrorLogging(t *testing.T) {
	app := &Application{}
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	event := GoEventBus.Event{Projection: "testProjection"}
	result := GoEventBus.Result{}
	err := fmt.Errorf("handler error")
	app.afterEventHook(context.Background(), event, result, err)

	output := buf.String()
	if !strings.Contains(output, "Event handler error") {
		t.Errorf("Expected error log, got %s", output)
	}
}

func TestErrorEventHook_Logging(t *testing.T) {
	app := &Application{}
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	event := GoEventBus.Event{Projection: "testProjection"}
	err := fmt.Errorf("some error")
	app.errorEventHook(context.Background(), event, err)

	output := buf.String()
	if !strings.Contains(output, "Event error hook") {
		t.Errorf("Expected error hook log, got %s", output)
	}
}
