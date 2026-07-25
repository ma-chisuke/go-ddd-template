package memory_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/go-ddd-template/shared/outbox"
	"github.com/example/go-ddd-template/shared/outbox/memory"
)

func testLogger() *slog.Logger { return slog.New(slog.NewJSONHandler(io.Discard, nil)) }

// recordingPublisher は送出されたメッセージを記録する。
type recordingPublisher struct{ sent []outbox.Message }

func (p *recordingPublisher) Publish(_ context.Context, m outbox.Message) error {
	p.sent = append(p.sent, m)
	return nil
}

func msg(id string) outbox.Message { return outbox.Message{ID: id, Type: "demo.message"} }

// TestStores_CommitStagedWritesBothQueueAndLog は、CommitStaged がキューと恒久ログの
// 両方へ同時に書くことを確認する（唯一の書き込み継ぎ目）。
func TestStores_CommitStagedWritesBothQueueAndLog(t *testing.T) {
	s := memory.NewStores()
	s.CommitStaged([]outbox.Message{msg("m-1"), msg("m-2")})

	queued := s.Queued()
	events := s.Events()
	require.Len(t, queued, 2, "配送キューに 2 件")
	require.Len(t, events, 2, "恒久ログに 2 件")
	assert.Equal(t, "m-1", queued[0].ID, "キューは発生順")
	assert.Equal(t, "m-1", events[0].ID, "ログは発生順")
}

// TestStores_MarkPublishedRemovesFromQueueButKeepsLog は本設計の核。
// MarkPublished で配送キューからは消えるが恒久ログには残るという非対称性を、
// 「キューは空」かつ「ログは残る」の対で検証する（片方だけでは壊れた実装でも満たせる）。
func TestStores_MarkPublishedRemovesFromQueueButKeepsLog(t *testing.T) {
	ctx := context.Background()
	s := memory.NewStores()
	s.CommitStaged([]outbox.Message{msg("m-1"), msg("m-2")})

	require.NoError(t, s.Outbox().MarkPublished(ctx, "m-1", "m-2"))

	assert.Empty(t, s.Queued(), "送信済みは配送キューから消える（delete-after-publish）")
	assert.Len(t, s.Events(), 2, "恒久ログは削除されない（追記専用）")
}

// TestStores_OutboxEnqueueWritesBothQueueAndLog は、Outbox() ビュー経由の Enqueue が
// キューと恒久ログの両方へ書くことを確認する（PostgreSQL アダプタと同じ意味論）。
func TestStores_OutboxEnqueueWritesBothQueueAndLog(t *testing.T) {
	ctx := context.Background()
	s := memory.NewStores()

	require.NoError(t, s.Outbox().Enqueue(ctx, msg("m-1")))

	assert.Len(t, s.Queued(), 1, "Enqueue は配送キューへ書く")
	assert.Len(t, s.Events(), 1, "Enqueue は恒久ログへも書く（postgres と同じ意味論）")
}

// TestStores_RunnerDrainsQueueLogRetained は、中継（Runner）が配送キューをドレインしても
// 恒久ログは残ることを確認する（inventory の memory/outbox_test.go から移送した観点）。
func TestStores_RunnerDrainsQueueLogRetained(t *testing.T) {
	ctx := context.Background()
	s := memory.NewStores()
	s.CommitStaged([]outbox.Message{msg("m-1")})

	unpub, err := s.Outbox().Unpublished(ctx, 10)
	require.NoError(t, err, "Unpublished")
	require.Len(t, unpub, 1, "コミット後の未送信メッセージ数")

	pub := &recordingPublisher{}
	runner := outbox.NewRunner(s.Outbox(), pub, testLogger(), outbox.WithBatch(10))
	sent, err := runner.RunOnce(ctx)
	require.NoError(t, err, "RunOnce")
	assert.Equal(t, 1, sent, "送出件数")
	assert.Len(t, pub.sent, 1, "publish 件数")

	again, _ := s.Outbox().Unpublished(ctx, 10)
	assert.Empty(t, again, "送信済みの行は配送キューに残らない")
	assert.Empty(t, s.Queued(), "配送キューは空になる")
	assert.Len(t, s.Events(), 1, "配送後もイベントログは残る")
}

// TestStores_UnpublishedRespectsLimit は、Unpublished が limit 件で打ち切ることを確認する。
func TestStores_UnpublishedRespectsLimit(t *testing.T) {
	ctx := context.Background()
	s := memory.NewStores()
	s.CommitStaged([]outbox.Message{msg("m-1"), msg("m-2"), msg("m-3")})

	got, err := s.Outbox().Unpublished(ctx, 2)
	require.NoError(t, err, "Unpublished")
	assert.Len(t, got, 2, "limit 件だけ返す")
}

// TestStores_QueuedAndEventsReturnCopies は、Queued / Events の返り値を書き換えても
// 内部状態が壊れないこと（防御的コピー）を確認する。
func TestStores_QueuedAndEventsReturnCopies(t *testing.T) {
	s := memory.NewStores()
	s.CommitStaged([]outbox.Message{msg("m-1")})

	q := s.Queued()
	q[0].ID = "mutated"
	assert.Equal(t, "m-1", s.Queued()[0].ID, "Queued の返り値を書き換えても内部状態は不変")

	e := s.Events()
	e[0].ID = "mutated"
	assert.Equal(t, "m-1", s.Events()[0].ID, "Events の返り値を書き換えても内部状態は不変")
}

// TestStores_CommitStagedEmptyIsNoop は、空スライスの CommitStaged が何もしないことを確認する。
func TestStores_CommitStagedEmptyIsNoop(t *testing.T) {
	s := memory.NewStores()
	s.CommitStaged(nil)
	assert.Empty(t, s.Queued(), "空コミットはキューを変えない")
	assert.Empty(t, s.Events(), "空コミットはログを変えない")
}
