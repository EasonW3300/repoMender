package telemetry

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
)

// context and net/http carry W3C trace context across API boundaries; crypto/rand
// creates non-guessable IDs when a request enters without a valid traceparent.

type contextKey struct{}

type SpanContext struct {
	TraceID string
	Parent  string
}

type Config struct {
	ServiceName string
	Version     string
	Endpoint    string
}

func Middleware(cfg Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		span := ParseTraceparent(r.Header.Get("traceparent"))
		if span.TraceID == "" {
			span.TraceID = newTraceID()
		}
		// Keep the incoming trace ID but create a fresh span ID for this server
		// hop, which makes the response a valid child context for downstream work.
		span.Parent = newSpanID()
		ctx := context.WithValue(r.Context(), contextKey{}, span)
		w.Header().Set("traceparent", FormatTraceparent(span))
		w.Header().Set("X-Request-ID", span.TraceID)
		// The endpoint is intentionally configuration-only in this dependency-free
		// MVP: collectors can correlate requests through W3C IDs, while a later
		// exporter can attach to this stable boundary without changing handlers.
		_ = cfg
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func FromContext(ctx context.Context) SpanContext {
	span, _ := ctx.Value(contextKey{}).(SpanContext)
	return span
}

func ParseTraceparent(value string) SpanContext {
	parts := strings.Split(strings.TrimSpace(value), "-")
	if len(parts) != 4 || len(parts[0]) != 2 || parts[0] != "00" || len(parts[1]) != 32 || len(parts[2]) != 16 || len(parts[3]) != 2 {
		return SpanContext{}
	}
	if !isHex(parts[1]) || !isHex(parts[2]) || !isHex(parts[3]) || strings.Trim(parts[1], "0") == "" {
		return SpanContext{}
	}
	return SpanContext{TraceID: parts[1], Parent: parts[2]}
}

func FormatTraceparent(span SpanContext) string {
	if span.TraceID == "" {
		return ""
	}
	parent := span.Parent
	if len(parent) != 16 || !isHex(parent) {
		parent = "0000000000000001"
	}
	return "00-" + span.TraceID + "-" + parent + "-01"
}

func newTraceID() string {
	return randomHex(16, 32)
}

func newSpanID() string {
	return randomHex(8, 16)
}

func randomHex(size, fallbackLength int) string {
	bytes := make([]byte, size)
	if _, err := rand.Read(bytes); err != nil {
		return strings.Repeat("0", fallbackLength-1) + "1"
	}
	return hex.EncodeToString(bytes)
}

func isHex(value string) bool {
	for _, r := range value {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}
