// Package memory はインメモリの送信アダプタ（outbound adapter＝被駆動側）を提供する。
// 送信アダプタはヘキサゴナルアーキテクチャの「出口」であり、application 層が定義した
// ポートを実装して外の世界（ここではメモリ上の記憶）へ書き出す。
//
// これはテスト用のモックではなく、application 層のポート（StockStore、UnitOfWork、
// MessagePublisher）をきちんと実装した「本物のアダプタ」である。擬似トランザクションと
// 楽観的排他制御の版チェックを備えており、DB を用意しなくても ErrConcurrencyConflict や
// アウトボックスの同一トランザクション書き込みを再現できる。ドメイン層とアプリケーション層を
// DB 非依存で高速にテストするために使う。
package memory

import (
	"fmt"
	"sync"
	"time"

	"github.com/example/go-ddd-template/contexts/inventory/internal/domain"
)

// reservationRow は確定済み（コミット済み）の予約行。
type reservationRow struct {
	ref       string
	quantity  int
	status    domain.ReservationStatus
	expiresAt time.Time
}

// stockRow は確定済み（コミット済み）の在庫行（予約を含む）。
type stockRow struct {
	id           string
	sku          string
	available    int
	version      int
	reservations []reservationRow
}

// StockRows は在庫項目集約の確定済み行を保持するインメモリの backing store。
// 並行アクセスを mutex で守る。
//
// Store の語をこの型に使わないのは規約である（CONVENTIONS.md）。Store は集約ストアの
// ポート（application.StockStore）とその実装（stockStore / readStockStore）だけが名乗り、
// 行を溜めておく容れ物は <X>Rows と名づける。<X> はポート名の語幹であって集約ルートの
// 型名ではないため、StockItem 集約の backing store は StockItemRows ではなく StockRows である。
type StockRows struct {
	mu   sync.Mutex
	rows map[string]stockRow // key: SKU 文字列
}

// NewStockRows は空の在庫行 backing store を生成する。
func NewStockRows() *StockRows {
	return &StockRows{rows: make(map[string]stockRow)}
}

// stockRowToStockItem は確定済みの行から集約を復元する。
func stockRowToStockItem(r stockRow) (*domain.StockItem, error) {
	qty, err := domain.NewQuantity(r.available)
	if err != nil {
		return nil, fmt.Errorf("永続化された数量が不正です（SKU=%q）: %w", r.sku, err)
	}
	loadedSKU, err := domain.NewSKU(r.sku)
	if err != nil {
		return nil, fmt.Errorf("永続化された SKU が不正です: %w", err)
	}
	reservations := make([]domain.Reservation, 0, len(r.reservations))
	for _, rr := range r.reservations {
		ref, err := domain.NewReservationRef(rr.ref)
		if err != nil {
			return nil, fmt.Errorf("永続化された予約参照が不正です: %w", err)
		}
		rq, err := domain.NewQuantity(rr.quantity)
		if err != nil {
			return nil, fmt.Errorf("永続化された予約数量が不正です: %w", err)
		}
		reservations = append(reservations, domain.ReconstituteReservation(ref, rq, rr.status, rr.expiresAt))
	}
	return domain.ReconstituteStockItem(r.id, loadedSKU, qty, r.version, reservations), nil
}

// load は確定済みデータから在庫項目を読み込み、集約を復元する。
func (s *StockRows) load(sku domain.SKU) (*domain.StockItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	r, ok := s.rows[sku.String()]
	if !ok {
		return nil, fmt.Errorf("SKU %q: %w", sku.String(), domain.ErrStockItemNotFound)
	}
	return stockRowToStockItem(r)
}

// loadMany は複数 SKU をまとめて読み込む。見つからない SKU は黙って除外する
// （存在検査はドメインサービス側の事前検証が担う）。
func (s *StockRows) loadMany(skus []domain.SKU) ([]*domain.StockItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	items := make([]*domain.StockItem, 0, len(skus))
	for _, sku := range skus {
		r, ok := s.rows[sku.String()]
		if !ok {
			continue
		}
		item, err := stockRowToStockItem(r)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

// loadByReservation は指定参照を持つ全ての在庫項目を読み込む。
func (s *StockRows) loadByReservation(ref domain.ReservationRef) ([]*domain.StockItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var items []*domain.StockItem
	for _, r := range s.rows {
		if !stockRowHasReservation(r, ref.String()) {
			continue
		}
		item, err := stockRowToStockItem(r)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

// loadExpiredPending は before 時点で期限切れの pending 予約を持つ在庫項目を最大 limit 件返す。
func (s *StockRows) loadExpiredPending(before time.Time, limit int) ([]*domain.StockItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var items []*domain.StockItem
	for _, r := range s.rows {
		if !stockRowHasExpiredPending(r, before) {
			continue
		}
		item, err := stockRowToStockItem(r)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
		if limit > 0 && len(items) >= limit {
			break
		}
	}
	return items, nil
}

func stockRowHasReservation(r stockRow, ref string) bool {
	for _, rr := range r.reservations {
		if rr.ref == ref {
			return true
		}
	}
	return false
}

func stockRowHasExpiredPending(r stockRow, before time.Time) bool {
	for _, rr := range r.reservations {
		if rr.status == domain.ReservationPending && !rr.expiresAt.IsZero() && !before.Before(rr.expiresAt) {
			return true
		}
	}
	return false
}

// stockItemToRow は集約を、指定バージョンで確定行へ変換する（予約状態を含む）。
func stockItemToRow(item *domain.StockItem, version int) stockRow {
	res := item.Reservations()
	resRows := make([]reservationRow, 0, len(res))
	for _, r := range res {
		resRows = append(resRows, reservationRow{
			ref:       r.Ref().String(),
			quantity:  r.Quantity().Int(),
			status:    r.Status(),
			expiresAt: r.ExpiresAt(),
		})
	}
	return stockRow{
		id:           item.ID(),
		sku:          item.SKU().String(),
		available:    item.Available().Int(),
		version:      version,
		reservations: resRows,
	}
}
