package correlation_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

// TestTraceparentRoundTrip は、32 桁 16 進の trace-id が Traceparent →
// TraceIDFromTraceparent で元に戻ることを確認する（span-id はホップごとに新規採番される）。
func TestTraceparentRoundTrip(t *testing.T) {
	const traceID = "0af7651916cd43dd8448eb211c80319c" // 32 桁 16 進

	tp, ok := correlation.Traceparent(traceID)
	require.True(t, ok, "32 桁 16 進なら traceparent を組み立てる")
	assert.Contains(t, tp, traceID, "traceparent は trace-id を含む")
	assert.True(t, strings.HasPrefix(tp, "00-"), "version は 00")
	assert.True(t, strings.HasSuffix(tp, "-01"), "flags は 01（sampled）")

	got, ok := correlation.TraceIDFromTraceparent(tp)
	require.True(t, ok, "組み立てた traceparent から trace-id を取り出せる")
	assert.Equal(t, traceID, got, "往復で trace-id が保存される")

	// span-id はホップごとに新規採番されるため、同じ trace-id でも 2 回の呼び出しで異なる。
	tp2, _ := correlation.Traceparent(traceID)
	assert.NotEqual(t, tp, tp2, "span-id はホップごとに新規採番される")
}

// TestTraceparent_RejectsNonConforming は、Traceparent が 32 桁 16 進でない入力を
// 拒否することを確認する（不成立時は ok=false）。
func TestTraceparent_RejectsNonConforming(t *testing.T) {
	cases := map[string]string{
		"短すぎる":   "abc123",
		"非 16 進": "zzf7651916cd43dd8448eb211c80319c",
		"33 桁":   "0af7651916cd43dd8448eb211c80319cd",
		"空":      "",
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			_, ok := correlation.Traceparent(in)
			assert.False(t, ok, "32 桁 16 進でない入力は traceparent を組み立てない")
		})
	}
}

// TestTraceIDFromTraceparent_RejectsMalformed は、TraceIDFromTraceparent が
// 全ゼロ trace-id・区切り数違い・非 16 進を拒否することを確認する。
func TestTraceIDFromTraceparent_RejectsMalformed(t *testing.T) {
	cases := map[string]string{
		"全ゼロ trace-id":    "00-00000000000000000000000000000000-b7ad6b7169203331-01",
		"区切りが 3 分割":       "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331",
		"区切りが 5 分割":       "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01-extra",
		"非 16 進 trace-id": "00-zzf7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01",
		"trace-id 桁不足":    "00-0af765-b7ad6b7169203331-01",
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			_, ok := correlation.TraceIDFromTraceparent(in)
			assert.False(t, ok, "不正な traceparent からは trace-id を取り出さない")
		})
	}
}

// TestIsHex は 16 進判定の真偽を確認する。
func TestIsHex(t *testing.T) {
	assert.True(t, correlation.IsHex("0aF9"), "16 進数字（大小混在）")
	assert.True(t, correlation.IsHex(""), "空文字列は真")
	assert.False(t, correlation.IsHex("0g"), "g は 16 進ではない")
	assert.False(t, correlation.IsHex("xyz"), "非 16 進")
}
