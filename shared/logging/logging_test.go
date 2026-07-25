package logging_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/go-ddd-template/shared/correlation"
	"github.com/example/go-ddd-template/shared/logging"
)

// decodeLine は 1 行の JSON ログをマップへデコードする。
func decodeLine(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &m), "ログ行のデコード")
	return m
}

// TestNew_AddsCorrelationID は、context に相関 ID があるとき、ログ行に
// correlation_id 属性として載ることを確認する。
func TestNew_AddsCorrelationID(t *testing.T) {
	var buf bytes.Buffer
	log := logging.New(&buf, slog.LevelInfo)

	ctx := correlation.WithID(context.Background(), "corr-123")
	log.InfoContext(ctx, "hello")

	m := decodeLine(t, &buf)
	assert.Equal(t, "corr-123", m["correlation_id"], "correlation_id 属性がログ行に載る")
	assert.Equal(t, "hello", m["msg"], "メッセージ本文")
}

// TestNew_NoCorrelationIDWhenAbsent は、context に相関 ID が無いときは
// correlation_id 属性を付けないことを確認する。
func TestNew_NoCorrelationIDWhenAbsent(t *testing.T) {
	var buf bytes.Buffer
	log := logging.New(&buf, slog.LevelInfo)

	log.InfoContext(context.Background(), "hello")

	m := decodeLine(t, &buf)
	_, ok := m["correlation_id"]
	assert.False(t, ok, "相関 ID が無い場合は correlation_id 属性を付けない")
}

// TestNew_WrappingSurvivesWithAttrsAndWithGroup は、WithAttrs / WithGroup で
// 派生したロガーでも相関 ID 付与のラップが維持されることを確認する。
func TestNew_WrappingSurvivesWithAttrsAndWithGroup(t *testing.T) {
	var buf bytes.Buffer
	log := logging.New(&buf, slog.LevelInfo)

	ctx := correlation.WithID(context.Background(), "corr-456")

	// WithAttrs 経由の派生ロガー。
	log.With("component", "test").InfoContext(ctx, "with-attrs")
	m := decodeLine(t, &buf)
	assert.Equal(t, "corr-456", m["correlation_id"], "WithAttrs 後も相関 ID が載る")
	assert.Equal(t, "test", m["component"], "追加した属性も載る")

	// WithGroup 経由の派生ロガー。相関 ID の付与は維持されるが、下位ハンドラのグループが
	// Handle で追加した属性にも適用されるため correlation_id はグループ内にネストされる。
	buf.Reset()
	log.WithGroup("grp").InfoContext(ctx, "with-group")
	m = decodeLine(t, &buf)
	grp, ok := m["grp"].(map[string]any)
	require.True(t, ok, "grp グループが存在する")
	assert.Equal(t, "corr-456", grp["correlation_id"], "WithGroup 後も相関 ID が載る（グループ内）")
}
