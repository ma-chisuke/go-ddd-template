// 注文明細と、その数量。
//
// 束ね方の根拠は CONVENTIONS.md の B-3 則 2（小さな値オブジェクトは、それを最も使う型の
// ファイルに同居させる）である。Quantity は 28 行で OrderLine 以外から使われず、
// 明細を読むときに必ず一緒に読む。
//
// なお在庫コンテキストの Quantity は 55 行あるので quantity.go に単独で残る（則 3）。
// 同じ規則に異なる入力を通した結果であり、非対称は意図的である。

package order

// OrderLine は注文明細の 1 行を表す。集約 Order の内部に保持される子要素であり、
// SKU・数量・単価の対で構成される。値はすべて検証済みの値オブジェクトなので、
// 生成に失敗はない（不正値は各値オブジェクトの生成時点で弾かれている）。
type OrderLine struct {
	sku       SKU
	quantity  Quantity
	unitPrice Money
}

// NewOrderLine は検証済みの値オブジェクトから注文明細を組み立てる。
func NewOrderLine(sku SKU, quantity Quantity, unitPrice Money) OrderLine {
	return OrderLine{sku: sku, quantity: quantity, unitPrice: unitPrice}
}

// SKU は明細の SKU を返す。
func (l OrderLine) SKU() SKU {
	return l.sku
}

// Quantity は明細の数量を返す。
func (l OrderLine) Quantity() Quantity {
	return l.quantity
}

// UnitPrice は明細の単価を返す。
func (l OrderLine) UnitPrice() Money {
	return l.unitPrice
}

// Subtotal は明細の小計（単価 × 数量）を返す。
func (l OrderLine) Subtotal() Money {
	return l.unitPrice.Mul(l.quantity.Int())
}

// Quantity は注文行の数量を表す値オブジェクト。不変であり、生成後は値を変更できない。
//
// 重要（コンテキストごと VO の教材ポイント）: 注文コンテキストの Quantity は
// **1 以上（n >= 1）** を値域とする。注文行に数量 0 は無いからである。これは在庫
// コンテキストの Quantity（利用可能在庫 available を扱うため n >= 0 を許容する）とは
// 意図的に異なる制約であり、各コンテキストが自分のドメイン規則に合わせて Quantity を
// 独立に定義することを示す。同名でも別コンテキストの別型であり、共有しない。腐敗防止層が
// 境界でこれらを翻訳する。
type Quantity struct {
	value int
}

// NewQuantity は 1 以上であることを検証して Quantity を生成する。
// 0 以下の場合は ErrInvalidQuantity を包んだ FieldViolation を返す
// （errors.Is(err, ErrInvalidQuantity) は従来どおり真になる — 規則 R-15）。
func NewQuantity(n int) (Quantity, error) {
	if n < 1 {
		return Quantity{}, VQuantity.Violated("注文行の数量は 1 以上でなければなりません（指定値: %d）", n)
	}
	return Quantity{value: n}, nil
}

// Int は数量を int で返す。
func (q Quantity) Int() int {
	return q.value
}
