package order_test

import (
	"errors"
	"testing"

	"github.com/example/go-ddd-template/contexts/ordering/internal/domain/order"
)

func TestNewQuantity(t *testing.T) {
	t.Run("正常系: 1 以上は生成できる", func(t *testing.T) {
		for _, n := range []int{1, 2, 100} {
			q, err := order.NewQuantity(n)
			if err != nil {
				t.Fatalf("NewQuantity(%d) 想定外のエラー: %v", n, err)
			}
			if q.Int() != n {
				t.Fatalf("Int() = %d, want %d", q.Int(), n)
			}
		}
	})

	t.Run("異常系: 0 以下は ErrInvalidQuantity（注文行数量は n >= 1）", func(t *testing.T) {
		for _, n := range []int{0, -1} {
			if _, err := order.NewQuantity(n); !errors.Is(err, order.ErrInvalidQuantity) {
				t.Fatalf("NewQuantity(%d) のエラー = %v, want ErrInvalidQuantity", n, err)
			}
		}
	})
}

func TestNewMoney(t *testing.T) {
	t.Run("正常系: 非負金額と非空通貨で生成できる", func(t *testing.T) {
		m, err := order.NewMoney(0, "JPY")
		if err != nil {
			t.Fatalf("想定外のエラー: %v", err)
		}
		if m.Amount() != 0 || m.Currency() != "JPY" {
			t.Fatalf("Money = %+v, want 0 JPY", m)
		}
	})

	t.Run("異常系: 負数は ErrInvalidMoney", func(t *testing.T) {
		if _, err := order.NewMoney(-1, "JPY"); !errors.Is(err, order.ErrInvalidMoney) {
			t.Fatalf("エラー = %v, want ErrInvalidMoney", err)
		}
	})

	t.Run("異常系: 空通貨は ErrInvalidMoney", func(t *testing.T) {
		if _, err := order.NewMoney(100, "  "); !errors.Is(err, order.ErrInvalidMoney) {
			t.Fatalf("エラー = %v, want ErrInvalidMoney", err)
		}
	})

	t.Run("Add: ゼロ値は加法の単位元", func(t *testing.T) {
		a, _ := order.NewMoney(300, "JPY")
		sum, err := (order.Money{}).Add(a)
		if err != nil {
			t.Fatalf("想定外のエラー: %v", err)
		}
		if sum.Amount() != 300 || sum.Currency() != "JPY" {
			t.Fatalf("Add 結果 = %+v, want 300 JPY", sum)
		}
	})

	t.Run("Add: 通貨不一致は ErrInvalidMoney", func(t *testing.T) {
		a, _ := order.NewMoney(300, "JPY")
		b, _ := order.NewMoney(5, "USD")
		if _, err := a.Add(b); !errors.Is(err, order.ErrInvalidMoney) {
			t.Fatalf("エラー = %v, want ErrInvalidMoney", err)
		}
	})

	t.Run("Mul: 単価 × 数量", func(t *testing.T) {
		a, _ := order.NewMoney(1200, "JPY")
		got := a.Mul(3)
		if got.Amount() != 3600 || got.Currency() != "JPY" {
			t.Fatalf("Mul 結果 = %+v, want 3600 JPY", got)
		}
	})
}

func TestNewSKU(t *testing.T) {
	t.Run("正常系: 空白を取り除いた値で生成できる", func(t *testing.T) {
		sku, err := order.NewSKU("  WIDGET-001  ")
		if err != nil {
			t.Fatalf("想定外のエラー: %v", err)
		}
		if sku.String() != "WIDGET-001" {
			t.Fatalf("String() = %q, want %q", sku.String(), "WIDGET-001")
		}
	})

	t.Run("異常系: 空文字は ErrInvalidSKU", func(t *testing.T) {
		if _, err := order.NewSKU("   "); !errors.Is(err, order.ErrInvalidSKU) {
			t.Fatalf("エラー = %v, want ErrInvalidSKU", err)
		}
	})
}

func TestDeriveReservationRef(t *testing.T) {
	id, err := order.NewOrderID("ORDER-xyz")
	if err != nil {
		t.Fatalf("OrderID 生成失敗: %v", err)
	}
	// 決定的: 同一注文 ID からは常に同一の予約参照が導出される。
	r1 := order.DeriveReservationRef(id)
	r2 := order.DeriveReservationRef(id)
	if r1.String() != r2.String() {
		t.Fatalf("導出が非決定的: %q vs %q", r1.String(), r2.String())
	}
	if r1.String() != id.String() {
		t.Fatalf("ReservationRef = %q, want %q", r1.String(), id.String())
	}
}
