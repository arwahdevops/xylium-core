package xylium

import (
	"fmt"
	"net/http" // For http.StatusInternalServerError status code constant

	"github.com/valyala/fasthttp" // Added for fasthttp.RequestHeader

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes" // For codes.Ok, codes.Error
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.25.0"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// ContextKeyOtelTraceID is the key used to store the OpenTelemetry Trace ID in the xylium.Context store.
const ContextKeyOtelTraceID string = "otel_trace_id"

// ContextKeyOtelSpanID is the key used to store the OpenTelemetry Span ID in the xylium.Context store.
const ContextKeyOtelSpanID string = "otel_span_id"

// OpenTelemetryConfig holds configuration for the OpenTelemetry (OTel) middleware.
type OpenTelemetryConfig struct {
	TracerProvider       oteltrace.TracerProvider
	Propagator           propagation.TextMapPropagator
	TracerName           string
	SpanNameFormatter    func(c *Context) string
	AdditionalAttributes []attribute.KeyValue
	Filter               func(c *Context) bool
}

const defaultOtelTracerName = "xylium-application"

func DefaultOpenTelemetryConfig() OpenTelemetryConfig {
	return OpenTelemetryConfig{
		TracerProvider: otel.GetTracerProvider(),
		Propagator:     otel.GetTextMapPropagator(),
		TracerName:     defaultOtelTracerName,
		SpanNameFormatter: func(c *Context) string {
			path := c.Path()
			if path == "" {
				return "/"
			}
			return path
		},
		AdditionalAttributes: nil,
		Filter:               nil,
	}
}

func Otel(config ...OpenTelemetryConfig) Middleware {
	cfg := DefaultOpenTelemetryConfig()

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
		if len(userCfg.AdditionalAttributes) > 0 {
			cfg.AdditionalAttributes = append(cfg.AdditionalAttributes, userCfg.AdditionalAttributes...)
		}
		if userCfg.Filter != nil {
			cfg.Filter = userCfg.Filter
		}
	}

	tracer := cfg.TracerProvider.Tracer(cfg.TracerName)

	return func(next HandlerFunc) HandlerFunc {
		return func(c *Context) error {
			if cfg.Filter != nil && cfg.Filter(c) {
				return next(c)
			}

			requestGoCtx := c.GoContext()
			carrier := newFastHTTPHeaderCarrier(&c.Ctx.Request.Header)
			propagatedCtx := cfg.Propagator.Extract(requestGoCtx, carrier)

			spanName := cfg.SpanNameFormatter(c)
			route := c.Path() // Fallback, ideally c.MatchedRoutePattern()

			attributes := []attribute.KeyValue{
				semconv.HTTPRequestMethodKey.String(c.Method()),
				semconv.URLSchemeKey.String(c.Scheme()),
				semconv.ServerAddressKey.String(c.Host()),
				semconv.URLPathKey.String(c.Path()),
				semconv.HTTPRouteKey.String(route),
			}

			if queryPart := c.Ctx.URI().QueryString(); len(queryPart) > 0 {
				attributes = append(attributes, semconv.URLQueryKey.String(string(queryPart)))
			}

			if requestIDVal, exists := c.Get(ContextKeyRequestID); exists { // Corrected c.Get usage
				if requestID, ok := requestIDVal.(string); ok && requestID != "" {
					attributes = append(attributes, attribute.String("xylium.request_id", requestID))
				}
			}

			if len(cfg.AdditionalAttributes) > 0 {
				attributes = append(attributes, cfg.AdditionalAttributes...)
			}

			spanStartOptions := []oteltrace.SpanStartOption{
				oteltrace.WithAttributes(attributes...),
				oteltrace.WithSpanKind(oteltrace.SpanKindServer),
			}

			tracedGoCtx, span := tracer.Start(propagatedCtx, spanName, spanStartOptions...)
			defer span.End()

			spanCtx := span.SpanContext()
			if spanCtx.HasTraceID() {
				c.Set(ContextKeyOtelTraceID, spanCtx.TraceID().String())
			}
			if spanCtx.HasSpanID() {
				c.Set(ContextKeyOtelSpanID, spanCtx.SpanID().String())
			}

			tracedXyliumCtx := c.WithGoContext(tracedGoCtx)
			err := next(tracedXyliumCtx)

			statusCode := c.Ctx.Response.StatusCode()
			span.SetAttributes(semconv.HTTPResponseStatusCodeKey.Int(statusCode))

			if err != nil {
				span.RecordError(err, oteltrace.WithStackTrace(true))
				span.SetStatus(codes.Error, err.Error()) // Corrected: use codes.Error
			} else {
				if statusCode >= http.StatusInternalServerError {
					span.SetStatus(codes.Error, fmt.Sprintf("HTTP server error: status code %d", statusCode)) // Corrected: use codes.Error
				}
				// No explicit codes.Ok needed for < 500 if no error, Unset is fine.
			}
			return err
		}
	}
}

// fastHTTPHeaderCarrier adapts fasthttp.RequestHeader to propagation.TextMapCarrier.
type fastHTTPHeaderCarrier struct {
	header *fasthttp.RequestHeader // Corrected: pointer to fasthttp.RequestHeader
}

func newFastHTTPHeaderCarrier(header *fasthttp.RequestHeader) *fastHTTPHeaderCarrier { // Corrected: pointer to fasthttp.RequestHeader
	return &fastHTTPHeaderCarrier{header: header}
}

func (fc *fastHTTPHeaderCarrier) Get(key string) string {
	return string(fc.header.Peek(key))
}

func (fc *fastHTTPHeaderCarrier) Set(key string, value string) {
	fc.header.Set(key, value)
}

func (fc *fastHTTPHeaderCarrier) Keys() []string {
	var keys []string
	fc.header.VisitAll(func(key, value []byte) {
		keys = append(keys, string(key))
	})
	return keys
}
