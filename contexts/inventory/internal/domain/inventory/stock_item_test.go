package inventory_test

import (
	"errors"
	"testing"

	"github.com/example/go-ddd-template/contexts/inventory/internal/domain/inventory"
)

// mustSKU はテスト用に SKU を生成するヘルパー。生成に失敗したらテストを止める。
func mustSKU(t *testing.T, s string) inventory.SKU {
	t.Helper()
	sku, err := inventory.NewSKU(s)
	if err != nil {
		t.Fatalf("SKU の生成に失敗しました: %v", err)
	}
	return sku
}

// mustQuantity はテスト用に Quantity を生成するヘルパー。
func mustQuantity(t *testing.T, n int) inventory.Quantity {
	t.Helper()
	q, err := inventory.NewQuantity(n)
	if err != nil {
		t.Fatalf("Quantity の生成に失敗しました: %v", err)
	}
	return q
}

func TestNewSKU(t *testing.T) {
	t.Run("正常系: 空白を取り除いた値で生成できる", func(t *testing.T) {
		sku, err := inventory.NewSKU("  WIDGET-001  ")
		if err != nil {
			t.Fatalf("想定外のエラー: %v", err)
		}
		if got := sku.String(); got != "WIDGET-001" {
			t.Fatalf("String() = %q, want %q", got, "WIDGET-001")
		}
	})

	t.Run("異常系: 空文字は ErrInvalidSKU", func(t *testing.T) {
		for _, in := range []string{"", "   ", "\t"} {
			if _, err := inventory.NewSKU(in); !errors.Is(err, inventory.ErrInvalidSKU) {
				t.Fatalf("NewSKU(%q) のエラー = %v, want ErrInvalidSKU", in, err)
			}
		}
	})
}

func TestNewQuantity(t *testing.T) {
	t.Run("正常系: 0 以上は生成できる", func(t *testing.T) {
		for _, n := range []int{0, 1, 100} {
			q, err := inventory.NewQuantity(n)
			if err != nil {
				t.Fatalf("NewQuantity(%d) 想定外のエラー: %v", n, err)
			}
			if q.Int() != n {
				t.Fatalf("Int() = %d, want %d", q.Int(), n)
			}
		}
	})

	t.Run("異常系: 負数は ErrInvalidQuantity", func(t *testing.T) {
		if _, err := inventory.NewQuantity(-1); !errors.Is(err, inventory.ErrInvalidQuantity) {
			t.Fatalf("エラー = %v, want ErrInvalidQuantity", err)
		}
	})

	t.Run("IsZero と Add", func(t *testing.T) {
		zero := mustQuantity(t, 0)
		if !zero.IsZero() {
			t.Fatal("0 は IsZero であるべき")
		}
		sum := mustQuantity(t, 3).Add(mustQuantity(t, 4))
		if sum.Int() != 7 {
			t.Fatalf("3 + 4 = %d, want 7", sum.Int())
		}
	})
}

func TestNewStockItem(t *testing.T) {
	t.Run("正常系: 利用可能 0・version 0 で始まる", func(t *testing.T) {
		item, err := inventory.NewStockItem("id-1", mustSKU(t, "WIDGET-001"))
		if err != nil {
			t.Fatalf("想定外のエラー: %v", err)
		}
		if item.Available().Int() != 0 {
			t.Fatalf("Available = %d, want 0", item.Available().Int())
		}
		if item.Version() != 0 {
			t.Fatalf("Version = %d, want 0", item.Version())
		}
		if item.Reserved().Int() != 0 {
			t.Fatalf("Reserved = %d, want 0", item.Reserved().Int())
		}
		if item.ID() != "id-1" {
			t.Fatalf("ID = %q, want id-1", item.ID())
		}
	})

	t.Run("異常系: 空 id は不正", func(t *testing.T) {
		if _, err := inventory.NewStockItem("", mustSKU(t, "WIDGET-001")); err == nil {
			t.Fatal("空 id はエラーになるべき")
		}
	})
}

func TestStockItem_Replenish(t *testing.T) {
	t.Run("正常系: 利用可能在庫が増え、イベントが記録される", func(t *testing.T) {
		item, _ := inventory.NewStockItem("id-1", mustSKU(t, "WIDGET-001"))

		if err := item.Replenish(mustQuantity(t, 10)); err != nil {
			t.Fatalf("想定外のエラー: %v", err)
		}
		if err := item.Replenish(mustQuantity(t, 5)); err != nil {
			t.Fatalf("想定外のエラー: %v", err)
		}
		if item.Available().Int() != 15 {
			t.Fatalf("Available = %d, want 15", item.Available().Int())
		}

		events := item.PullEvents()
		if len(events) != 2 {
			t.Fatalf("イベント数 = %d, want 2", len(events))
		}
		first, ok := events[0].(inventory.StockReplenished)
		if !ok {
			t.Fatalf("イベント型 = %T, want StockReplenished", events[0])
		}
		if first.EventName() != "inventory.stock_replenished" {
			t.Fatalf("EventName = %q", first.EventName())
		}
		if first.QuantityAdded != 10 || first.Available != 10 {
			t.Fatalf("最初のイベント内容が不正: %+v", first)
		}
		if first.OccurredAt().IsZero() {
			t.Fatal("OccurredAt が設定されていない")
		}

		// PullEvents 後は空になる。
		if remaining := item.PullEvents(); len(remaining) != 0 {
			t.Fatalf("PullEvents 後の残イベント = %d, want 0", len(remaining))
		}
	})

	t.Run("異常系: 補充数量 0 は ErrInvalidQuantity", func(t *testing.T) {
		item, _ := inventory.NewStockItem("id-1", mustSKU(t, "WIDGET-001"))
		if err := item.Replenish(mustQuantity(t, 0)); !errors.Is(err, inventory.ErrInvalidQuantity) {
			t.Fatalf("エラー = %v, want ErrInvalidQuantity", err)
		}
		// 失敗時はイベントも記録されない。
		if events := item.PullEvents(); len(events) != 0 {
			t.Fatalf("失敗時にイベントが記録された: %d", len(events))
		}
	})
}

func TestReconstituteAndMarkPersisted(t *testing.T) {
	item := inventory.ReconstituteStockItem("id-9", mustSKU(t, "GADGET-9"), mustQuantity(t, 42), 3)
	if item.Version() != 3 || item.Available().Int() != 42 {
		t.Fatalf("復元結果が不正: version=%d available=%d", item.Version(), item.Available().Int())
	}
	item.MarkPersisted(4)
	if item.Version() != 4 {
		t.Fatalf("MarkPersisted 後の Version = %d, want 4", item.Version())
	}
	// 復元では未発火イベントは無い。
	if events := item.PullEvents(); len(events) != 0 {
		t.Fatalf("復元直後のイベント = %d, want 0", len(events))
	}
}
