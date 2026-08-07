package memory_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/go-ddd-template/contexts/ordering/internal/adapter/outbound/memory"
	"github.com/example/go-ddd-template/contexts/ordering/internal/application"
	"github.com/example/go-ddd-template/contexts/ordering/internal/domain"
	"github.com/example/go-ddd-template/shared/uow"
)

func mustShipmentID(t *testing.T, s string) domain.ShipmentID {
	t.Helper()
	id, err := domain.NewShipmentID(s)
	require.NoError(t, err, "ShipmentID 生成")
	return id
}

func mustOrderID(t *testing.T, s string) domain.OrderID {
	t.Helper()
	id, err := domain.NewOrderID(s)
	require.NoError(t, err, "OrderID 生成")
	return id
}

// newShipment は未永続化（version 0）の出荷を組み立てる。
func newShipment(t *testing.T, shipmentID, orderID string) *domain.Shipment {
	t.Helper()
	s, err := domain.NewShipment(mustShipmentID(t, shipmentID), mustOrderID(t, orderID))
	require.NoError(t, err, "Shipment 生成")
	return s
}

// 2 つ目の集約ルートを足しても、擬似トランザクションの意味論（ロールバックで破棄・
// コミットで確定）が変わっていないことを、負側と正側の対で 1 回の観測で確かめる。
// 片方だけなら「常に書かない実装」「常に書く実装」でも満たせてしまう。
func TestUnitOfWork_ShipmentRollbackAndCommit(t *testing.T) {
	ctx := context.Background()
	orders := memory.NewOrderRows()
	shipments := memory.NewShipmentRows()
	work := memory.NewUnitOfWork(orders, shipments, memory.NewStores())
	read := memory.NewReadShipmentStore(shipments)

	// 負側: 保存したあとに中断すると、確定ストアには 1 行も残らない。
	sentinel := errors.New("業務都合で中断")
	err := work.Within(ctx, func(ctx context.Context, r application.Repos) error {
		if err := r.Shipments().Save(ctx, newShipment(t, "SHIP-1", "ORDER-1")); err != nil {
			return err
		}
		return sentinel
	})
	require.ErrorIs(t, err, sentinel)
	_, err = read.Load(ctx, mustShipmentID(t, "SHIP-1"))
	require.ErrorIs(t, err, domain.ErrShipmentNotFound, "ロールバック後の読み込み")

	// 正側: 同じ操作をコミットすると確定している。
	err = work.Within(ctx, func(ctx context.Context, r application.Repos) error {
		return r.Shipments().Save(ctx, newShipment(t, "SHIP-1", "ORDER-1"))
	})
	require.NoError(t, err, "コミット")
	got, err := read.Load(ctx, mustShipmentID(t, "SHIP-1"))
	require.NoError(t, err, "コミット後の読み込み")
	assert.Equal(t, domain.StatusPreparing, got.Status(), "確定した状態")
	assert.Equal(t, 1, got.Version(), "確定した version")
}

// 楽観的排他制御の衝突を DB なしで再現する（出荷でも注文と同じ意味論であること）。
func TestUnitOfWork_ShipmentConcurrencyConflict(t *testing.T) {
	ctx := context.Background()
	orders := memory.NewOrderRows()
	shipments := memory.NewShipmentRows()
	work := memory.NewUnitOfWork(orders, shipments, memory.NewStores())

	require.NoError(t, work.Within(ctx, func(ctx context.Context, r application.Repos) error {
		return r.Shipments().Save(ctx, newShipment(t, "SHIP-2", "ORDER-2"))
	}), "初期データ投入")

	// 同一 ID の新規挿入（version 0）は既存行と衝突する。
	err := work.Within(ctx, func(ctx context.Context, r application.Repos) error {
		return r.Shipments().Save(ctx, newShipment(t, "SHIP-2", "ORDER-2"))
	})
	require.ErrorIs(t, err, uow.ErrConcurrencyConflict)
}

// 読み取り専用アダプタは書き込みを受け付けない（誤用の早期検知）。
func TestReadShipmentStore_RejectsWrite(t *testing.T) {
	shipments := memory.NewShipmentRows()
	err := memory.NewReadShipmentStore(shipments).Save(context.Background(), newShipment(t, "SHIP-3", "ORDER-3"))
	require.Error(t, err, "読み取り専用アダプタへの書き込み")
}
