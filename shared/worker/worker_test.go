package worker_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/go-ddd-template/shared/worker"
)

// TestSafely_RecoversPanicAndLogs は、fn が panic しても Safely が回復し、
// worker 名を添えてログに記録することを確認する。
func TestSafely_RecoversPanicAndLogs(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelError}))

	// panic しても呼び出し元へ伝播しない（回復される）。
	assert.NotPanics(t, func() {
		worker.Safely(context.Background(), log, "reaper", func(context.Context) {
			panic("boom")
		})
	}, "panic は Safely 内で回復されるべき")

	var m map[string]any
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &m), "ログ行のデコード")
	assert.Equal(t, "reaper", m["worker"], "worker 名がログに記録される")
	assert.Equal(t, "boom", m["panic"], "panic 値がログに記録される")
	assert.Equal(t, "ERROR", m["level"], "ERROR レベルで記録される")
}

// TestSafely_NoSideEffectOnNormalReturn は、fn が正常終了した場合は
// ログに何も出力されず、fn が期待どおり 1 回だけ実行されることを確認する。
func TestSafely_NoSideEffectOnNormalReturn(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelError}))

	calls := 0
	worker.Safely(context.Background(), log, "relay", func(context.Context) {
		calls++
	})

	assert.Equal(t, 1, calls, "fn はちょうど 1 回実行される")
	assert.Empty(t, buf.String(), "正常終了時はログに何も出力されない")
}
