package application_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/example/go-ddd-template/contexts/inventory/internal/adapter/outbound/memory"
	"github.com/example/go-ddd-template/contexts/inventory/internal/application"
	"github.com/example/go-ddd-template/contexts/inventory/internal/domain/inventory"
	"github.com/example/go-ddd-template/shared/uow"
)

// testLogger は出力を捨てる構造化ロガーを返す。
func testLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// fixture はインメモリアダプタで組み立てたユースケース一式と、
// 配信されたイベントの記録を保持する。
type fixture struct {
	replenisher *application.Replenisher
	viewer      *application.StockViewer
	captured    *[]inventory.DomainEvent
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	store := memory.NewStore()
	work := memory.NewUnitOfWork(store, memory.NewOutboxStore())
	read := memory.NewReadStockStore(store)
	exec := uow.NewExecutor(uow.WithBaseBackoff(0))
	log := testLogger()

	captured := &[]inventory.DomainEvent{}
	// 実際の InProcessDispatcher を使い、購読ハンドラで配信イベントを記録する。
	dispatcher := application.NewInProcessDispatcher(log, func(_ context.Context, e inventory.DomainEvent) {
		*captured = append(*captured, e)
	})

	return fixture{
		replenisher: application.NewReplenisher(exec, work, dispatcher, log),
		viewer:      application.NewStockViewer(read, log),
		captured:    captured,
	}
}

func TestReplenishThenQuery(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	// 1) 未登録 SKU を補充すると新規作成され version 1 になる。
	res, err := f.replenisher.Replenish(ctx, application.ReplenishInput{SKU: "WIDGET-001", Quantity: 10})
	if err != nil {
		t.Fatalf("Replenish 想定外のエラー: %v", err)
	}
	if res.Available != 10 || res.Version != 1 || res.Reserved != 0 {
		t.Fatalf("補充結果が不正: %+v", res)
	}

	// 保存成功後にイベントが 1 件配信されている。
	if len(*f.captured) != 1 {
		t.Fatalf("配信イベント数 = %d, want 1", len(*f.captured))
	}
	if _, ok := (*f.captured)[0].(inventory.StockReplenished); !ok {
		t.Fatalf("配信イベント型 = %T, want StockReplenished", (*f.captured)[0])
	}

	// 2) 照会すると補充結果が読める。
	view, err := f.viewer.QueryStock(ctx, application.QueryStockInput{SKU: "WIDGET-001"})
	if err != nil {
		t.Fatalf("QueryStock 想定外のエラー: %v", err)
	}
	if view.Available != 10 || view.Version != 1 {
		t.Fatalf("照会結果が不正: %+v", view)
	}

	// 3) 既存 SKU を再補充すると version が上がる。
	res2, err := f.replenisher.Replenish(ctx, application.ReplenishInput{SKU: "WIDGET-001", Quantity: 5})
	if err != nil {
		t.Fatalf("再補充 想定外のエラー: %v", err)
	}
	if res2.Available != 15 || res2.Version != 2 {
		t.Fatalf("再補充結果が不正: %+v", res2)
	}
	if len(*f.captured) != 2 {
		t.Fatalf("配信イベント総数 = %d, want 2", len(*f.captured))
	}
}

func TestQueryStock_NotFound(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	_, err := f.viewer.QueryStock(ctx, application.QueryStockInput{SKU: "MISSING"})
	if !errors.Is(err, inventory.ErrStockItemNotFound) {
		t.Fatalf("エラー = %v, want ErrStockItemNotFound", err)
	}
}

func TestReplenish_ValidationErrors(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	tests := []struct {
		name  string
		input application.ReplenishInput
		want  error
	}{
		{"空 SKU", application.ReplenishInput{SKU: "", Quantity: 1}, inventory.ErrInvalidSKU},
		{"負の数量", application.ReplenishInput{SKU: "X", Quantity: -1}, inventory.ErrInvalidQuantity},
		{"数量 0", application.ReplenishInput{SKU: "X", Quantity: 0}, inventory.ErrInvalidQuantity},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.replenisher.Replenish(ctx, tc.input)
			if !errors.Is(err, tc.want) {
				t.Fatalf("エラー = %v, want %v", err, tc.want)
			}
		})
	}

	// 入力検証で失敗したときはイベントを配信しない。
	if len(*f.captured) != 0 {
		t.Fatalf("検証失敗時に配信されたイベント = %d, want 0", len(*f.captured))
	}
}
