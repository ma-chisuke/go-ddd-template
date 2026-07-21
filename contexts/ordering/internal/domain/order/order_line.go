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
