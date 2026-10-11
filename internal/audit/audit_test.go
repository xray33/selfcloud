package audit_test

import (
	"context"
	"log/slog"
	"testing"

	"selfcloud/internal/audit"
)

func TestAuditWorkflow(t *testing.T) {
	ctx := context.Background()

	// 1. Unattached context returns nil
	if ev := audit.FromContext(ctx); ev != nil {
		t.Fatalf("expected nil from unattached context, got %v", ev)
	}

	// 2. Attach creates an empty event in context
	ctx, ev := audit.Attach(ctx)
	if ev == nil {
		t.Fatal("expected non-nil event from Attach")
	}
	if audit.FromContext(ctx) != ev {
		t.Fatal("FromContext should return identical event pointer")
	}

	// 3. Record success
	audit.Record(ctx, "file_create", "/test.txt", "File created successfully", slog.String("size", "123"))
	if ev.Action != "file_create" {
		t.Errorf("expected action file_create, got %s", ev.Action)
	}
	if ev.Target != "/test.txt" {
		t.Errorf("expected target /test.txt, got %s", ev.Target)
	}
	if ev.Summary != "File created successfully" {
		t.Errorf("expected summary, got %s", ev.Summary)
	}
	if ev.Failed {
		t.Errorf("expected Failed == false")
	}
	if len(ev.Attrs) != 1 || ev.Attrs[0].Key != "size" {
		t.Errorf("unexpected attrs: %v", ev.Attrs)
	}

	// 4. Record failure
	audit.RecordFailure(ctx, "file_delete", "/protected.txt", "Permission denied")
	if ev.Action != "file_delete" {
		t.Errorf("expected action file_delete, got %s", ev.Action)
	}
	if !ev.Failed {
		t.Errorf("expected Failed == true")
	}

	// 5. SetUsername
	audit.SetUsername(ctx, "alice")
	if ev.Username != "alice" {
		t.Errorf("expected username alice, got %s", ev.Username)
	}
}

func TestAuditNoopWhenUnattached(t *testing.T) {
	ctx := context.Background()

	// Must not panic when no event attached
	audit.Record(ctx, "action", "target", "summary")
	audit.RecordFailure(ctx, "action", "target", "summary")
	audit.SetUsername(ctx, "username")
}
