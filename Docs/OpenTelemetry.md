# Xylium OpenTelemetry (OTel) Integration

Xylium provides middleware for integrating with OpenTelemetry, enabling distributed tracing for your web applications. This allows you to gain insights into request flows, identify performance bottlenecks, and understand interactions between services.

## Table of Contents

*   [1. Overview](#1-overview)
*   [2. Prerequisites: Initializing the OTel SDK](#2-prerequisites-initializing-the-otel-sdk)
*   [3. Using the OpenTelemetry Middleware](#3-using-the-opentelemetry-middleware)
    *   [3.1. Basic Usage with Defaults](#31-basic-usage-with-defaults)
    *   [3.2. Custom Configuration (`OpenTelemetryConfig`)](#32-custom-configuration-opentelemetryconfig)
        *   [Configuration Options](#configuration-options)
        *   [Example with Custom Configuration](#example-with-custom-configuration)
*   [4. Span Naming](#4-span-naming)
*   [5. Context Propagation](#5-context-propagation)
*   [6. Integration with Xylium Logger](#6-integration-with-xylium-logger)
*   [7. Recorded Span Attributes](#7-recorded-span-attributes)
*   [8. Span Status](#8-span-status)
*   [9. Filtering Traces](#9-filtering-traces)
*   [10. Full Example](#10-full-example)

---

## 1. Overview

The Xylium OpenTelemetry middleware (`xylium.Otel()`) automatically:
*   **Extracts** trace context from incoming HTTP request headers (e.g., `traceparent`, `tracestate`).
*   **Starts** a new server span for each request, linking it to an existing trace if context was propagated.
*   **Records** standard HTTP semantic attributes on the span (method, path, status code, etc.).
*   **Injects** `trace_id` and `span_id` into the `xylium.Context`, making them available to your handlers and automatically included by `c.Logger()`.
*   **Propagates** the Go `context.Context` enriched with the active span to subsequent handlers and middleware.
*   **Handles** errors and sets the span status accordingly.

## 2. Prerequisites: Initializing the OTel SDK

**Crucially, your application is responsible for initializing the OpenTelemetry SDK.** This typically involves:
1.  Setting up a **TracerProvider** (e.g., `sdktrace.NewTracerProvider`).
2.  Configuring **Exporters** (e.g., OTLP, Jaeger, Zipkin) to send trace data to a backend.
3.  Registering the `TracerProvider` and a `TextMapPropagator` (e.g., `propagation.TraceContext` and `propagation.Baggage`) globally using `otel.SetTracerProvider()` and `otel.SetTextMapPropagator()`.

This initialization should happen early in your application's `main()` function, before you set up your Xylium router and middleware.

**Simplified OTel SDK Setup Example (e.g., for console exporter):**
```go
// In your main.go
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace" // Console exporter
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.25.0" // Or your chosen version

	"github.com/arwahdevops/xylium-core/src/xylium"
)

// initTracer sets up the OpenTelemetry SDK for tracing.
func initTracer() (*sdktrace.TracerProvider, error) {
	// Create a new exporter. Replace with OTLP or other exporters for production.
	exporter, err := stdouttrace.New(stdouttrace.WithPrettyPrint())
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout trace exporter: %w", err)
	}

	// Create a new resource with service name.
	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceNameKey.String("my-xylium-app"),
			// semconv.ServiceVersionKey.String("v0.1.0"), // Optional
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	// Create a new TracerProvider with the exporter and resource.
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		// Add other options like samplers if needed
		// sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)

	// Set the global TracerProvider and TextMapPropagator.
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, // W3C Trace Context
		propagation.Baggage{},      // W3C Baggage
	))

	log.Println("OpenTelemetry TracerProvider initialized and set globally.")
	return tp, nil
}

func main() {
	// Initialize OpenTelemetry TracerProvider
	tp, err := initTracer()
	if err != nil {
		log.Fatalf("Failed to initialize OpenTelemetry tracer: %v", err)
	}
	defer func() {
		// Shutdown the TracerProvider to flush buffered traces.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := tp.Shutdown(ctx); err != nil {
			log.Printf("Error shutting down OTel TracerProvider: %v", err)
		}
	}()

	app := xylium.New()

	// Use the Xylium OTel middleware
	// It will use the globally configured TracerProvider and Propagator
	app.Use(xylium.Otel()) 
	
	// Add your routes and other middleware...
	app.GET("/", func(c *xylium.Context) error {
		// Example: Create a child span within the handler
		_, childSpan := otel.Tracer("my-handler-tracer").Start(c.GoContext(), "my-custom-operation")
		defer childSpan.End()
		// ... do some work ...
		childSpan.SetAttributes(attribute.String("custom.key", "custom.value"))
		
		return c.String(http.StatusOK, "Hello with OTel!")
	})

	// Start server gracefully
	// ... (server start logic as in Xylium examples) ...
}
```

## 3. Using the OpenTelemetry Middleware

### 3.1. Basic Usage with Defaults

If you have initialized the OTel SDK globally (as shown above), using the middleware is straightforward:

```go
app := xylium.New()

// Apply the OTel middleware globally
// This will use the global TracerProvider and Propagator.
app.Use(xylium.Otel())

// ... add routes and other middleware ...

app.GET("/ping", func(c *xylium.Context) error {
	// c.Logger() will now automatically include trace_id and span_id
	c.Logger().Info("Ping request handled.")
	// c.GoContext() will be the context associated with the current span
	return c.String(http.StatusOK, "pong")
})
```

### 3.2. Custom Configuration (`OpenTelemetryConfig`)

You can customize the middleware's behavior by providing an `OpenTelemetryConfig` struct.

#### Configuration Options

```go
type OpenTelemetryConfig struct {
	// TracerProvider is the OpenTelemetry TracerProvider to use.
	// If nil, the global TracerProvider (otel.GetTracerProvider()) will be used.
	TracerProvider oteltrace.TracerProvider

	// Propagator is the OpenTelemetry TextMapPropagator to use for context propagation.
	// If nil, the global Propagator (otel.GetTextMapPropagator()) will be used.
	Propagator propagation.TextMapPropagator

	// TracerName is the name of the tracer that will be created by the middleware.
	// Defaults to "xylium-application".
	TracerName string

	// SpanNameFormatter is an optional function to customize the name of the server span.
	// By default, the span name is the request path (e.g., "/users/123").
	// For better cardinality, it's recommended to use the route pattern (e.g., "/users/:id").
	SpanNameFormatter func(c *xylium.Context) string

	// AdditionalAttributes allows adding custom key-value pairs to every span
	// created by this middleware.
	AdditionalAttributes []attribute.KeyValue

	// Filter is an optional function to conditionally skip tracing for some requests.
	// If Filter returns true, tracing is skipped for that request.
	Filter func(c *Context) bool
}
```

#### Example with Custom Configuration

```go
otelConfig := xylium.OpenTelemetryConfig{
	TracerName: "my-custom-service-tracer",
	SpanNameFormatter: func(c *xylium.Context) string {
		// Ideally, use a matched route pattern if available from Xylium
		// Example: routePattern := c.MatchedRoutePattern() 
		// if routePattern != "" { return c.Method() + " " + routePattern }
		return c.Method() + " " + c.Path() // Fallback: "GET /some/path"
	},
	AdditionalAttributes: []attribute.KeyValue{
		attribute.String("environment", "staging"),
	},
	Filter: func(c *xylium.Context) bool {
		// Example: Don't trace /health or /metrics endpoints
		if c.Path() == "/health" || c.Path() == "/metrics" {
			return true // Skip tracing
		}
		return false // Trace this request
	},
	// You can also provide a specific TracerProvider or Propagator if not using global ones.
	// TracerProvider: myCustomTracerProvider,
	// Propagator: myCustomPropagator,
}

app.Use(xylium.Otel(otelConfig))
```

## 4. Span Naming

By default, the server span created by the middleware is named after the request path (e.g., `/api/items/42`).
You can customize this using the `SpanNameFormatter` option in `OpenTelemetryConfig`.

For better observability and lower cardinality in your tracing backend, it's highly recommended to name spans using the **route pattern** (e.g., `/api/items/:id`) rather than the concrete path. Xylium currently uses `c.Path()` by default. If a future Xylium version exposes the matched route pattern on the `Context` (e.g., via `c.MatchedRoutePattern()`), you should use that in your `SpanNameFormatter`.

```go
// Ideal SpanNameFormatter (if MatchedRoutePattern becomes available)
SpanNameFormatter: func(c *xylium.Context) string {
    // pattern := c.MatchedRoutePattern() // Hypothetical
    // if pattern != "" {
    //     return c.Method() + " " + pattern
    // }
    return c.Method() + " " + c.Path() // Current best effort
},
```

## 5. Context Propagation

*   **Extraction**: The middleware uses the configured `Propagator` (defaulting to the global one) to extract trace context (e.g., `traceparent`, `tracestate` headers) from incoming request headers. This links the new server span to any parent span from an upstream service.
*   **Internal Propagation**: The Go `context.Context` associated with the newly created server span is propagated to subsequent Xylium handlers and middleware via `c.WithGoContext()`. You can access this traced context using `c.GoContext()` in your handlers to create child spans or pass it to downstream calls.

```go
func MyHandler(c *xylium.Context) error {
    // c.GoContext() here is the context associated with the server span.
    tracedCtx := c.GoContext()

    // Create a child span for a specific operation
    _, childSpan := otel.Tracer("my-operation-tracer").Start(tracedCtx, "database-query")
    // ... perform database query using tracedCtx ...
    childSpan.End()

    return c.String(http.StatusOK, "Done")
}
```

## 6. Integration with Xylium Logger

The OTel middleware automatically injects the `trace_id` and `span_id` of the active server span into the `xylium.Context` store using the keys:
*   `xylium.ContextKeyOtelTraceID` (value: `"otel_trace_id"`)
*   `xylium.ContextKeyOtelSpanID` (value: `"otel_span_id"`)

Xylium's contextual logger (`c.Logger()`) is designed to automatically pick up these keys (along with `xylium.ContextKeyRequestID`) and include them in structured log entries. This greatly helps in correlating logs with traces.

**Example Log Output (JSON formatter):**
```json
{
    "timestamp": "2023-10-27T10:00:00.123Z",
    "level": "INFO",
    "message": "User profile requested.",
    "fields": {
        "handler": "GetUserProfile",
        "user_id": "usr_123",
        "xylium_request_id": "uuid-abc-123",
        "otel_trace_id": "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4",
        "otel_span_id": "0123456789abcdef"
    },
    "caller": "main.go:42" 
}
```

## 7. Recorded Span Attributes

The middleware records several standard semantic attributes on the server span, based on OpenTelemetry conventions (`semconv/v1.25.0` or similar):

*   `http.request.method` (e.g., "GET")
*   `url.scheme` (e.g., "http", "https")
*   `server.address` (from the `Host` header, e.g., "example.com")
*   `url.path` (e.g., "/users/123")
*   `url.query` (if query parameters exist, e.g., "name=xylium&page=1")
*   `http.route` (The route pattern. Defaults to `url.path` if a more specific pattern isn't available from Xylium.)
*   `http.response.status_code` (e.g., 200, 404, 500)
*   Custom attribute `xylium.request_id` if Xylium's `RequestID` middleware is used.
*   Any attributes added via `OpenTelemetryConfig.AdditionalAttributes`.

## 8. Span Status

The status of the server span is set according to OpenTelemetry guidelines:
*   If a Go error is returned by any handler in the chain:
    *   The error is recorded on the span using `span.RecordError(err, oteltrace.WithStackTrace(true))`.
    *   The span status is set to `codes.Error`.
*   If no Go error is returned by the handlers:
    *   If the HTTP response status code is `500` or greater (server-side errors), the span status is set to `codes.Error`.
    *   For HTTP status codes less than `500` (e.g., 2xx, 3xx, 4xx client errors), the span status remains `Unset` (which is generally interpreted as `OK` by tracing backends if no error was recorded).

## 9. Filtering Traces

You can use the `Filter` option in `OpenTelemetryConfig` to prevent certain requests from being traced. This is useful for excluding high-volume, low-value endpoints like health checks.

```go
otelConfig := xylium.OpenTelemetryConfig{
    Filter: func(c *xylium.Context) bool {
        if c.Path() == "/_healthz" {
            return true // Skip tracing for this path
        }
        if strings.HasPrefix(c.Header("User-Agent"), "HealthChecker") {
            return true // Skip tracing for health checker user agents
        }
        return false // Trace all other requests
    },
}
app.Use(xylium.Otel(otelConfig))
```

## 10. Full Example

For a more complete, runnable example demonstrating OTel SDK initialization and middleware usage, please refer to the example provided in [Section 2](#2-prerequisites-initializing-the-otel-sdk) or check the `examples/` directory in the Xylium repository.

By integrating OpenTelemetry, you equip your Xylium applications with powerful distributed tracing capabilities, essential for modern microservices and complex application architectures.
