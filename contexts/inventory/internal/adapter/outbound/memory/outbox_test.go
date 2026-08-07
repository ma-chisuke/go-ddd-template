package memory_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/go-ddd-template/contexts/inventory/internal/adapter/outbound/memory"
	"github.com/example/go-ddd-template/contexts/inventory/internal/application"
	"github.com/example/go-ddd-template/contexts/inventory/internal/domain"
	"github.com/example/go-ddd-template/shared/outbox"
)

func outboxTestLogger() *slog.Logger { return slog.New(slog.NewJSONHandler(io.Discard, nil)) }

// recordingPublisher は送出されたメッセージを記録する。
type recordingPublisher struct {
	sent []outbox.Message
}

func (p *recordingPublisher) Publish(_ context.Context, m outbox.Message) error {
	p.sent = append(p.sent, m)
	return nil
}

// TestOutbox_EnqueueCommitsWithSave は、集約の保存・アウトボックスへの Enqueue・
// 恒久イベントログへの記録が同一の作業単位（UoW）で原子的にコミットされることを確認する。
func TestOutbox_EnqueueCommitsWithSave(t *testing.T) {
	ctx := context.Background()
	rows := memory.NewStockItemRows()
	stores := memory.NewStores()
	work := memory.NewUnitOfWork(rows, stores)

	// UoW 内で在庫を保存しつつ、同一トランザクションでメッセージを Enqueue する。
	err := work.Within(ctx, func(ctx context.Context, r application.Repos) error {
		item, err := domain.NewStockItem("id-1", mustSKU(t, "WIDGET-001"))
		if err != nil {
			return err
		}
		if err := item.Replenish(mustQty(t, 5)); err != nil {
			return err
		}
		if err := r.Stock().Save(ctx, item); err != nil {
			return err
		}
		return r.Outbox().Enqueue(ctx, outbox.Message{
			ID:         "msg-1",
			Type:       "demo.message",
			Payload:    []byte(`{"hello":"world"}`),
			OccurredAt: time.Now().UTC(),
		})
	})
	require.NoError(t, err, "UoW")

	// コミット後、メッセージが未送信として読める。
	unpub, err := stores.Outbox().Unpublished(ctx, 10)
	require.NoError(t, err, "Unpublished")
	require.Len(t, unpub, 1, "コミット後の未送信メッセージ数")
	assert.Equal(t, "msg-1", unpub[0].ID)

	// 同一トランザクションで恒久イベントログにも記録されている。
	events := stores.Events()
	require.Len(t, events, 1, "コミット後のイベントログ件数")
	assert.Equal(t, "msg-1", events[0].ID, "イベントログの ID は outbox と同じ")
	assert.Equal(t, "demo.message", events[0].Type, "イベントログの種別")

	// 中継（Runner）が送出し、送信できた行を配送キューから削除する。
	pub := &recordingPublisher{}
	runner := outbox.NewRunner(stores.Outbox(), pub, outboxTestLogger(), outbox.WithBatch(10))
	sent, err := runner.RunOnce(ctx)
	require.NoError(t, err, "RunOnce")
	assert.Equal(t, 1, sent, "送出件数")
	assert.Len(t, pub.sent, 1, "publish 件数")

	// 送信済みの行は配送キューから消える（delete-after-publish）。
	again, _ := stores.Outbox().Unpublished(ctx, 10)
	assert.Empty(t, again, "送信済みの行は配送キューに残らない")
	assert.Empty(t, stores.Queued(), "配送キューは空になる")

	// 一方、恒久イベントログは配送後も残る（追記専用・削除しない）。
	assert.Len(t, stores.Events(), 1, "配送後もイベントログは残る")
}

// TestOutbox_RollbackDiscardsEnqueue は、UoW がロールバックすると Enqueue も
// 巻き戻る（コミットされない）ことを確認する。配送キューと恒久イベントログの
// 両方が空のままであること＝両者が同一トランザクションで確定することを示す。
func TestOutbox_RollbackDiscardsEnqueue(t *testing.T) {
	ctx := context.Background()
	rows := memory.NewStockItemRows()
	stores := memory.NewStores()
	work := memory.NewUnitOfWork(rows, stores)

	sentinel := errors.New("業務都合で中断")
	err := work.Within(ctx, func(ctx context.Context, r application.Repos) error {
		if err := r.Outbox().Enqueue(ctx, outbox.Message{ID: "msg-1", Type: "demo.message", OccurredAt: time.Now().UTC()}); err != nil {
			return err
		}
		return sentinel // ロールバック
	})
	require.ErrorIs(t, err, sentinel)

	unpub, err := stores.Outbox().Unpublished(ctx, 10)
	require.NoError(t, err, "Unpublished")
	assert.Empty(t, unpub, "ロールバックされたメッセージは残らない")
	assert.Empty(t, stores.Events(), "ロールバック時はイベントログにも記録されない")
}

// TestEvents_SameTxAsAggregateAndOutbox は、集約の保存・配送キューへの投入・
// 恒久イベントログへの記録の 3 者が「ひとつのトランザクション」で確定することを、
// ロールバック時とコミット時の両方で確認する（FR-4 / R-2 の不変条件）。
func TestEvents_SameTxAsAggregateAndOutbox(t *testing.T) {
	ctx := context.Background()
	rows := memory.NewStockItemRows()
	stores := memory.NewStores()
	work := memory.NewUnitOfWork(rows, stores)
	read := memory.NewReadStockStore(rows)
	sku := mustSKU(t, "WIDGET-TX")

	// 集約の保存とメッセージ投入を行ってから中断する。3 者すべてが巻き戻る。
	sentinel := errors.New("業務都合で中断")
	err := work.Within(ctx, func(ctx context.Context, r application.Repos) error {
		item, err := domain.NewStockItem("id-tx", sku)
		if err != nil {
			return err
		}
		if err := item.Replenish(mustQty(t, 5)); err != nil {
			return err
		}
		if err := r.Stock().Save(ctx, item); err != nil {
			return err
		}
		if err := r.Outbox().Enqueue(ctx, outbox.Message{
			ID: "tx-msg-1", Type: "demo.message", OccurredAt: time.Now().UTC(),
		}); err != nil {
			return err
		}
		return sentinel
	})
	require.ErrorIs(t, err, sentinel)

	_, err = read.Load(ctx, sku)
	require.ErrorIs(t, err, domain.ErrStockItemNotFound, "集約は保存されていない")
	assert.Empty(t, stores.Queued(), "配送キューにも残らない")
	assert.Empty(t, stores.Events(), "イベントログにも残らない")

	// 同じ操作を成功させると、3 者すべてが確定する。
	err = work.Within(ctx, func(ctx context.Context, r application.Repos) error {
		item, err := domain.NewStockItem("id-tx", sku)
		if err != nil {
			return err
		}
		if err := item.Replenish(mustQty(t, 5)); err != nil {
			return err
		}
		if err := r.Stock().Save(ctx, item); err != nil {
			return err
		}
		return r.Outbox().Enqueue(ctx, outbox.Message{
			ID: "tx-msg-1", Type: "demo.message", OccurredAt: time.Now().UTC(),
		})
	})
	require.NoError(t, err, "UoW")

	item, err := read.Load(ctx, sku)
	require.NoError(t, err, "集約が保存されている")
	assert.Equal(t, 5, item.Available().Int(), "確定 available")
	require.Len(t, stores.Queued(), 1, "配送キューに 1 件")
	require.Len(t, stores.Events(), 1, "イベントログに 1 件")
	assert.Equal(t, "tx-msg-1", stores.Events()[0].ID, "同じメッセージ ID が記録される")
}
