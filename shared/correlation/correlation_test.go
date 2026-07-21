package correlation_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/example/go-ddd-template/shared/correlation"
)

func TestWithIDAndFromContext(t *testing.T) {
	ctx := correlation.WithID(context.Background(), "abc123")

	got, ok := correlation.FromContext(ctx)
	assert.True(t, ok, "相関 ID が存在するはず")
	assert.Equal(t, "abc123", got, "FromContext の値")
	assert.Equal(t, "abc123", correlation.FromContextOrEmpty(ctx), "FromContextOrEmpty の値")
}

func TestFromContext_Absent(t *testing.T) {
	ctx := context.Background()
	_, ok := correlation.FromContext(ctx)
	assert.False(t, ok, "相関 ID が無い context で ok=true になった")
	assert.Empty(t, correlation.FromContextOrEmpty(ctx), "FromContextOrEmpty は空文字列であるべき")
}
