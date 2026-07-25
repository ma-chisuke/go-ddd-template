package order

import (
	"fmt"
	"strings"
)

// Money は金額を表す値オブジェクト。最小通貨単位（例 円・セント）の整数 amount と、
// ISO-4217 の通貨コード currency の対で表す。不変であり、生成後は値を変更できない。
//
// 規則:
//   - amount は非負（>= 0）。
//   - currency は非空。
//
// ゼロ値 Money{}（amount 0・通貨空）は「加法の単位元」として扱う。注文合計を行ごとに
// 足し込む際、初期値としてゼロ値から始められるようにするためである（下記 Add 参照）。
type Money struct {
	amount   int64
	currency string
}

// NewMoney は金額を検証して生成する。amount が負なら、または通貨が空なら
// ErrInvalidMoney を包んだ FieldViolation を返す。
//
// 重要（FR-4.3 の中核）: 番兵は双方とも ErrInvalidMoney で同一のため、番兵だけでは
// 「金額が悪いのか通貨が悪いのか」をリクエスターに言い分けられない。同じ番兵を指す
// 2 つの Rule（VMoneyAmount / VMoneyCurrency）に分けることで、初めて
// unitPrice.amount と unitPrice.currency を区別できる。Rule は番兵より細かくてよい
// （errors.Is の判定単位は 1 つのままでよい。規則 R-6）。
func NewMoney(amount int64, currency string) (Money, error) {
	if amount < 0 {
		return Money{}, VMoneyAmount.Violated("金額は 0 以上でなければなりません（指定値: %d）", amount)
	}
	cur := strings.TrimSpace(currency)
	if cur == "" {
		return Money{}, VMoneyCurrency.Violated("通貨は空にできません")
	}
	return Money{amount: amount, currency: cur}, nil
}

// Amount は最小通貨単位での金額を返す。
func (m Money) Amount() int64 {
	return m.amount
}

// Currency は通貨コードを返す。
func (m Money) Currency() string {
	return m.currency
}

// IsZero は加法の単位元（未設定のゼロ値）かどうかを返す。
func (m Money) IsZero() bool {
	return m.amount == 0 && m.currency == ""
}

// Mul は金額を非負の係数 n 倍した金額を返す（通貨は不変）。
// 注文行の小計（単価 × 数量）を求めるのに使う。n は数量なので 1 以上を想定する。
func (m Money) Mul(n int) Money {
	return Money{amount: m.amount * int64(n), currency: m.currency}
}

// Add は 2 つの金額の和を返す。いずれかがゼロ値なら他方をそのまま返す（単位元）。
// 双方が非ゼロで通貨が異なる場合は ErrInvalidMoney を返す（通貨をまたいだ加算は不正）。
//
// ここは FieldViolation にしない。通貨不一致は「2 つの明細行の通貨が食い違う」という
// 集約レベルの矛盾であり、単一の入力フィールドに帰着しないためである。この場合は
// invalid-params をキーごと省略する（規則 R-14 / FR-3.4）。「違反フィールドが 0 件」と
// 「特定できなかった」を区別できるようにするため、空配列は返さない。
func (m Money) Add(other Money) (Money, error) {
	if m.IsZero() {
		return other, nil
	}
	if other.IsZero() {
		return m, nil
	}
	if m.currency != other.currency {
		return Money{}, fmt.Errorf("通貨が一致しません（%q と %q）: %w", m.currency, other.currency, ErrInvalidMoney)
	}
	return Money{amount: m.amount + other.amount, currency: m.currency}, nil
}
