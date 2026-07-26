package application_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/go-ddd-template/contexts/inventory/internal/adapter/outbound/memory"
	"github.com/example/go-ddd-template/contexts/inventory/internal/application"
	"github.com/example/go-ddd-template/contexts/inventory/internal/domain"
	"github.com/example/go-ddd-template/shared/event"
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
	captured    *[]domain.DomainEvent
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	store := memory.NewStore()
	work := memory.NewUnitOfWork(store, memory.NewStores())
	read := memory.NewReadStockStore(store)
	exec := uow.NewExecutor(uow.WithBaseBackoff(0))
	log := testLogger()

	captured := &[]domain.DomainEvent{}
	// 実際の InProcessDispatcher を使い、購読ハンドラで配信イベントを記録する。
	dispatcher := event.NewTyped[domain.DomainEvent](log, func(_ context.Context, e domain.DomainEvent) {
		*captured = append(*captured, e)
	})

	return fixture{
		replenisher: application.NewReplenisher(exec, work, dispatcher, log),
		viewer:      application.NewStockViewer(read, log),
		captured:    captured,
	}
}

func TestReplenishThenQuery(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := newFixture(t)

	// 1) 未登録 SKU を補充すると新規作成され version 1 になる。
	res, err := f.replenisher.Replenish(ctx, application.ReplenishInput{SKU: "WIDGET-001", Quantity: 10})
	require.NoError(t, err, "Replenish")
	assert.Equal(t, 10, res.Available)
	assert.Equal(t, 1, res.Version)
	assert.Equal(t, 0, res.Reserved)

	// 保存成功後にイベントが 1 件配信されている。
	require.Len(t, *f.captured, 1, "配信イベント数")
	assert.IsType(t, domain.StockReplenished{}, (*f.captured)[0])

	// 2) 照会すると補充結果が読める。
	view, err := f.viewer.QueryStock(ctx, application.QueryStockInput{SKU: "WIDGET-001"})
	require.NoError(t, err, "QueryStock")
	assert.Equal(t, 10, view.Available)
	assert.Equal(t, 1, view.Version)

	// 3) 既存 SKU を再補充すると version が上がる。
	res2, err := f.replenisher.Replenish(ctx, application.ReplenishInput{SKU: "WIDGET-001", Quantity: 5})
	require.NoError(t, err, "再補充")
	assert.Equal(t, 15, res2.Available)
	assert.Equal(t, 2, res2.Version)
	assert.Len(t, *f.captured, 2, "配信イベント総数")
}

func TestQueryStock_NotFound(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := newFixture(t)

	_, err := f.viewer.QueryStock(ctx, application.QueryStockInput{SKU: "MISSING"})
	require.ErrorIs(t, err, domain.ErrStockItemNotFound)
}

func TestReplenish_ValidationErrors(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	tests := []struct {
		name  string
		input application.ReplenishInput
		want  error
	}{
		{
			name:  "境界: 空の SKU は ErrInvalidSKU",
			input: application.ReplenishInput{SKU: "", Quantity: 1},
			want:  domain.ErrInvalidSKU,
		},
		{
			name:  "境界: 負の数量は ErrInvalidQuantity",
			input: application.ReplenishInput{SKU: "X", Quantity: -1},
			want:  domain.ErrInvalidQuantity,
		},
		{
			name:  "境界: 数量 0 は ErrInvalidQuantity",
			input: application.ReplenishInput{SKU: "X", Quantity: 0},
			want:  domain.ErrInvalidQuantity,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// フィクスチャはサブテストの内側で組む（C-5）。ループの外で 1 度だけ組むと
			// 並列サブテストが captured を共有し、どのケースが配信したのかを分離できない。
			f := newFixture(t)

			_, err := f.replenisher.Replenish(ctx, tc.input)
			require.ErrorIs(t, err, tc.want)

			// 入力検証で失敗したときはイベントを配信しない。
			// この検証をループの外に置くと、並列サブテストが動く前に評価されて空振りする
			// （t.Parallel() を呼んだサブテストは親関数が返るまで実行されない）。
			assert.Empty(t, *f.captured, "検証失敗時にイベントは配信されない")
		})
	}
}
