// 注文の識別子の往復（P-1 / R-1・R-2）と、Order 集約の状態機械（P-7 / R-23〜R-26）。
//
// このファイルには 2 種類の性質テストが並ぶ。
//
//   - 値オブジェクトの往復 — New と String が互いの逆であること、失敗側では番兵エラーと
//     ゼロ値が返ること。正と負は**対で 1 つの性質**である。正だけなら「常に成功する New」でも、
//     負だけなら「常に失敗する New」でも満たせてしまう。
//   - 集約の状態機械 — 任意の操作列に対する不変条件。Order は可変操作が Cancel の 1 つだけ
//     なので「軽い例」であり、単純さの代わりに**明細の構成を生成器で振って** Total() の
//     保存則に厚みを持たせる。重い例は在庫側の stock_item_property_test.go にある。
//
// 状態機械は rapid.Check + (*rapid.T).Repeat で書く（rapid.Run という関数は存在しない）。
// 不変条件は actions[""] に置く — ライブラリの doc が「executed before/after every other
// action invocation and should only contain invariant checking code」と定めている。

package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/example/go-ddd-template/contexts/ordering/internal/domain"
)

// maxOrderLines は生成する注文明細の最大行数。maxMoneyAmount × maxLineQuantity × この行数を
// 重ねても int64 に収まる範囲に留める（主張したいのは保存則であって整数型の限界ではない）。
const (
	maxOrderLines   = 8
	maxLineQuantity = 50
)

// newIdentifierGen は識別子の**成功側**の文字列生成器を返す。
//
// 非空かつ前後に空白を含まない形に絞る。New<T> は入力を TrimSpace してから包むので、
// 前後に空白のある文字列は往復しない（New(s).String() != s）。これは実装のバグではなく
// 「前後の空白は意味を持たない」という仕様なので、往復を主張する生成域から外す。
// 語中の空白は許す（TrimSpace が触らないため往復する）。
func newIdentifierGen() *rapid.Generator[string] {
	return rapid.StringMatching(`[0-9A-Za-z][0-9A-Za-z ._-]{0,14}[0-9A-Za-z]|[0-9A-Za-z]`)
}

// newBlankGen は識別子の**失敗側**の文字列生成器を返す（空文字と空白のみの文字列）。
func newBlankGen() *rapid.Generator[string] {
	return rapid.StringOfN(rapid.SampledFrom([]rune{' ', '\t', '\n', '\r'}), 0, 6, -1)
}

// TestOrderID_RoundTripsAndRejectsBlank は OrderID について R-1（正）と R-2（負）を主張する。
func TestOrderID_RoundTripsAndRejectsBlank(t *testing.T) {
	t.Parallel()

	t.Run("性質: 空白を前後に持たない非空文字列は New と String で往復する", func(t *testing.T) {
		t.Parallel()

		rapid.Check(t, func(t *rapid.T) {
			s := newIdentifierGen().Draw(t, "s")
			id, err := domain.NewOrderID(s)
			require.NoError(t, err, "非空文字列からは生成できる")
			assert.Equal(t, s, id.String(), "String は入力をそのまま返す")
		})
	})

	t.Run("性質: 空白のみの文字列は ErrInvalidOrderID とゼロ値を返す", func(t *testing.T) {
		t.Parallel()

		rapid.Check(t, func(t *rapid.T) {
			s := newBlankGen().Draw(t, "s")
			id, err := domain.NewOrderID(s)
			require.ErrorIs(t, err, domain.ErrInvalidOrderID, "空白のみは番兵で拒否される")
			assert.True(t, id.IsZero(), "失敗時の返り値はゼロ値")
		})
	})
}

// TestCustomerID_RoundTripsAndRejectsBlank は CustomerID について R-1（正）と R-2（負）を主張する。
func TestCustomerID_RoundTripsAndRejectsBlank(t *testing.T) {
	t.Parallel()

	t.Run("性質: 空白を前後に持たない非空文字列は New と String で往復する", func(t *testing.T) {
		t.Parallel()

		rapid.Check(t, func(t *rapid.T) {
			s := newIdentifierGen().Draw(t, "s")
			id, err := domain.NewCustomerID(s)
			require.NoError(t, err, "非空文字列からは生成できる")
			assert.Equal(t, s, id.String(), "String は入力をそのまま返す")
		})
	})

	t.Run("性質: 空白のみの文字列は ErrInvalidCustomerID とゼロ値を返す", func(t *testing.T) {
		t.Parallel()

		rapid.Check(t, func(t *rapid.T) {
			s := newBlankGen().Draw(t, "s")
			id, err := domain.NewCustomerID(s)
			require.ErrorIs(t, err, domain.ErrInvalidCustomerID, "空白のみは番兵で拒否される")
			// CustomerID は IsZero を持たないので、比較可能な構造体としてゼロ値と突き合わせる。
			assert.Equal(t, domain.CustomerID{}, id, "失敗時の返り値はゼロ値")
		})
	})
}

// TestReservationRef_RoundTripsAndRejectsBlank は注文コンテキストの ReservationRef について
// R-1（正）と R-2（負）を主張する。
//
// 在庫コンテキストにも同名の型があるが**別パッケージの別の型**であり、性質テストを共有しない
// （contexts/inventory/internal/domain/reservation_property_test.go に独立に書いてある）。
// この重複は冗長ではなく、「値オブジェクトはコンテキストごとに所有する」ことの表れである。
func TestReservationRef_RoundTripsAndRejectsBlank(t *testing.T) {
	t.Parallel()

	t.Run("性質: 空白を前後に持たない非空文字列は New と String で往復する", func(t *testing.T) {
		t.Parallel()

		rapid.Check(t, func(t *rapid.T) {
			s := newIdentifierGen().Draw(t, "s")
			ref, err := domain.NewReservationRef(s)
			require.NoError(t, err, "非空文字列からは生成できる")
			assert.Equal(t, s, ref.String(), "String は入力をそのまま返す")
		})
	})

	t.Run("性質: 空白のみの文字列は ErrInvalidReservationRef とゼロ値を返す", func(t *testing.T) {
		t.Parallel()

		rapid.Check(t, func(t *rapid.T) {
			s := newBlankGen().Draw(t, "s")
			ref, err := domain.NewReservationRef(s)
			require.ErrorIs(t, err, domain.ErrInvalidReservationRef, "空白のみは番兵で拒否される")
			assert.True(t, ref.IsZero(), "失敗時の返り値はゼロ値")
		})
	})
}

// newLineGen は通貨を固定した注文明細の生成器を返す。
func newLineGen(currency string) *rapid.Generator[domain.OrderLine] {
	return rapid.Custom(func(t *rapid.T) domain.OrderLine {
		sku, err := domain.NewSKU(newIdentifierGen().Draw(t, "sku"))
		require.NoError(t, err, "生成器が不正な SKU を作った")
		qty, err := domain.NewQuantity(rapid.IntRange(1, maxLineQuantity).Draw(t, "quantity"))
		require.NoError(t, err, "生成器が不正な Quantity を作った")
		return domain.NewOrderLine(sku, qty, newMoneyGen(currency).Draw(t, "unitPrice"))
	})
}

// newOrderGen は 1 行以上・通貨の揃った明細から作った Order の生成器を返す。
func newOrderGen(currency string) *rapid.Generator[*domain.Order] {
	return rapid.Custom(func(t *rapid.T) *domain.Order {
		id, err := domain.NewOrderID(newIdentifierGen().Draw(t, "orderID"))
		require.NoError(t, err, "生成器が不正な OrderID を作った")
		customer, err := domain.NewCustomerID(newIdentifierGen().Draw(t, "customerID"))
		require.NoError(t, err, "生成器が不正な CustomerID を作った")
		lines := rapid.SliceOfN(newLineGen(currency), 1, maxOrderLines).Draw(t, "lines")
		order, err := domain.NewOrder(id, customer, lines)
		require.NoError(t, err, "1 行以上・通貨の揃った明細からは注文を作れる")
		return order
	})
}

// TestOrder_CancelStateMachine は Order 集約の状態機械であり、R-23（遷移の合法性）と
// R-24（Total の保存則）を主張する。
//
// 可変操作は Cancel の 1 つだけなので、状態機械としては最小である。**操作が 1 つの集約にも
// 状態機械テストは書ける**ことを示す軽い例として意図的に残してあり、単純さの代わりに
// 明細の構成（行数・SKU・数量・単価）を生成器で振って Total() の保存則に厚みを持たせている。
func TestOrder_CancelStateMachine(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		currency := rapid.SampledFrom(currencies).Draw(t, "currency")
		order := newOrderGen(currency).Draw(t, "order")

		// 期待する合計を**集約の外**で独立に組み立てる。Money.Add で足し込むと Total() の
		// 実装と同じ経路をなぞるだけになり、両方が同時に壊れると気づけない。
		var wantAmount int64
		for _, l := range order.Lines() {
			wantAmount += l.UnitPrice().Amount() * int64(l.Quantity().Int())
		}

		require.Equal(t, domain.StatusConfirmed, order.Status(), "NewOrder は Confirmed で始まる")
		cancelled := false

		t.Repeat(map[string]func(*rapid.T){
			// 不変条件のみ。各アクションの前後で毎回実行される。
			"": func(t *rapid.T) {
				// R-24: Total() は明細小計の総和に一致し、Cancel() では変化しない。
				assert.Equal(t, wantAmount, order.Total().Amount(), "Total() は明細小計の総和")
				assert.Equal(t, currency, order.Total().Currency(), "Total() の通貨は明細の通貨")
			},
			"cancel": func(t *rapid.T) {
				err := order.Cancel()
				if cancelled {
					// R-23（負）: 2 回目以降は Confirmed でないので拒否される。
					require.ErrorIs(t, err, domain.ErrOrderNotConfirmed, "2 回目以降の Cancel は拒否される")
					assert.Equal(t, domain.StatusCancelled, order.Status(), "拒否された Cancel は状態を変えない")
					return
				}
				// R-23（正）: 1 回目は成功して Cancelled へ遷移する。
				require.NoError(t, err, "Confirmed からの 1 回目の Cancel は成功する")
				assert.Equal(t, domain.StatusCancelled, order.Status(), "Cancel は Cancelled へ遷移させる")
				cancelled = true
			},
		})
	})
}

// TestOrder_ConstructionRejectsEmptyLines は R-25（明細は 1 行以上）を主張する。
func TestOrder_ConstructionRejectsEmptyLines(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		id, err := domain.NewOrderID(newIdentifierGen().Draw(t, "orderID"))
		require.NoError(t, err, "生成器が不正な OrderID を作った")
		customer, err := domain.NewCustomerID(newIdentifierGen().Draw(t, "customerID"))
		require.NoError(t, err, "生成器が不正な CustomerID を作った")

		// nil スライスと長さ 0 のスライスは Go では別物なので、両方を引く。
		var lines []domain.OrderLine
		if rapid.Bool().Draw(t, "emptySlice") {
			lines = []domain.OrderLine{}
		}

		order, err := domain.NewOrder(id, customer, lines)
		require.ErrorIs(t, err, domain.ErrEmptyOrder, "空の明細は ErrEmptyOrder")
		assert.Nil(t, order, "失敗時は集約を返さない")
	})
}

// TestOrder_ConstructionRejectsMixedCurrency は R-26（明細間の通貨の一貫性）を主張する。
func TestOrder_ConstructionRejectsMixedCurrency(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		curA, curB := newDistinctCurrencies(t)
		id, err := domain.NewOrderID(newIdentifierGen().Draw(t, "orderID"))
		require.NoError(t, err, "生成器が不正な OrderID を作った")
		customer, err := domain.NewCustomerID(newIdentifierGen().Draw(t, "customerID"))
		require.NoError(t, err, "生成器が不正な CustomerID を作った")

		// 各通貨を 1 行以上含む明細を作り、並び順も振る。先頭がどちらの通貨でも拒否される
		// ことを見たいので、順序を固定すると片方の経路しか踏まない。
		lines := rapid.SliceOfN(newLineGen(curA), 1, maxOrderLines).Draw(t, "linesA")
		lines = append(lines, rapid.SliceOfN(newLineGen(curB), 1, maxOrderLines).Draw(t, "linesB")...)
		lines = rapid.Permutation(lines).Draw(t, "lines")

		order, err := domain.NewOrder(id, customer, lines)
		require.ErrorIs(t, err, domain.ErrInvalidMoney, "通貨が食い違う明細は ErrInvalidMoney")
		assert.Nil(t, order, "失敗時は集約を返さない")
	})
}
