package inventory_test

import (
	"errors"
	"testing"
	"time"

	"github.com/example/go-ddd-template/contexts/inventory/internal/domain/inventory"
)

// mustRef はテスト用に ReservationRef を生成するヘルパー。
func mustRef(t *testing.T, s string) inventory.ReservationRef {
	t.Helper()
	ref, err := inventory.NewReservationRef(s)
	if err != nil {
		t.Fatalf("ReservationRef の生成に失敗しました: %v", err)
	}
	return ref
}

// seededItem は available=n の在庫項目を作る（新規作成 → 補充）。
func seededItem(t *testing.T, sku string, n int) *inventory.StockItem {
	t.Helper()
	item, err := inventory.NewStockItem("id-"+sku, mustSKU(t, sku))
	if err != nil {
		t.Fatalf("NewStockItem 失敗: %v", err)
	}
	if err := item.Replenish(mustQuantity(t, n)); err != nil {
		t.Fatalf("Replenish 失敗: %v", err)
	}
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
	t.Run("正常系: available を減らし StockReserved を記録する", func(t *testing.T) {
		item := seededItem(t, "WIDGET-001", 10)
		if err := item.Reserve(mustRef(t, "RES-1"), mustQuantity(t, 4), time.Hour); err != nil {
			t.Fatalf("想定外のエラー: %v", err)
		}
		if item.Available().Int() != 6 {
			t.Fatalf("Available = %d, want 6", item.Available().Int())
		}
		if item.Reserved().Int() != 4 {
			t.Fatalf("Reserved = %d, want 4", item.Reserved().Int())
		}
		if events := item.PullEvents(); !hasEvent(events, "inventory.stock_reserved") {
			t.Fatalf("StockReserved が記録されていない: %+v", events)
		}
	})

	t.Run("冪等: 同一 ref の再予約は no-op（二重予約しない）", func(t *testing.T) {
		item := seededItem(t, "WIDGET-001", 10)
		ref := mustRef(t, "RES-1")
		if err := item.Reserve(ref, mustQuantity(t, 4), time.Hour); err != nil {
			t.Fatalf("1 回目 想定外のエラー: %v", err)
		}
		_ = item.PullEvents()
		// 同じ ref・別の数量で再予約しても状態は変わらない。
		if err := item.Reserve(ref, mustQuantity(t, 99), time.Hour); err != nil {
			t.Fatalf("2 回目 想定外のエラー: %v", err)
		}
		if item.Available().Int() != 6 || item.Reserved().Int() != 4 {
			t.Fatalf("冪等でない: available=%d reserved=%d", item.Available().Int(), item.Reserved().Int())
		}
		if events := item.PullEvents(); len(events) != 0 {
			t.Fatalf("冪等 no-op でイベントが出た: %+v", events)
		}
	})

	t.Run("異常系: 在庫不足は ErrInsufficientStock（状態は不変）", func(t *testing.T) {
		item := seededItem(t, "WIDGET-001", 3)
		err := item.Reserve(mustRef(t, "RES-1"), mustQuantity(t, 4), time.Hour)
		if !errors.Is(err, inventory.ErrInsufficientStock) {
			t.Fatalf("エラー = %v, want ErrInsufficientStock", err)
		}
		if item.Available().Int() != 3 || item.Reserved().Int() != 0 {
			t.Fatalf("失敗時に状態が変わった: available=%d reserved=%d", item.Available().Int(), item.Reserved().Int())
		}
	})

	t.Run("異常系: 数量 0 と空 ref", func(t *testing.T) {
		item := seededItem(t, "WIDGET-001", 10)
		if err := item.Reserve(mustRef(t, "RES-1"), mustQuantity(t, 0), time.Hour); !errors.Is(err, inventory.ErrInvalidQuantity) {
			t.Fatalf("数量 0 のエラー = %v, want ErrInvalidQuantity", err)
		}
		if err := item.Reserve(inventory.ReservationRef{}, mustQuantity(t, 1), time.Hour); !errors.Is(err, inventory.ErrInvalidReservationRef) {
			t.Fatalf("空 ref のエラー = %v, want ErrInvalidReservationRef", err)
		}
	})

	t.Run("available が 0 到達で StockDepleted", func(t *testing.T) {
		item := seededItem(t, "WIDGET-001", 5)
		if err := item.Reserve(mustRef(t, "RES-1"), mustQuantity(t, 5), time.Hour); err != nil {
			t.Fatalf("想定外のエラー: %v", err)
		}
		if item.Available().Int() != 0 {
			t.Fatalf("Available = %d, want 0", item.Available().Int())
		}
		if events := item.PullEvents(); !hasEvent(events, "inventory.stock_depleted") {
			t.Fatalf("StockDepleted が記録されていない: %+v", events)
		}
	})
}

func TestStockItem_Confirm(t *testing.T) {
	t.Run("正常系: pending → confirmed（available は不変、TTL クリアで Reap されない）", func(t *testing.T) {
		item := seededItem(t, "WIDGET-001", 10)
		ref := mustRef(t, "RES-1")
		_ = item.Reserve(ref, mustQuantity(t, 4), time.Hour)
		_ = item.PullEvents()

		if err := item.Confirm(ref); err != nil {
			t.Fatalf("想定外のエラー: %v", err)
		}
		if item.Available().Int() != 6 || item.Reserved().Int() != 4 {
			t.Fatalf("Confirm で数量が変化した: available=%d reserved=%d", item.Available().Int(), item.Reserved().Int())
		}
		if events := item.PullEvents(); !hasEvent(events, "inventory.stock_reservation_confirmed") {
			t.Fatalf("StockReservationConfirmed が記録されていない: %+v", events)
		}

		// confirmed は期限切れでも Reap されない。
		reaped := item.ReapExpired(time.Now().Add(24 * time.Hour))
		if len(reaped) != 0 {
			t.Fatalf("confirmed が Reap された: %+v", reaped)
		}
		if item.Reserved().Int() != 4 {
			t.Fatalf("confirmed が解放された: reserved=%d", item.Reserved().Int())
		}
	})

	t.Run("冪等: 既に confirmed の ref は no-op", func(t *testing.T) {
		item := seededItem(t, "WIDGET-001", 10)
		ref := mustRef(t, "RES-1")
		_ = item.Reserve(ref, mustQuantity(t, 4), time.Hour)
		_ = item.Confirm(ref)
		_ = item.PullEvents()
		if err := item.Confirm(ref); err != nil {
			t.Fatalf("2 回目 Confirm 想定外のエラー: %v", err)
		}
		if events := item.PullEvents(); len(events) != 0 {
			t.Fatalf("冪等 no-op でイベントが出た: %+v", events)
		}
	})

	t.Run("異常系: 有効な予約が無い ref は ErrReservationNotFound", func(t *testing.T) {
		item := seededItem(t, "WIDGET-001", 10)
		if err := item.Confirm(mustRef(t, "UNKNOWN")); !errors.Is(err, inventory.ErrReservationNotFound) {
			t.Fatalf("エラー = %v, want ErrReservationNotFound", err)
		}
	})
}

func TestStockItem_Release(t *testing.T) {
	t.Run("正常系: pending を解放して available へ戻す", func(t *testing.T) {
		item := seededItem(t, "WIDGET-001", 10)
		ref := mustRef(t, "RES-1")
		_ = item.Reserve(ref, mustQuantity(t, 4), time.Hour)
		_ = item.PullEvents()

		if err := item.Release(ref); err != nil {
			t.Fatalf("想定外のエラー: %v", err)
		}
		if item.Available().Int() != 10 || item.Reserved().Int() != 0 {
			t.Fatalf("解放後の状態が不正: available=%d reserved=%d", item.Available().Int(), item.Reserved().Int())
		}
		if events := item.PullEvents(); !hasEvent(events, "inventory.stock_released") {
			t.Fatalf("StockReleased が記録されていない: %+v", events)
		}
	})

	t.Run("正常系: confirmed も解放できる", func(t *testing.T) {
		item := seededItem(t, "WIDGET-001", 10)
		ref := mustRef(t, "RES-1")
		_ = item.Reserve(ref, mustQuantity(t, 4), time.Hour)
		_ = item.Confirm(ref)
		_ = item.PullEvents()
		if err := item.Release(ref); err != nil {
			t.Fatalf("想定外のエラー: %v", err)
		}
		if item.Available().Int() != 10 {
			t.Fatalf("confirmed 解放後の available = %d, want 10", item.Available().Int())
		}
	})

	t.Run("冪等: 未知・解放済みの ref は no-op", func(t *testing.T) {
		item := seededItem(t, "WIDGET-001", 10)
		if err := item.Release(mustRef(t, "UNKNOWN")); err != nil {
			t.Fatalf("未知 ref の Release がエラー: %v", err)
		}
		if events := item.PullEvents(); len(events) != 0 {
			t.Fatalf("no-op でイベントが出た: %+v", events)
		}
	})
}

func TestStockItem_ReapExpired(t *testing.T) {
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
	if len(reaped) != 1 {
		t.Fatalf("Reap 件数 = %d, want 1", len(reaped))
	}
	rel, ok := reaped[0].(inventory.StockReleased)
	if !ok || rel.ReservationRef != "EXP" {
		t.Fatalf("Reap されたイベントが不正: %+v", reaped[0])
	}
	// EXP の 10 が戻る。残る有効予約は LIVE(20) + CONF(30) = 50。
	if item.Reserved().Int() != 50 {
		t.Fatalf("Reap 後の reserved = %d, want 50", item.Reserved().Int())
	}
	// available = 100 - 20 - 30 = 50。
	if item.Available().Int() != 50 {
		t.Fatalf("Reap 後の available = %d, want 50", item.Available().Int())
	}

	// ReapExpired は内部イベントに蓄積せず戻り値で返す（PullEvents では取得できない）。
	if events := item.PullEvents(); hasEvent(events, "inventory.stock_released") {
		t.Fatalf("ReapExpired のイベントが PullEvents に混入した: %+v", events)
	}

	// もう一度 Reap しても解放対象は無い。
	if again := item.ReapExpired(now); len(again) != 0 {
		t.Fatalf("2 回目の Reap で解放が発生: %+v", again)
	}
}

func TestReconstituteStockItem_WithReservations(t *testing.T) {
	res := []*inventory.Reservation{
		inventory.ReconstituteReservation(mustRef(t, "RES-1"), mustQuantity(t, 4), inventory.ReservationPending, time.Now().Add(time.Hour)),
		inventory.ReconstituteReservation(mustRef(t, "RES-2"), mustQuantity(t, 6), inventory.ReservationConfirmed, time.Time{}),
	}
	item := inventory.ReconstituteStockItem("id-1", mustSKU(t, "WIDGET-001"), mustQuantity(t, 90), 5, res)
	if item.Available().Int() != 90 {
		t.Fatalf("Available = %d, want 90", item.Available().Int())
	}
	if item.Reserved().Int() != 10 {
		t.Fatalf("Reserved（導出）= %d, want 10", item.Reserved().Int())
	}
	if len(item.Reservations()) != 2 {
		t.Fatalf("復元された予約数 = %d, want 2", len(item.Reservations()))
	}
}

func TestReservationService_Allocate(t *testing.T) {
	svc := inventory.ReservationService{}

	t.Run("正常系: 複数 SKU を全か無かで予約する", func(t *testing.T) {
		a := seededItem(t, "SKU-A", 10)
		b := seededItem(t, "SKU-B", 10)
		ref := mustRef(t, "ORDER-1")
		lines := []inventory.ReservationLine{
			{SKU: mustSKU(t, "SKU-A"), Quantity: mustQuantity(t, 3)},
			{SKU: mustSKU(t, "SKU-B"), Quantity: mustQuantity(t, 7)},
		}
		if err := svc.Allocate([]*inventory.StockItem{a, b}, ref, lines, time.Hour); err != nil {
			t.Fatalf("想定外のエラー: %v", err)
		}
		if a.Reserved().Int() != 3 || b.Reserved().Int() != 7 {
			t.Fatalf("予約数が不正: A=%d B=%d", a.Reserved().Int(), b.Reserved().Int())
		}
	})

	t.Run("異常系: 1 SKU でも不足なら全体を失敗させ部分予約を作らない", func(t *testing.T) {
		a := seededItem(t, "SKU-A", 10)
		b := seededItem(t, "SKU-B", 2) // 不足させる
		ref := mustRef(t, "ORDER-1")
		lines := []inventory.ReservationLine{
			{SKU: mustSKU(t, "SKU-A"), Quantity: mustQuantity(t, 3)},
			{SKU: mustSKU(t, "SKU-B"), Quantity: mustQuantity(t, 7)},
		}
		err := svc.Allocate([]*inventory.StockItem{a, b}, ref, lines, time.Hour)
		if !errors.Is(err, inventory.ErrInsufficientStock) {
			t.Fatalf("エラー = %v, want ErrInsufficientStock", err)
		}
		// 部分予約が作られていないこと（A も予約されない）。
		if a.Reserved().Int() != 0 || b.Reserved().Int() != 0 {
			t.Fatalf("部分予約が作られた: A=%d B=%d", a.Reserved().Int(), b.Reserved().Int())
		}
	})

	t.Run("異常系: 要求 SKU の在庫項目が無い", func(t *testing.T) {
		a := seededItem(t, "SKU-A", 10)
		ref := mustRef(t, "ORDER-1")
		lines := []inventory.ReservationLine{
			{SKU: mustSKU(t, "SKU-MISSING"), Quantity: mustQuantity(t, 1)},
		}
		err := svc.Allocate([]*inventory.StockItem{a}, ref, lines, time.Hour)
		if !errors.Is(err, inventory.ErrStockItemNotFound) {
			t.Fatalf("エラー = %v, want ErrStockItemNotFound", err)
		}
	})
}
