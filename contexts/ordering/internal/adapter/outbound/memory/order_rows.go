// Package memory はインメモリの送信アダプタ（outbound adapter＝被駆動側）を提供する。
// 送信アダプタはヘキサゴナルアーキテクチャの「出口」であり、application 層が定義した
// ポートを実装して外の世界（ここではメモリ上の記憶）へ書き出す。
//
// これはテスト用のモックではなく、application 層のポート（OrderStore、UnitOfWork、
// MessagePublisher）をきちんと実装した「本物のアダプタ」である。擬似トランザクションと
// 楽観的排他制御の版チェックを備えており、DB を用意しなくても ErrConcurrencyConflict や
// アウトボックスの同一トランザクション書き込みを再現できる。ドメイン層とアプリケーション層を
// DB 非依存で高速にテストするために使う。
package memory

import (
	"fmt"

	"github.com/example/go-ddd-template/contexts/ordering/internal/domain"
)

// lineRow は確定済み（コミット済み）の注文明細行。
type lineRow struct {
	sku       string
	quantity  int
	unitPrice int64
	currency  string
}

// orderRow は確定済み（コミット済み）の注文行（明細を含む）。
type orderRow struct {
	id             string
	customerID     string
	status         string
	totalAmount    int64
	totalCurrency  string
	reservationRef string
	version        int
	lines          []lineRow
}

// OrderRows は注文集約の確定済み行（key: OrderID 文字列）を保持するインメモリの
// backing store。共通機構 rows[R]（rows.go）を orderRow で特殊化したものである。
//
// 型エイリアス（= ）であって定義型ではない。Go の定義型は基底型のメソッドを継承しないため、
// = を落とすと get / withLock / applyGroup がすべて消える。同一パッケージの Stores も
// 同じ手法（shared/outbox/memory.Stores への別名）を採っており、役割の同じ型に同じ手法を
// 使うほうが読者に一貫して見える。
//
// Store の語をこの型に使わないのは規約である（CONVENTIONS.md）。Store は集約ストアの
// ポート（application.OrderStore）とその実装（orderStore / readOrderStore）だけが名乗り、
// 行を溜めておく容れ物は <集約名>Rows と名づける。
type OrderRows = rows[orderRow]

// NewOrderRows は空の注文行 backing store を生成する。
func NewOrderRows() *OrderRows {
	return &rows[orderRow]{m: make(map[string]orderRow)}
}

// parseOrderStatus は永続化された文字列を注文状態へ変換する。
func parseOrderStatus(s string) (domain.OrderStatus, error) {
	switch s {
	case "confirmed":
		return domain.OrderStatusConfirmed, nil
	case "cancelled":
		return domain.OrderStatusCancelled, nil
	default:
		return domain.OrderStatusConfirmed, fmt.Errorf("永続化された注文状態が不正です: %q", s)
	}
}

// orderRowToOrder は確定済みの行から集約を復元する。
func orderRowToOrder(r orderRow) (*domain.Order, error) {
	orderID, err := domain.NewOrderID(r.id)
	if err != nil {
		return nil, fmt.Errorf("永続化された注文 ID が不正です: %w", err)
	}
	customer, err := domain.NewCustomerID(r.customerID)
	if err != nil {
		return nil, fmt.Errorf("永続化された顧客 ID が不正です: %w", err)
	}
	status, err := parseOrderStatus(r.status)
	if err != nil {
		return nil, err
	}
	total, err := domain.NewMoney(r.totalAmount, r.totalCurrency)
	if err != nil {
		return nil, fmt.Errorf("永続化された合計金額が不正です: %w", err)
	}
	ref, err := domain.NewReservationRef(r.reservationRef)
	if err != nil {
		return nil, fmt.Errorf("永続化された予約参照が不正です: %w", err)
	}
	lines := make([]domain.OrderLine, 0, len(r.lines))
	for _, lr := range r.lines {
		sku, err := domain.NewSKU(lr.sku)
		if err != nil {
			return nil, fmt.Errorf("永続化された SKU が不正です: %w", err)
		}
		qty, err := domain.NewQuantity(lr.quantity)
		if err != nil {
			return nil, fmt.Errorf("永続化された数量が不正です: %w", err)
		}
		price, err := domain.NewMoney(lr.unitPrice, lr.currency)
		if err != nil {
			return nil, fmt.Errorf("永続化された単価が不正です: %w", err)
		}
		lines = append(lines, domain.NewOrderLine(sku, qty, price))
	}
	return domain.ReconstituteOrder(orderID, customer, lines, status, total, ref, r.version), nil
}

// orderToOrderRow は集約を、指定バージョンで確定行へ変換する（明細を含む）。
func orderToOrderRow(o *domain.Order, version int) orderRow {
	lines := make([]lineRow, 0, len(o.Lines()))
	for _, l := range o.Lines() {
		lines = append(lines, lineRow{
			sku:       l.SKU().String(),
			quantity:  l.Quantity().Int(),
			unitPrice: l.UnitPrice().Amount(),
			currency:  l.UnitPrice().Currency(),
		})
	}
	return orderRow{
		id:             o.ID().String(),
		customerID:     o.CustomerID().String(),
		status:         o.Status().String(),
		totalAmount:    o.Total().Amount(),
		totalCurrency:  o.Total().Currency(),
		reservationRef: o.ReservationRef().String(),
		version:        version,
		lines:          lines,
	}
}
