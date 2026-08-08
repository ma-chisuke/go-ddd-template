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

// このファイルは出荷ストア（2 つ目の集約ルートのアダプタ）の楽観的排他制御を、
// UoW を介した本物の経路で検証する。ユースケース側のテストが使う flakyUoW は衝突を
// **注入**するので、ストア自身の版チェックが働いていることは証明できない。

// errAborted はロールバックを起こすためのテスト用センチネル。
var errAborted = errors.New("業務都合で中断")

func mustShipmentID(t *testing.T, s string) domain.ShipmentID {
	t.Helper()
	id, err := domain.NewShipmentID(s)
	require.NoError(t, err, "ShipmentID 生成")
	return id
}

func mustTrackingNumber(t *testing.T, s string) domain.TrackingNumber {
	t.Helper()
	tn, err := domain.NewTrackingNumber(s)
	require.NoError(t, err, "TrackingNumber 生成")
	return tn
}

func mustShipment(t *testing.T, id, orderID string) *domain.Shipment {
	t.Helper()
	sid, err := domain.NewShipmentID(id)
	require.NoError(t, err, "ShipmentID 生成")
	oid, err := domain.NewOrderID(orderID)
	require.NoError(t, err, "OrderID 生成")
	return domain.NewShipment(sid, oid)
}

// newShipmentUoW は出荷だけを扱う最小の作業単位を組み立てる。
func newShipmentUoW(t *testing.T) (*memory.UnitOfWork, *memory.ShipmentRows) {
	t.Helper()
	shipmentRows := memory.NewShipmentRows()
	return memory.NewUnitOfWork(memory.NewOrderRows(), shipmentRows, memory.NewStores()), shipmentRows
}

// saveShipment は 1 トランザクションで出荷を保存する。
func saveShipment(ctx context.Context, work *memory.UnitOfWork, s *domain.Shipment) error {
	return work.Within(ctx, func(ctx context.Context, r application.Repos) error {
		return r.Shipments().Save(ctx, s)
	})
}

func TestShipmentStore_ConcurrencyConflict(t *testing.T) {
	ctx := context.Background()
	work, shipmentRows := newShipmentUoW(t)
	read := memory.NewReadShipmentStore(shipmentRows)

	require.NoError(t, saveShipment(ctx, work, mustShipment(t, "SHIP-1", "ORDER-1")), "初回保存")

	// 同じ出荷を 2 回読み、両方 version 1 の集約を得る。
	first, err := read.Load(ctx, mustShipmentID(t, "SHIP-1"))
	require.NoError(t, err, "1 回目の読み込み")
	second, err := read.Load(ctx, mustShipmentID(t, "SHIP-1"))
	require.NoError(t, err, "2 回目の読み込み（stale になる予定）")

	// first を発送して保存 → version 2 になる。
	require.NoError(t, first.MarkShipped(mustTrackingNumber(t, "TRACK-1")), "MarkShipped")
	require.NoError(t, saveShipment(ctx, work, first), "first の保存")
	assert.Equal(t, 2, first.Version(), "first.Version")

	// second（version 1 のまま）を保存 → 衝突。
	require.NoError(t, second.MarkShipped(mustTrackingNumber(t, "TRACK-2")), "MarkShipped")
	err = saveShipment(ctx, work, second)
	require.ErrorIs(t, err, uow.ErrConcurrencyConflict, "版が食い違えば衝突")

	// 確定済みの出荷は first の結果のまま（衝突した書き込みは確定していない）。
	final, err := read.Load(ctx, mustShipmentID(t, "SHIP-1"))
	require.NoError(t, err, "最終読み込み")
	assert.Equal(t, "TRACK-1", final.TrackingNumber().String(), "追跡番号")
	assert.Equal(t, domain.ShipmentShipped, final.Status(), "状態")
	assert.Equal(t, 2, final.Version(), "version")
}

func TestShipmentStore_DuplicateInsertConflicts(t *testing.T) {
	ctx := context.Background()
	work, _ := newShipmentUoW(t)

	require.NoError(t, saveShipment(ctx, work, mustShipment(t, "SHIP-DUP", "ORDER-1")), "初回挿入")

	// 同じ ID を version 0（未永続化）のまま挿入しようとすると衝突する。
	err := saveShipment(ctx, work, mustShipment(t, "SHIP-DUP", "ORDER-1"))

	require.ErrorIs(t, err, uow.ErrConcurrencyConflict, "重複挿入は衝突")
}

func TestShipmentStore_RollbackDiscardsStagedWrite(t *testing.T) {
	ctx := context.Background()
	work, shipmentRows := newShipmentUoW(t)
	read := memory.NewReadShipmentStore(shipmentRows)

	sentinel := errAborted
	err := work.Within(ctx, func(ctx context.Context, r application.Repos) error {
		if err := r.Shipments().Save(ctx, mustShipment(t, "SHIP-ROLLBACK", "ORDER-1")); err != nil {
			return err
		}
		return sentinel // ここで中断 → ロールバック
	})
	require.ErrorIs(t, err, sentinel, "中断のエラーが伝播する")

	_, err = read.Load(ctx, mustShipmentID(t, "SHIP-ROLLBACK"))
	require.ErrorIs(t, err, domain.ErrShipmentNotFound, "ロールバックされたので存在しない")
}

// 読み取り専用アダプタへの書き込みは誤用であり、黙って成功させない。
func TestShipmentStore_ReadOnlyStoreRejectsSave(t *testing.T) {
	read := memory.NewReadShipmentStore(memory.NewShipmentRows())

	err := read.Save(context.Background(), mustShipment(t, "SHIP-RO", "ORDER-1"))

	require.Error(t, err, "誤用は早期に検知する")
	assert.Contains(t, err.Error(), "読み取り専用", "誤用であることが文言から分かる")
}
