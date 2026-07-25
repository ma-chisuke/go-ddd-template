package inventory_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/go-ddd-template/contexts/inventory/internal/domain/inventory"
)

// mustRef はテスト用に ReservationRef を生成するヘルパー。
func mustRef(t *testing.T, s string) inventory.ReservationRef {
	t.Helper()
	ref, err := inventory.NewReservationRef(s)
	require.NoError(t, err, "ReservationRef の生成")
	return ref
}

// seededItem は available=n の在庫項目を作る（新規作成 → 補充）。
func seededItem(t *testing.T, sku string, n int) *inventory.StockItem {
	t.Helper()
	item, err := inventory.NewStockItem("id-"+sku, mustSKU(t, sku))
	require.NoError(t, err, "NewStockItem")
	require.NoError(t, item.Replenish(mustQuantity(t, n)), "Replenish")
	_ = item.PullEvents() // 補充イベントは以降のテストに無関係なので捨てる
	return item
}

// hasEvent はイベント列に指定種別が含まれるかを返す。
func hasEvent(events []inventory.DomainEvent, name string) bool {
	for _, e := range events {
		if e.EventName() == name {
			return true
		}
	}
	return false
}

func TestStockItem_Reserve(t *testing.T) {
	t.Parallel()

	t.Run("正常系: available を減らし StockReserved を記録する", func(t *testing.T) {
		t.Parallel()

		item := seededItem(t, "WIDGET-001", 10)
		require.NoError(t, item.Reserve(mustRef(t, "RES-1"), mustQuantity(t, 4), time.Hour))
		assert.Equal(t, 6, item.Available().Int(), "Available")
		assert.Equal(t, 4, item.Reserved().Int(), "Reserved")
		assert.True(t, hasEvent(item.PullEvents(), "inventory.stock_reserved"), "StockReserved が記録される")
	})

	t.Run("冪等: 同一 ref の再予約は no-op（二重予約しない）", func(t *testing.T) {
		t.Parallel()

		item := seededItem(t, "WIDGET-001", 10)
		ref := mustRef(t, "RES-1")
		require.NoError(t, item.Reserve(ref, mustQuantity(t, 4), time.Hour), "1 回目")
		_ = item.PullEvents()
		// 同じ ref・別の数量で再予約しても状態は変わらない。
		require.NoError(t, item.Reserve(ref, mustQuantity(t, 99), time.Hour), "2 回目")
		assert.Equal(t, 6, item.Available().Int(), "冪等 available")
		assert.Equal(t, 4, item.Reserved().Int(), "冪等 reserved")
		assert.Empty(t, item.PullEvents(), "冪等 no-op でイベントは出ない")
	})

	t.Run("異常系: 在庫不足は ErrInsufficientStock（状態は不変）", func(t *testing.T) {
		t.Parallel()

		item := seededItem(t, "WIDGET-001", 3)
		err := item.Reserve(mustRef(t, "RES-1"), mustQuantity(t, 4), time.Hour)
		require.ErrorIs(t, err, inventory.ErrInsufficientStock)
		assert.Equal(t, 3, item.Available().Int(), "失敗時 available 不変")
		assert.Equal(t, 0, item.Reserved().Int(), "失敗時 reserved 不変")
	})

	t.Run("異常系: 数量 0 と空 ref", func(t *testing.T) {
		t.Parallel()

		item := seededItem(t, "WIDGET-001", 10)
		require.ErrorIs(t, item.Reserve(mustRef(t, "RES-1"), mustQuantity(t, 0), time.Hour), inventory.ErrInvalidQuantity)
		require.ErrorIs(t, item.Reserve(inventory.ReservationRef{}, mustQuantity(t, 1), time.Hour), inventory.ErrInvalidReservationRef)
	})

	t.Run("境界: available が 0 到達で StockDepleted", func(t *testing.T) {
		t.Parallel()

		item := seededItem(t, "WIDGET-001", 5)
		require.NoError(t, item.Reserve(mustRef(t, "RES-1"), mustQuantity(t, 5), time.Hour))
		assert.Equal(t, 0, item.Available().Int(), "Available")
		assert.True(t, hasEvent(item.PullEvents(), "inventory.stock_depleted"), "StockDepleted が記録される")
	})
}

func TestStockItem_Confirm(t *testing.T) {
	t.Parallel()

	t.Run("正常系: pending → confirmed（available は不変、TTL クリアで Reap されない）", func(t *testing.T) {
		t.Parallel()

		item := seededItem(t, "WIDGET-001", 10)
		ref := mustRef(t, "RES-1")
		_ = item.Reserve(ref, mustQuantity(t, 4), time.Hour)
		_ = item.PullEvents()

		require.NoError(t, item.Confirm(ref))
		assert.Equal(t, 6, item.Available().Int(), "Confirm 後 available")
		assert.Equal(t, 4, item.Reserved().Int(), "Confirm 後 reserved")
		assert.True(t, hasEvent(item.PullEvents(), "inventory.stock_reservation_confirmed"), "StockReservationConfirmed が記録される")

		// confirmed は期限切れでも Reap されない。
		reaped := item.ReapExpired(time.Now().Add(24 * time.Hour))
		assert.Empty(t, reaped, "confirmed は Reap されない")
		assert.Equal(t, 4, item.Reserved().Int(), "confirmed は解放されない")
	})

	t.Run("冪等: 既に confirmed の ref は no-op", func(t *testing.T) {
		t.Parallel()

		item := seededItem(t, "WIDGET-001", 10)
		ref := mustRef(t, "RES-1")
		_ = item.Reserve(ref, mustQuantity(t, 4), time.Hour)
		_ = item.Confirm(ref)
		_ = item.PullEvents()
		require.NoError(t, item.Confirm(ref), "2 回目 Confirm")
		assert.Empty(t, item.PullEvents(), "冪等 no-op でイベントは出ない")
	})

	t.Run("異常系: 有効な予約が無い ref は ErrReservationNotFound", func(t *testing.T) {
		t.Parallel()

		item := seededItem(t, "WIDGET-001", 10)
		require.ErrorIs(t, item.Confirm(mustRef(t, "UNKNOWN")), inventory.ErrReservationNotFound)
	})
}

func TestStockItem_Release(t *testing.T) {
	t.Parallel()

	t.Run("正常系: pending を解放して available へ戻す", func(t *testing.T) {
		t.Parallel()

		item := seededItem(t, "WIDGET-001", 10)
		ref := mustRef(t, "RES-1")
		_ = item.Reserve(ref, mustQuantity(t, 4), time.Hour)
		_ = item.PullEvents()

		require.NoError(t, item.Release(ref))
		assert.Equal(t, 10, item.Available().Int(), "解放後 available")
		assert.Equal(t, 0, item.Reserved().Int(), "解放後 reserved")
		assert.True(t, hasEvent(item.PullEvents(), "inventory.stock_released"), "StockReleased が記録される")
	})

	t.Run("正常系: confirmed も解放できる", func(t *testing.T) {
		t.Parallel()

		item := seededItem(t, "WIDGET-001", 10)
		ref := mustRef(t, "RES-1")
		_ = item.Reserve(ref, mustQuantity(t, 4), time.Hour)
		_ = item.Confirm(ref)
		_ = item.PullEvents()
		require.NoError(t, item.Release(ref))
		assert.Equal(t, 10, item.Available().Int(), "confirmed 解放後 available")
	})

	t.Run("冪等: 未知・解放済みの ref は no-op", func(t *testing.T) {
		t.Parallel()

		item := seededItem(t, "WIDGET-001", 10)
		require.NoError(t, item.Release(mustRef(t, "UNKNOWN")), "未知 ref の Release")
		assert.Empty(t, item.PullEvents(), "no-op でイベントは出ない")
	})
}

func TestStockItem_ReapExpired(t *testing.T) {
	t.Parallel()

	// 期限切れの pending「のみ」を解放し、confirmed と未期限は触らない。
	item := seededItem(t, "WIDGET-001", 100)
	expiredPending := mustRef(t, "EXP")
	livePending := mustRef(t, "LIVE")
	confirmed := mustRef(t, "CONF")

	_ = item.Reserve(expiredPending, mustQuantity(t, 10), time.Hour)
	_ = item.Reserve(livePending, mustQuantity(t, 20), 48*time.Hour)
	_ = item.Reserve(confirmed, mustQuantity(t, 30), time.Hour)
	_ = item.Confirm(confirmed)
	_ = item.PullEvents()

	// 「予約時刻 + 24 時間」を now とする。EXP(1h)/CONF(1h) は期限切れ、LIVE(48h) は未期限。
	now := time.Now().Add(24 * time.Hour)
	reaped := item.ReapExpired(now)

	// 解放されるのは期限切れ pending の EXP だけ。
	require.Len(t, reaped, 1, "Reap 件数")
	rel, ok := reaped[0].(inventory.StockReleased)
	require.True(t, ok, "Reap されたイベントは StockReleased")
	assert.Equal(t, "EXP", rel.ReservationRef)
	// EXP の 10 が戻る。残る有効予約は LIVE(20) + CONF(30) = 50。
	assert.Equal(t, 50, item.Reserved().Int(), "Reap 後 reserved")
	// available = 100 - 20 - 30 = 50。
	assert.Equal(t, 50, item.Available().Int(), "Reap 後 available")

	// ReapExpired は内部イベントに蓄積せず戻り値で返す（PullEvents では取得できない）。
	assert.False(t, hasEvent(item.PullEvents(), "inventory.stock_released"), "ReapExpired のイベントは PullEvents に混入しない")

	// もう一度 Reap しても解放対象は無い。
	assert.Empty(t, item.ReapExpired(now), "2 回目の Reap で解放は発生しない")
}

func TestReconstituteStockItem_WithReservations(t *testing.T) {
	t.Parallel()

	res := []*inventory.Reservation{
		inventory.ReconstituteReservation(mustRef(t, "RES-1"), mustQuantity(t, 4), inventory.ReservationPending, time.Now().Add(time.Hour)),
		inventory.ReconstituteReservation(mustRef(t, "RES-2"), mustQuantity(t, 6), inventory.ReservationConfirmed, time.Time{}),
	}
	item := inventory.ReconstituteStockItem("id-1", mustSKU(t, "WIDGET-001"), mustQuantity(t, 90), 5, res)
	assert.Equal(t, 90, item.Available().Int(), "Available")
	assert.Equal(t, 10, item.Reserved().Int(), "Reserved（導出）")
	assert.Len(t, item.Reservations(), 2, "復元された予約数")
}

func TestReservationService_Allocate(t *testing.T) {
	t.Parallel()

	svc := inventory.ReservationService{}

	t.Run("正常系: 複数 SKU を全か無かで予約する", func(t *testing.T) {
		t.Parallel()

		a := seededItem(t, "SKU-A", 10)
		b := seededItem(t, "SKU-B", 10)
		ref := mustRef(t, "ORDER-1")
		lines := []inventory.ReservationLine{
			{SKU: mustSKU(t, "SKU-A"), Quantity: mustQuantity(t, 3)},
			{SKU: mustSKU(t, "SKU-B"), Quantity: mustQuantity(t, 7)},
		}
		require.NoError(t, svc.Allocate([]*inventory.StockItem{a, b}, ref, lines, time.Hour))
		assert.Equal(t, 3, a.Reserved().Int(), "A 予約数")
		assert.Equal(t, 7, b.Reserved().Int(), "B 予約数")
	})

	t.Run("異常系: 1 SKU でも不足なら全体を失敗させ部分予約を作らない", func(t *testing.T) {
		t.Parallel()

		a := seededItem(t, "SKU-A", 10)
		b := seededItem(t, "SKU-B", 2) // 不足させる
		ref := mustRef(t, "ORDER-1")
		lines := []inventory.ReservationLine{
			{SKU: mustSKU(t, "SKU-A"), Quantity: mustQuantity(t, 3)},
			{SKU: mustSKU(t, "SKU-B"), Quantity: mustQuantity(t, 7)},
		}
		err := svc.Allocate([]*inventory.StockItem{a, b}, ref, lines, time.Hour)
		require.ErrorIs(t, err, inventory.ErrInsufficientStock)
		// 部分予約が作られていないこと（A も予約されない）。
		assert.Equal(t, 0, a.Reserved().Int(), "A に部分予約なし")
		assert.Equal(t, 0, b.Reserved().Int(), "B に部分予約なし")
	})

	t.Run("異常系: 要求 SKU の在庫項目が無い", func(t *testing.T) {
		t.Parallel()

		a := seededItem(t, "SKU-A", 10)
		ref := mustRef(t, "ORDER-1")
		lines := []inventory.ReservationLine{
			{SKU: mustSKU(t, "SKU-MISSING"), Quantity: mustQuantity(t, 1)},
		}
		err := svc.Allocate([]*inventory.StockItem{a}, ref, lines, time.Hour)
		require.ErrorIs(t, err, inventory.ErrStockItemNotFound)
	})
}
