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
	"sync"

	"github.com/example/go-ddd-template/contexts/ordering/internal/domain"
)

// lineRow は確定済み（コミット済み）の注文明細行。
type lineRow struct {
	sku       string
	quantity  int
	unitPrice int64
	currency  string
}

// record は確定済み（コミット済み）の注文行（明細を含む）。
type record struct {
	id             string
	customerID     string
	status         string
	totalAmount    int64
	totalCurrency  string
	reservationRef string
	version        int
	lines          []lineRow
}

// Store はインメモリの確定済みデータを保持する。並行アクセスを mutex で守る。
type Store struct {
	mu   sync.Mutex
	rows map[string]record // key: OrderID 文字列
}

// NewStore は空の注文ストアを生成する。
func NewStore() *Store {
	return &Store{rows: make(map[string]record)}
}

// load は確定済みデータから注文を読み込み、集約を復元する。
func (s *Store) load(id domain.OrderID) (*domain.Order, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	r, ok := s.rows[id.String()]
	if !ok {
		return nil, fmt.Errorf("注文 %q: %w", id.String(), domain.ErrOrderNotFound)
	}
	return recordToOrder(r)
}

// parseStatus は永続化された文字列を注文状態へ変換する。
func parseStatus(s string) (domain.Status, error) {
	switch s {
	case "confirmed":
		return domain.StatusConfirmed, nil
	case "cancelled":
		return domain.StatusCancelled, nil
	default:
		return domain.StatusConfirmed, fmt.Errorf("永続化された注文状態が不正です: %q", s)
	}
}

// recordToOrder は確定済みの行から集約を復元する。
func recordToOrder(r record) (*domain.Order, error) {
	orderID, err := domain.NewOrderID(r.id)
	if err != nil {
		return nil, fmt.Errorf("永続化された注文 ID が不正です: %w", err)
	}
	customer, err := domain.NewCustomerID(r.customerID)
	if err != nil {
		return nil, fmt.Errorf("永続化された顧客 ID が不正です: %w", err)
	}
	status, err := parseStatus(r.status)
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

// orderToRecord は集約を、指定バージョンで確定行へ変換する（明細を含む）。
func orderToRecord(o *domain.Order, version int) record {
	lines := make([]lineRow, 0, len(o.Lines()))
	for _, l := range o.Lines() {
		lines = append(lines, lineRow{
			sku:       l.SKU().String(),
			quantity:  l.Quantity().Int(),
			unitPrice: l.UnitPrice().Amount(),
			currency:  l.UnitPrice().Currency(),
		})
	}
	return record{
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
