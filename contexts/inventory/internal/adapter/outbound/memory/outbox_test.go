package memory_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/example/go-ddd-template/contexts/inventory/internal/adapter/outbound/memory"
	"github.com/example/go-ddd-template/contexts/inventory/internal/application"
	"github.com/example/go-ddd-template/contexts/inventory/internal/domain/inventory"
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

// TestOutbox_EnqueueCommitsWithSave は、集約の保存とアウトボックスへの Enqueue が
// 同一の作業単位（UoW）で原子的にコミットされることを確認する。
func TestOutbox_EnqueueCommitsWithSave(t *testing.T) {
	ctx := context.Background()
	store := memory.NewStore()
	outboxStore := memory.NewOutboxStore()
	work := memory.NewUnitOfWork(store, outboxStore)

	// UoW 内で在庫を保存しつつ、同一トランザクションでメッセージを Enqueue する。
	err := work.Within(ctx, func(ctx context.Context, r application.Repos) error {
		item, err := inventory.NewStockItem("id-1", mustSKU(t, "WIDGET-001"))
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
	if err != nil {
		t.Fatalf("UoW 失敗: %v", err)
	}

	// コミット後、メッセージが未送信として読める。
	unpub, err := outboxStore.Unpublished(ctx, 10)
	if err != nil {
		t.Fatalf("Unpublished 失敗: %v", err)
	}
	if len(unpub) != 1 || unpub[0].ID != "msg-1" {
		t.Fatalf("コミット後の未送信メッセージが不正: %+v", unpub)
	}

	// 中継（Runner）が送出して published にする。
	pub := &recordingPublisher{}
	runner := outbox.NewRunner(outboxStore, pub, outboxTestLogger(), outbox.WithBatch(10))
	sent, err := runner.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce 失敗: %v", err)
	}
	if sent != 1 || len(pub.sent) != 1 {
		t.Fatalf("送出件数が不正: sent=%d published=%d", sent, len(pub.sent))
	}
	// 2 回目は送信済みなので 0 件。
	again, _ := outboxStore.Unpublished(ctx, 10)
	if len(again) != 0 {
		t.Fatalf("送信済みなのに未送信として残っている: %+v", again)
	}
}

// TestOutbox_RollbackDiscardsEnqueue は、UoW がロールバックすると Enqueue も
// 巻き戻る（コミットされない）ことを確認する。
func TestOutbox_RollbackDiscardsEnqueue(t *testing.T) {
	ctx := context.Background()
	store := memory.NewStore()
	outboxStore := memory.NewOutboxStore()
	work := memory.NewUnitOfWork(store, outboxStore)

	sentinel := errors.New("業務都合で中断")
	err := work.Within(ctx, func(ctx context.Context, r application.Repos) error {
		if err := r.Outbox().Enqueue(ctx, outbox.Message{ID: "msg-1", Type: "demo.message", OccurredAt: time.Now().UTC()}); err != nil {
			return err
		}
		return sentinel // ロールバック
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("エラー = %v, want sentinel", err)
	}

	unpub, err := outboxStore.Unpublished(ctx, 10)
	if err != nil {
		t.Fatalf("Unpublished 失敗: %v", err)
	}
	if len(unpub) != 0 {
		t.Fatalf("ロールバックされたのにメッセージが残っている: %+v", unpub)
	}
}
