// Package memory はインメモリの送信アダプタ（outbound adapter＝被駆動側）を提供する。
// 送信アダプタはヘキサゴナルアーキテクチャの「出口」であり、application 層が定義した
// ポートを実装して外の世界（ここではメモリ上の記憶）へ書き出す。
//
// これはテスト用のモックではなく、application 層のポート（OrderStore、UnitOfWork、
// MessagePublisher）をきちんと実装した「本物のアダプタ」である。擬似トランザクションと
// 楽観的排他制御の版チェックを備えており、DB を用意しなくても ErrConcurrencyConflict や
// アウトボックスの同一トランザクション書き込みを再現できる。ドメイン層とアプリケーション層を
// DB 非依存で高速にテストするために使う。
//
// 集約ストアごとに 1 ファイル（<集約名>_store.go）を置き、そのファイルが「確定済みデータの
// 保持（<集約名>Rows）・トランザクション束縛のポート実装（tx<集約名>Store）・読み取り専用の
// ポート実装（read<集約名>Store）」の 3 つを束ねる。トランザクション機構そのもの
// （UnitOfWork / txState / txOutbox）は uow.go にある。
package memory

import (
	"context"
	"fmt"
	"sync"

	"github.com/example/go-ddd-template/contexts/ordering/internal/application"
	"github.com/example/go-ddd-template/contexts/ordering/internal/domain"
	"github.com/example/go-ddd-template/shared/uow"
)

// lineRow は確定済み（コミット済み）の注文明細行。
type lineRow struct {
	sku       string
	quantity  int
	unitPrice int64
	currency  string
}

// orderRecord は確定済み（コミット済み）の注文行（明細を含む）。
type orderRecord struct {
	id             string
	customerID     string
	status         string
	totalAmount    int64
	totalCurrency  string
	reservationRef string
	version        int
	lines          []lineRow
}

// OrderRows は確定済みの注文行を保持する backing store。並行アクセスを mutex で守る。
//
// **これは application.OrderStore ポートの実装ではない。** ポートを実装するのは下の
// txOrderStore（トランザクション束縛）と readOrderStore（読み取り専用）で、この型は
// その 2 つが読み書きする生データの入れ物である。だから Store ではなく Rows と名づける
// （orderRecord という「行」を保持するものが「行の集まり」という対応）。
type OrderRows struct {
	mu   sync.Mutex
	rows map[string]orderRecord // key: OrderID 文字列
}

// NewOrderRows は空の注文 backing store を生成する。
func NewOrderRows() *OrderRows {
	return &OrderRows{rows: make(map[string]orderRecord)}
}

// load は確定済みデータから注文を読み込み、集約を復元する。
func (r *OrderRows) load(id domain.OrderID) (*domain.Order, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	row, ok := r.rows[id.String()]
	if !ok {
		return nil, fmt.Errorf("注文 %q: %w", id.String(), domain.ErrOrderNotFound)
	}
	return recordToOrder(row)
}

// putLocked は確定済みデータへ 1 行を書き込む。**呼び出し側が mu を保持していること。**
// コミット時の適用（txState.commit）は backing store ごとに 1 回だけロックを取り、その内側で
// このメソッドを繰り返し呼ぶ（複数行の書き込みを不可分に確定させるため）。
func (r *OrderRows) putLocked(row orderRecord) {
	r.rows[row.id] = row
}

// parseOrderStatus は永続化された文字列を注文状態へ変換する。
func parseOrderStatus(s string) (domain.Status, error) {
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
func recordToOrder(r orderRecord) (*domain.Order, error) {
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

// orderToRecord は集約を、指定バージョンで確定行へ変換する（明細を含む）。
func orderToRecord(o *domain.Order, version int) orderRecord {
	lines := make([]lineRow, 0, len(o.Lines()))
	for _, l := range o.Lines() {
		lines = append(lines, lineRow{
			sku:       l.SKU().String(),
			quantity:  l.Quantity().Int(),
			unitPrice: l.UnitPrice().Amount(),
			currency:  l.UnitPrice().Currency(),
		})
	}
	return orderRecord{
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

// txOrderStore はトランザクションに束ねた OrderStore。
type txOrderStore struct {
	tx   *txState
	rows *OrderRows
}

// コンパイル時にポートを満たしていることを確認する。
// この表明は検査 14（集約ストア実装のファイル名）の判定根拠でもある。
var _ application.OrderStore = (*txOrderStore)(nil)

func (s *txOrderStore) Load(_ context.Context, id domain.OrderID) (*domain.Order, error) {
	return s.rows.load(id)
}

// Save は集約の版を確定ストアと突き合わせて検証し、集約のバージョンを同期（MarkPersisted）
// したうえで、確定ストアへ書き込む操作を staging に積む。実際の書き込みはコミット時に行う。
// 版が食い違えば uow.ErrConcurrencyConflict を返し、確定ストアは変更しない。
func (s *txOrderStore) Save(_ context.Context, o *domain.Order) error {
	s.rows.mu.Lock()
	defer s.rows.mu.Unlock()

	existing, ok := s.rows.rows[o.ID().String()]
	var next int
	if o.Version() == 0 {
		// 新規挿入。既に存在するなら衝突。
		if ok {
			return fmt.Errorf("注文 %q は既に存在します: %w", o.ID().String(), uow.ErrConcurrencyConflict)
		}
		next = 1
	} else {
		// 既存更新。存在しない、または版が食い違えば衝突。
		if !ok || existing.version != o.Version() {
			return fmt.Errorf("注文 %q のバージョンが一致しません: %w", o.ID().String(), uow.ErrConcurrencyConflict)
		}
		next = o.Version() + 1
	}

	row := orderToRecord(o, next)
	s.tx.stage(&s.rows.mu, func() { s.rows.putLocked(row) })
	o.MarkPersisted(next)
	return nil
}

// NewReadOrderStore は読み取り用の OrderStore を返す。注文照会ユースケース用。
// 書き込みには使わない。
func NewReadOrderStore(rows *OrderRows) application.OrderStore {
	return &readOrderStore{rows: rows}
}

// readOrderStore は確定済みデータを直接読む読み取り専用アダプタ。
type readOrderStore struct {
	rows *OrderRows
}

var _ application.OrderStore = (*readOrderStore)(nil)

func (s *readOrderStore) Load(_ context.Context, id domain.OrderID) (*domain.Order, error) {
	return s.rows.load(id)
}

// Save は読み取り専用アダプタでは使用しない。誤用を早期に検知するためエラーを返す。
func (s *readOrderStore) Save(_ context.Context, _ *domain.Order) error {
	return fmt.Errorf("readOrderStore は読み取り専用です: 書き込みは UnitOfWork.Within を使ってください")
}
