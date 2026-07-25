package logpub_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/go-ddd-template/shared/outbox"
	"github.com/example/go-ddd-template/shared/outbox/logpub"
)

// TestPublish_LogsAndSucceeds は、no-op Publisher が常に成功し、送出しようとした
// メッセージの識別情報を構造化ログへ記録することを確認する。
func TestPublish_LogsAndSucceeds(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	pub := logpub.New(log)

	err := pub.Publish(context.Background(), outbox.Message{
		ID:      "msg-1",
		Type:    "demo.message",
		TraceID: "trace-1",
	})
	require.NoError(t, err, "no-op Publisher は常に成功する")

	var m map[string]any
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &m), "ログ行のデコード")
	assert.Equal(t, "msg-1", m["id"], "メッセージ ID がログに載る")
	assert.Equal(t, "demo.message", m["type"], "種別がログに載る")
	assert.Equal(t, "trace-1", m["trace_id"], "trace_id がログに載る")
}
