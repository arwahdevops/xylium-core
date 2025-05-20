// src/xylium/middleware_opentelemetry.go
package xylium

import (
	"fmt"      // For string formatting, e.g., in error messages or span status.
	"net/http" // For HTTP status code constants like http.StatusInternalServerError.

	"github.com/valyala/fasthttp" // For fasthttp.RequestHeader type.

	"go.opentelemetry.io/otel"                         // Core OpenTelemetry package.
	"go.opentelemetry.io/otel/attribute"               // For creating span attributes.
	"go.opentelemetry.io/otel/codes"                   // For OTel span status codes (Ok, Error).
	"go.opentelemetry.io/otel/propagation"             // For context propagation (extracting/injecting trace context).
	semconv "go.opentelemetry.io/otel/semconv/v1.25.0" // Semantic conventions for OpenTelemetry attributes. Update version as needed.
	oteltrace "go.opentelemetry.io/otel/trace"         // OpenTelemetry tracing specific types.
)

// Note: ContextKeyOtelTraceID and ContextKeyOtelSpanID are defined in types.go
// and are implicitly available within this package.

// OpenTelemetryConfig holds configuration options for the OpenTelemetry (OTel) middleware.
// This middleware integrates Xylium with OpenTelemetry for distributed tracing.
type OpenTelemetryConfig struct {
	// TracerProvider is the OpenTelemetry TracerProvider to use for creating tracers.
	// If nil, the global TracerProvider (obtained via `otel.GetTracerProvider()`) will be used.
	// It's recommended to configure a global TracerProvider in your application's main setup.
	TracerProvider oteltrace.TracerProvider

	// Propagator is the OpenTelemetry TextMapPropagator to use for extracting trace context
	// from incoming request headers and injecting it into outgoing requests (if applicable).
	// If nil, the global Propagator (obtained via `otel.GetTextMapPropagator()`) will be used.
	// Common choices include `propagation.TraceContext{}` (W3C Trace Context) and `propagation.Baggage{}`.
	Propagator propagation.TextMapPropagator

	// TracerName is the name of the tracer that will be created by this middleware.
	// This name helps identify the source of spans in your tracing backend.
	// Defaults to "xylium-application" if not specified.
	TracerName string

	// SpanNameFormatter is an optional function to customize the name of the server span
	// created for each incoming request.
	// By default, the span name is derived from the request path (e.g., "/users/123").
	// For better cardinality and aggregation in tracing backends, it's highly recommended
	// to use a formatter that generates span names based on the matched route pattern
	// (e.g., "GET /users/:id") if Xylium provides access to this pattern in the Context.
	// If not provided, a default formatter using `c.Path()` is used.
	SpanNameFormatter func(c *Context) string

	// AdditionalAttributes allows adding a list of custom key-value attributes
	// to every server span created by this middleware. This can be used to enrich
	// spans with common application-specific metadata (e.g., environment, region).
	AdditionalAttributes []attribute.KeyValue

	// Filter is an optional function that can be used to conditionally skip tracing
	// for certain requests. If the function returns true for a given `xylium.Context`,
	// tracing will be bypassed for that request.
	// Useful for excluding health checks, metrics endpoints, or other high-volume,
	// low-value traces.
	Filter func(c *Context) bool
}

// defaultOtelTracerName is the default name used for the tracer created by this middleware
// if no `TracerName` is specified in `OpenTelemetryConfig`.
const defaultOtelTracerName = "xylium-application"

// DefaultOpenTelemetryConfig returns an `OpenTelemetryConfig` with sensible default values.
// It uses global OTel providers and a basic span name formatter.
func DefaultOpenTelemetryConfig() OpenTelemetryConfig {
	return OpenTelemetryConfig{
		TracerProvider: otel.GetTracerProvider(),    // Use global TracerProvider.
		Propagator:     otel.GetTextMapPropagator(), // Use global Propagator.
		TracerName:     defaultOtelTracerName,
		SpanNameFormatter: func(c *Context) string { // Default span name formatter.
			path := c.Path()
			if path == "" { // Should ideally not happen for a valid request.
				// Provide a fallback span name for empty paths to avoid issues.
				// Using method helps differentiate if path is truly empty.
				return "HTTP " + c.Method()
			}
			// For better cardinality, use HTTP method + route pattern.
			// Example: "GET /users/:id"
			// If c.MatchedRoutePattern() becomes available in Xylium context:
			// if pattern := c.MatchedRoutePattern(); pattern != "" {
			// 	 return c.Method() + " " + pattern
			// }
			// Fallback to method + path if no pattern is available.
			return c.Method() + " " + path
		},
		AdditionalAttributes: nil, // No additional attributes by default.
		Filter:               nil, // No filter by default (trace all requests).
	}
}

// Otel returns a Xylium middleware for OpenTelemetry integration.
// It can be called with zero or one `OpenTelemetryConfig` argument.
// If no config is provided, `DefaultOpenTelemetryConfig()` is used.
//
// This middleware performs the following:
//  1. Extracts trace context from incoming request headers using the configured Propagator.
//  2. Starts a new server span for the request, linking it to an existing trace if context was propagated.
//  3. Sets standard OpenTelemetry semantic attributes for HTTP servers on the span.
//  4. Injects the `trace_id` and `span_id` of the active span into the `xylium.Context` store,
//     making them available via `ContextKeyOtelTraceID` and `ContextKeyOtelSpanID` for logging (`c.Logger()`)
//     and other purposes.
//  5. Propagates the Go `context.Context` (enriched with the active span) to subsequent handlers
//     and middleware via `c.WithGoContext()`.
//  6. Records errors from the handler chain on the span and sets the span status accordingly.
//  7. Sets the HTTP response status code as a span attribute.
func Otel(config ...OpenTelemetryConfig) Middleware {
	// Start with default configuration.
	cfg := DefaultOpenTelemetryConfig()

	// If user provided a custom configuration, merge it with defaults.
	if len(config) > 0 {
		userCfg := config[0]
		if userCfg.TracerProvider != nil {
			cfg.TracerProvider = userCfg.TracerProvider
		}
		if userCfg.Propagator != nil {
			cfg.Propagator = userCfg.Propagator
		}
		if userCfg.TracerName != "" {
			cfg.TracerName = userCfg.TracerName
		}
		if userCfg.SpanNameFormatter != nil {
			cfg.SpanNameFormatter = userCfg.SpanNameFormatter
		}
		// Append additional attributes from user config to any defaults (if defaults had them).
		// Current default has nil, so simple assignment or append works.
		if len(userCfg.AdditionalAttributes) > 0 {
			cfg.AdditionalAttributes = append(cfg.AdditionalAttributes[:0:0], userCfg.AdditionalAttributes...)
		}
		if userCfg.Filter != nil {
			cfg.Filter = userCfg.Filter
		}
	}

	// Get a tracer instance from the configured (or global) TracerProvider.
	tracer := cfg.TracerProvider.Tracer(cfg.TracerName)

	// Return the actual middleware handler function.
	return func(next HandlerFunc) HandlerFunc {
		return func(c *Context) error {
			// If a filter is configured, execute it. If it returns true, skip tracing.
			if cfg.Filter != nil && cfg.Filter(c) {
				c.Logger().Debugf("OTel Middleware: Tracing skipped for request %s %s due to filter.", c.Method(), c.Path())
				return next(c) // Proceed to next handler without tracing.
			}

			// Get the Go context from the current Xylium context. This might already
			// have trace information if Xylium is part of a larger traced system.
			requestGoCtx := c.GoContext()

			// Create a carrier for extracting trace context from fasthttp headers.
			carrier := newFastHTTPHeaderCarrier(&c.Ctx.Request.Header)
			// Extract trace context (e.g., traceparent, tracestate) from incoming headers.
			// This links the new span to an existing trace if one is being propagated.
			propagatedCtx := cfg.Propagator.Extract(requestGoCtx, carrier)

			// Determine the span name using the configured formatter.
			spanName := cfg.SpanNameFormatter(c)

			// Determine the HTTP route. Ideally, this would be the matched route pattern
			// (e.g., "/users/:id") for better cardinality. Using c.Path() as a fallback.
			// If Xylium context could provide c.MatchedRoutePattern(), that would be preferred here.
			httpRoute := c.Path() // Fallback; replace if c.MatchedRoutePattern() becomes available.

			// Prepare common OpenTelemetry semantic attributes for an HTTP server span.
			attributes := []attribute.KeyValue{
				semconv.HTTPRequestMethodKey.String(c.Method()), // HTTP method (e.g., "GET", "POST")
				semconv.URLSchemeKey.String(c.Scheme()),         // URL scheme (e.g., "http", "https")
				semconv.ServerAddressKey.String(c.Host()),       // Logical server address (from Host header)
				semconv.URLPathKey.String(c.Path()),             // Full request path
				semconv.HTTPRouteKey.String(httpRoute),          // The route that matched the request
				// semconv.ClientAddressKey.String(c.RealIP()), // If c.RealIP() is reliable and desired
			}
			// Add URL query if present.
			if queryBytes := c.Ctx.URI().QueryString(); len(queryBytes) > 0 {
				attributes = append(attributes, semconv.URLQueryKey.String(string(queryBytes)))
			}
			// Add Xylium Request ID as a custom attribute if available.
			if requestIDVal, exists := c.Get(ContextKeyRequestID); exists {
				if requestID, ok := requestIDVal.(string); ok && requestID != "" {
					attributes = append(attributes, attribute.String("xylium.request_id", requestID))
				}
			}
			// Add any additional custom attributes configured by the user.
			if len(cfg.AdditionalAttributes) > 0 {
				attributes = append(attributes, cfg.AdditionalAttributes...)
			}

			// Define span start options.
			spanStartOptions := []oteltrace.SpanStartOption{
				oteltrace.WithAttributes(attributes...),          // Set initial attributes.
				oteltrace.WithSpanKind(oteltrace.SpanKindServer), // This is a server-side span.
			}

			// Start the new server span. `propagatedCtx` is used as the parent context.
			tracedGoCtx, span := tracer.Start(propagatedCtx, spanName, spanStartOptions...)
			defer span.End() // Ensure the span is ended when the handler function returns.

			// Make trace_id and span_id available in Xylium's context store for logging.
			spanContext := span.SpanContext()
			if spanContext.HasTraceID() {
				c.Set(ContextKeyOtelTraceID, spanContext.TraceID().String())
			}
			if spanContext.HasSpanID() {
				c.Set(ContextKeyOtelSpanID, spanContext.SpanID().String())
			}

			// Create a new Xylium Context with the OTel-enriched Go context.
			// This ensures that `c.GoContext()` in subsequent handlers returns the traced context.
			tracedXyliumCtx := c.WithGoContext(tracedGoCtx)

			// Execute the next handler in the chain with the new traced Xylium context.
			err := next(tracedXyliumCtx)

			// After the handler chain has executed, record response information on the span.
			statusCode := c.Ctx.Response.StatusCode()
			span.SetAttributes(semconv.HTTPResponseStatusCodeKey.Int(statusCode))

			// Set span status based on the error returned by the handler chain or the HTTP status code.
			if err != nil {
				// If an error was returned by a handler, record it on the span.
				span.RecordError(err, oteltrace.WithStackTrace(true)) // Include stack trace.
				span.SetStatus(codes.Error, err.Error())              // Mark span status as Error.
			} else {
				// If no Go error from handler, check HTTP status for server-side errors (5xx).
				if statusCode >= http.StatusInternalServerError { // 500 or greater.
					span.SetStatus(codes.Error, fmt.Sprintf("HTTP server error: status code %d", statusCode))
				}
				// For HTTP status codes < 500 (e.g., 2xx success, 4xx client errors) and no Go error,
				// the span status remains `codes.Unset` (which is implicitly OK by OTel convention).
				// No explicit `span.SetStatus(codes.Ok, ...)` is strictly necessary here.
			}

			return err // Return the error (or nil) from the handler chain.
		}
	}
}

// fastHTTPHeaderCarrier adapts fasthttp.RequestHeader to the
// `propagation.TextMapCarrier` interface required by OpenTelemetry propagators.
// This allows OTel to read (extract) and write (inject) trace context
// from/to fasthttp request headers.
type fastHTTPHeaderCarrier struct {
	header *fasthttp.RequestHeader // Pointer to the fasthttp request header.
}

// newFastHTTPHeaderCarrier creates a new carrier for the given fasthttp request header.
func newFastHTTPHeaderCarrier(header *fasthttp.RequestHeader) *fastHTTPHeaderCarrier {
	return &fastHTTPHeaderCarrier{header: header}
}

// Get retrieves a single value from the header for a given key.
// Implements `propagation.TextMapCarrier`.
func (fc *fastHTTPHeaderCarrier) Get(key string) string {
	return string(fc.header.Peek(key))
}

// Set sets a value in the header for a given key.
// Implements `propagation.TextMapCarrier`. Used for injection.
func (fc *fastHTTPHeaderCarrier) Set(key string, value string) {
	fc.header.Set(key, value)
}

// Keys returns a slice of all keys present in the header.
// Implements `propagation.TextMapCarrier`.
func (fc *fastHTTPHeaderCarrier) Keys() []string {
	var keys []string
	// VisitAll iterates over all headers.
	fc.header.VisitAll(func(key, value []byte) {
		keys = append(keys, string(key))
	})
	return keys
}
