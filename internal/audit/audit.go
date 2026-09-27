// Package audit provides a lightweight context-carried request audit collector.
// Handlers call audit.Record once to annotate the current request with a human-readable
// explanation. LoggingMiddleware reads this at the end of the request and emits a
// structured [AUDIT] log event — without any log calls being scattered around handlers.
package audit

import (
	"context"
	"log/slog"
)

// Event holds the audit annotation set by a handler for the current request.
type Event struct {
	Action   string      // machine-readable action name, e.g. "file_create"
	Target   string      // primary resource path being acted upon
	Summary  string      // human-readable explanation
	Attrs    []slog.Attr // optional extra structured fields
	Failed   bool        // true if the operation did not succeed
	Username string      // authenticated username; written by auth middleware via pointer mutation
}

type auditCtxKey struct{}

// Attach returns a new context with an empty mutable Event attached.
// Called once by LoggingMiddleware before the request is dispatched.
func Attach(ctx context.Context) (context.Context, *Event) {
	ev := &Event{}
	return context.WithValue(ctx, auditCtxKey{}, ev), ev
}

// FromContext returns the Event attached by Attach, or nil if none.
func FromContext(ctx context.Context) *Event {
	ev, _ := ctx.Value(auditCtxKey{}).(*Event)
	return ev
}

// Record annotates the request's audit event with a successful operation summary.
// attrs are optional extra slog.Attr fields appended to the AUDIT log line.
func Record(ctx context.Context, action, target, summary string, attrs ...slog.Attr) {
	if ev := FromContext(ctx); ev != nil {
		ev.Action = action
		ev.Target = target
		ev.Summary = summary
		ev.Attrs = attrs
		ev.Failed = false
	}
}

// RecordFailure annotates the request's audit event with a failed operation summary.
func RecordFailure(ctx context.Context, action, target, summary string, attrs ...slog.Attr) {
	if ev := FromContext(ctx); ev != nil {
		ev.Action = action
		ev.Target = target
		ev.Summary = summary
		ev.Attrs = attrs
		ev.Failed = true
	}
}

// SetUsername writes the authenticated username into the shared event pointer.
// Called by BasicAuthMiddleware so LoggingMiddleware can read it after ServeHTTP returns.
func SetUsername(ctx context.Context, username string) {
	if ev := FromContext(ctx); ev != nil {
		ev.Username = username
	}
}
