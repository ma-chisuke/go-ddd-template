package correlation_test

import (
	"context"
	"testing"

	"github.com/example/go-ddd-template/shared/correlation"
)

func TestWithIDAndFromContext(t *testing.T) {
	ctx := correlation.WithID(context.Background(), "abc123")

	got, ok := correlation.FromContext(ctx)
	if !ok || got != "abc123" {
		t.Fatalf("FromContext = (%q, %v), want (abc123, true)", got, ok)
	}
	if s := correlation.FromContextOrEmpty(ctx); s != "abc123" {
		t.Fatalf("FromContextOrEmpty = %q, want abc123", s)
	}
}

func TestFromContext_Absent(t *testing.T) {
	ctx := context.Background()
	if _, ok := correlation.FromContext(ctx); ok {
		t.Fatal("相関 ID が無い context で ok=true になった")
	}
	if s := correlation.FromContextOrEmpty(ctx); s != "" {
		t.Fatalf("FromContextOrEmpty = %q, want 空文字列", s)
	}
}
