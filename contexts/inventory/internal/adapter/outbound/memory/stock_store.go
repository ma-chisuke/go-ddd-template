// Package memory はインメモリの送信アダプタ（outbound adapter＝被駆動側）を提供する。
// 送信アダプタはヘキサゴナルアーキテクチャの「出口」であり、application 層が定義した
// ポートを実装して外の世界（ここではメモリ上の記憶）へ書き出す。
//
// これはテスト用のモックではなく、application 層のポート（StockStore、UnitOfWork、
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
	"time"

	"github.com/example/go-ddd-template/contexts/inventory/internal/application"
	"github.com/example/go-ddd-template/contexts/inventory/internal/domain"
	"github.com/example/go-ddd-template/shared/uow"
)

// reservationRow は確定済み（コミット済み）の予約行。
type reservationRow struct {
	ref       string
	quantity  int
	status    domain.ReservationStatus
	expiresAt time.Time
}

// stockItemRecord は確定済み（コミット済み）の在庫行（予約を含む）。
type stockItemRecord struct {
	id           string
	sku          string
	available    int
	version      int
	reservations []reservationRow
}

// StockItemRows は確定済みの在庫行を保持する backing store。並行アクセスを mutex で守る。
//
// **これは application.StockStore ポートの実装ではない。** ポートを実装するのは下の
// txStockStore（トランザクション束縛）と readStockStore（読み取り専用）で、この型は
// その 2 つが読み書きする生データの入れ物である。だから Store ではなく Rows と名づける
// （stockItemRecord という「行」を保持するものが「行の集まり」という対応）。
type StockItemRows struct {
	mu   sync.Mutex
	rows map[string]stockItemRecord // key: SKU 文字列
}

// NewStockItemRows は空の在庫 backing store を生成する。
func NewStockItemRows() *StockItemRows {
	return &StockItemRows{rows: make(map[string]stockItemRecord)}
}

// putLocked は確定済みデータへ 1 行を書き込む。**呼び出し側が mu を保持していること。**
// コミット時の適用（txState.commit）は backing store ごとに 1 回だけロックを取り、その内側で
// このメソッドを繰り返し呼ぶ（複数行の書き込みを不可分に確定させるため）。マルチ SKU 予約で
// 複数行が 1 回の Save で保存される経路が、この不可分性の直撃点である。
func (r *StockItemRows) putLocked(row stockItemRecord) {
	r.rows[row.sku] = row
}

// recordToStockItem は確定済みの行から集約を復元する。
func recordToStockItem(r stockItemRecord) (*domain.StockItem, error) {
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
func (r *StockItemRows) load(sku domain.SKU) (*domain.StockItem, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	row, ok := r.rows[sku.String()]
	if !ok {
		return nil, fmt.Errorf("SKU %q: %w", sku.String(), domain.ErrStockItemNotFound)
	}
	return recordToStockItem(row)
}

// loadMany は複数 SKU をまとめて読み込む。見つからない SKU は黙って除外する
// （存在検査はドメインサービス側の事前検証が担う）。
func (r *StockItemRows) loadMany(skus []domain.SKU) ([]*domain.StockItem, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	items := make([]*domain.StockItem, 0, len(skus))
	for _, sku := range skus {
		row, ok := r.rows[sku.String()]
		if !ok {
			continue
		}
		item, err := recordToStockItem(row)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

// loadByReservation は指定参照を持つ全ての在庫項目を読み込む。
func (r *StockItemRows) loadByReservation(ref domain.ReservationRef) ([]*domain.StockItem, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var items []*domain.StockItem
	for _, row := range r.rows {
		if !recordHasReservation(row, ref.String()) {
			continue
		}
		item, err := recordToStockItem(row)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

// loadExpiredPending は before 時点で期限切れの pending 予約を持つ在庫項目を最大 limit 件返す。
func (r *StockItemRows) loadExpiredPending(before time.Time, limit int) ([]*domain.StockItem, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var items []*domain.StockItem
	for _, row := range r.rows {
		if !recordHasExpiredPending(row, before) {
			continue
		}
		item, err := recordToStockItem(row)
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

func recordHasReservation(r stockItemRecord, ref string) bool {
	for _, rr := range r.reservations {
		if rr.ref == ref {
			return true
		}
	}
	return false
}

func recordHasExpiredPending(r stockItemRecord, before time.Time) bool {
	for _, rr := range r.reservations {
		if rr.status == domain.ReservationPending && !rr.expiresAt.IsZero() && !before.Before(rr.expiresAt) {
			return true
		}
	}
	return false
}

// itemToRecord は集約を、指定バージョンで確定行へ変換する（予約状態を含む）。
func itemToRecord(item *domain.StockItem, version int) stockItemRecord {
	res := item.Reservations()
	rows := make([]reservationRow, 0, len(res))
	for _, r := range res {
		rows = append(rows, reservationRow{
			ref:       r.Ref().String(),
			quantity:  r.Quantity().Int(),
			status:    r.Status(),
			expiresAt: r.ExpiresAt(),
		})
	}
	return stockItemRecord{
		id:           item.ID(),
		sku:          item.SKU().String(),
		available:    item.Available().Int(),
		version:      version,
		reservations: rows,
	}
}

// txStockStore はトランザクションに束ねた StockStore。
type txStockStore struct {
	tx   *txState
	rows *StockItemRows
}

// コンパイル時にポートを満たしていることを確認する。
// この表明は検査 14（集約ストア実装のファイル名）の判定根拠でもある。
var _ application.StockStore = (*txStockStore)(nil)

func (s *txStockStore) Load(_ context.Context, sku domain.SKU) (*domain.StockItem, error) {
	return s.rows.load(sku)
}

func (s *txStockStore) LoadMany(_ context.Context, skus []domain.SKU) ([]*domain.StockItem, error) {
	return s.rows.loadMany(skus)
}

func (s *txStockStore) LoadByReservation(_ context.Context, ref domain.ReservationRef) ([]*domain.StockItem, error) {
	return s.rows.loadByReservation(ref)
}

func (s *txStockStore) LoadExpiredPending(_ context.Context, before time.Time, limit int) ([]*domain.StockItem, error) {
	return s.rows.loadExpiredPending(before, limit)
}

// Save は各集約の版を確定ストアと突き合わせて検証し、集約のバージョンを同期（MarkPersisted）
// したうえで、確定ストアへ書き込む操作（予約状態を含む）を staging に積む。実際の書き込みは
// コミット時に行う。版が食い違えば uow.ErrConcurrencyConflict を返し、確定ストアは変更しない。
//
// items が複数ある場合（マルチ SKU 予約）も、積まれる先は同じ backing store のグループなので
// コミット時に 1 回のロックでまとめて確定する（束の途中の状態は観測されない）。
func (s *txStockStore) Save(_ context.Context, items ...*domain.StockItem) error {
	s.rows.mu.Lock()
	defer s.rows.mu.Unlock()

	for _, item := range items {
		existing, ok := s.rows.rows[item.SKU().String()]
		var next int
		if item.Version() == 0 {
			// 新規挿入。既に存在するなら衝突。
			if ok {
				return fmt.Errorf("SKU %q は既に存在します: %w", item.SKU().String(), uow.ErrConcurrencyConflict)
			}
			next = 1
		} else {
			// 既存更新。存在しない、または版が食い違えば衝突。
			if !ok || existing.version != item.Version() {
				return fmt.Errorf("SKU %q のバージョンが一致しません: %w", item.SKU().String(), uow.ErrConcurrencyConflict)
			}
			next = item.Version() + 1
		}

		row := itemToRecord(item, next)
		s.tx.stage(&s.rows.mu, func() { s.rows.putLocked(row) })
		item.MarkPersisted(next)
	}
	return nil
}

// NewReadStockStore は読み取り用の StockStore を返す。
// 在庫照会ユースケース用。書き込みには使わない。
func NewReadStockStore(rows *StockItemRows) application.StockStore {
	return &readStockStore{rows: rows}
}

// readStockStore は確定済みデータを直接読む読み取り専用アダプタ。
type readStockStore struct {
	rows *StockItemRows
}

var _ application.StockStore = (*readStockStore)(nil)

func (s *readStockStore) Load(_ context.Context, sku domain.SKU) (*domain.StockItem, error) {
	return s.rows.load(sku)
}

func (s *readStockStore) LoadMany(_ context.Context, skus []domain.SKU) ([]*domain.StockItem, error) {
	return s.rows.loadMany(skus)
}

func (s *readStockStore) LoadByReservation(_ context.Context, ref domain.ReservationRef) ([]*domain.StockItem, error) {
	return s.rows.loadByReservation(ref)
}

func (s *readStockStore) LoadExpiredPending(_ context.Context, before time.Time, limit int) ([]*domain.StockItem, error) {
	return s.rows.loadExpiredPending(before, limit)
}

// Save は読み取り専用アダプタでは使用しない。誤用を早期に検知するためエラーを返す。
func (s *readStockStore) Save(_ context.Context, _ ...*domain.StockItem) error {
	return fmt.Errorf("readStockStore は読み取り専用です: 書き込みは UnitOfWork.Within を使ってください")
}
