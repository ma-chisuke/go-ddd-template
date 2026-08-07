// Package postgres は PostgreSQL（pgx + sqlc）を用いた送信アダプタ
// （outbound adapter＝被駆動側）を提供する。ヘキサゴナルアーキテクチャの「出口」であり、
// application 層のポート（StockStore、UnitOfWork、MessagePublisher）を実装して外の世界（DB）へ
// 書き出す。
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/example/go-ddd-template/contexts/inventory/internal/adapter/outbound/postgres/sqlcgen"
	"github.com/example/go-ddd-template/contexts/inventory/internal/application"
	"github.com/example/go-ddd-template/contexts/inventory/internal/domain"
	"github.com/example/go-ddd-template/shared/uow"
)

// pgUniqueViolation は PostgreSQL の一意制約違反を表す SQLSTATE。
const pgUniqueViolation = "23505"

// stockStore は sqlc が生成した Queries を用いた StockStore アダプタ。
// sqlcgen.Queries は *pgxpool.Pool でも pgx.Tx でも動作するため、
// 読み取り用（プール直結）と書き込み用（トランザクション束縛）の両方でこの型を使える。
type stockStore struct {
	q *sqlcgen.Queries
}

// コンパイル時にポートを満たしていることを確認する。
//
// 表明は型に対して 1 つで足りる。読み取り用（NewReadStockStore）と書き込み用
// （NewUnitOfWork の closure）は同じ stockStore 型を使い回すため、生成関数ごとに
// 表明を重ねる必要はない。
//
// この表明は「この型はこのポートの実装である」という読み手への明示であり、同時に
// 検査 14（集約ストア実装のファイル名）の判定根拠でもある。
var _ application.StockStore = (*stockStore)(nil)

func newStockStore(q *sqlcgen.Queries) *stockStore {
	return &stockStore{q: q}
}

// Load は SKU で在庫項目を読み込み、予約を含めて集約を復元する。
// 行が存在しない場合は domain.ErrStockItemNotFound を返す。
func (s *stockStore) Load(ctx context.Context, sku domain.SKU) (*domain.StockItem, error) {
	row, err := s.q.GetStockItemBySKU(ctx, sku.String())
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("SKU %q: %w", sku.String(), domain.ErrStockItemNotFound)
		}
		return nil, fmt.Errorf("在庫項目の読み込みに失敗しました: %w", err)
	}
	return s.reconstitute(ctx, row.ID, row.Sku, row.Available, row.Version)
}

// LoadMany は複数 SKU をまとめて読み込む。見つからない SKU は除外する。
func (s *stockStore) LoadMany(ctx context.Context, skus []domain.SKU) ([]*domain.StockItem, error) {
	items := make([]*domain.StockItem, 0, len(skus))
	for _, sku := range skus {
		item, err := s.Load(ctx, sku)
		if err != nil {
			if errors.Is(err, domain.ErrStockItemNotFound) {
				continue
			}
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

// LoadByReservation は指定参照を持つ全ての在庫項目を読み込む。
func (s *stockStore) LoadByReservation(ctx context.Context, ref domain.ReservationRef) ([]*domain.StockItem, error) {
	ids, err := s.q.ListStockItemIDsByReservationRef(ctx, ref.String())
	if err != nil {
		return nil, fmt.Errorf("予約参照による在庫項目の検索に失敗しました: %w", err)
	}
	return s.loadByIDs(ctx, ids)
}

// LoadExpiredPending は before 時点で期限切れの pending 予約を持つ在庫項目を最大 limit 件返す。
func (s *stockStore) LoadExpiredPending(ctx context.Context, before time.Time, limit int) ([]*domain.StockItem, error) {
	ids, err := s.q.ListExpiredPendingStockItemIDs(ctx, sqlcgen.ListExpiredPendingStockItemIDsParams{
		ExpiresAt: pgtype.Timestamptz{Time: before, Valid: true},
		Limit:     int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("期限切れ予約の検索に失敗しました: %w", err)
	}
	return s.loadByIDs(ctx, ids)
}

func (s *stockStore) loadByIDs(ctx context.Context, ids []string) ([]*domain.StockItem, error) {
	items := make([]*domain.StockItem, 0, len(ids))
	for _, id := range ids {
		row, err := s.q.GetStockItemByID(ctx, id)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			return nil, fmt.Errorf("在庫項目 %q の読み込みに失敗しました: %w", id, err)
		}
		item, err := s.reconstitute(ctx, row.ID, row.Sku, row.Available, row.Version)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

// reconstitute は在庫行と、その予約行から集約を復元する。
func (s *stockStore) reconstitute(ctx context.Context, id, sku string, available, version int32) (*domain.StockItem, error) {
	qty, err := domain.NewQuantity(int(available))
	if err != nil {
		return nil, fmt.Errorf("永続化された数量が不正です（SKU=%q）: %w", sku, err)
	}
	loadedSKU, err := domain.NewSKU(sku)
	if err != nil {
		return nil, fmt.Errorf("永続化された SKU が不正です: %w", err)
	}

	resRows, err := s.q.ListReservationsByStockItem(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("予約の読み込みに失敗しました: %w", err)
	}
	reservations := make([]domain.Reservation, 0, len(resRows))
	for _, rr := range resRows {
		ref, err := domain.NewReservationRef(rr.Ref)
		if err != nil {
			return nil, fmt.Errorf("永続化された予約参照が不正です: %w", err)
		}
		rq, err := domain.NewQuantity(int(rr.Quantity))
		if err != nil {
			return nil, fmt.Errorf("永続化された予約数量が不正です: %w", err)
		}
		status, err := parseReservationStatus(rr.Status)
		if err != nil {
			return nil, err
		}
		var expiresAt time.Time
		if rr.ExpiresAt.Valid {
			expiresAt = rr.ExpiresAt.Time
		}
		reservations = append(reservations, domain.ReconstituteReservation(ref, rq, status, expiresAt))
	}
	return domain.ReconstituteStockItem(id, loadedSKU, qty, int(version), reservations), nil
}

// Save は在庫項目を予約状態ごと永続化する。version が 0 の集約は新規挿入し、それ以外は
// 楽観的排他制御つきで更新する。版が食い違えば uow.ErrConcurrencyConflict を返す。
// 予約は「全削除 → 現在の予約を挿入」というスナップショット方式で書き込む（同一トランザクション）。
func (s *stockStore) Save(ctx context.Context, items ...*domain.StockItem) error {
	for _, item := range items {
		if err := s.saveOne(ctx, item); err != nil {
			return err
		}
	}
	return nil
}

func (s *stockStore) saveOne(ctx context.Context, item *domain.StockItem) error {
	if item.Version() == 0 {
		// 新規挿入。永続化済みのバージョンは 1 から始める。
		err := s.q.InsertStockItem(ctx, sqlcgen.InsertStockItemParams{
			ID:        item.ID(),
			Sku:       item.SKU().String(),
			Available: int32(item.Available().Int()),
			Version:   1,
		})
		if err != nil {
			// 同一 SKU の同時挿入は一意制約違反になる。再試行で解決できるよう
			// 楽観的排他制御の衝突へ翻訳する（再試行時に既存行を読み直して更新する）。
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
				return fmt.Errorf("SKU %q は既に存在します: %w", item.SKU().String(), uow.ErrConcurrencyConflict)
			}
			return fmt.Errorf("在庫項目の挿入に失敗しました: %w", err)
		}
		item.MarkPersisted(1)
		return s.saveReservations(ctx, item)
	}

	// 既存項目の更新。期待バージョンが一致する行だけを更新する。
	next := item.Version() + 1
	rows, err := s.q.UpdateStockItem(ctx, sqlcgen.UpdateStockItemParams{
		Available: int32(item.Available().Int()),
		Version:   int32(next),
		Sku:       item.SKU().String(),
		Version_2: int32(item.Version()),
	})
	if err != nil {
		return fmt.Errorf("在庫項目の更新に失敗しました: %w", err)
	}
	if rows == 0 {
		// 更新対象が 0 行 = 期待バージョンの行が無い = 他者が先に更新した（衝突）。
		return fmt.Errorf("SKU %q のバージョンが一致しません: %w", item.SKU().String(), uow.ErrConcurrencyConflict)
	}
	item.MarkPersisted(next)
	return s.saveReservations(ctx, item)
}

// saveReservations は集約の予約状態をスナップショットとして書き直す。
func (s *stockStore) saveReservations(ctx context.Context, item *domain.StockItem) error {
	if err := s.q.DeleteReservationsByStockItem(ctx, item.ID()); err != nil {
		return fmt.Errorf("予約の削除に失敗しました: %w", err)
	}
	for _, r := range item.Reservations() {
		var expiresAt pgtype.Timestamptz
		if r.Status() == domain.ReservationPending && !r.ExpiresAt().IsZero() {
			expiresAt = pgtype.Timestamptz{Time: r.ExpiresAt(), Valid: true}
		}
		err := s.q.InsertReservation(ctx, sqlcgen.InsertReservationParams{
			StockItemID: item.ID(),
			Ref:         r.Ref().String(),
			Quantity:    int32(r.Quantity().Int()),
			Status:      r.Status().String(),
			ExpiresAt:   expiresAt,
		})
		if err != nil {
			return fmt.Errorf("予約の挿入に失敗しました: %w", err)
		}
	}
	return nil
}

// parseReservationStatus は永続化された文字列を予約状態へ変換する。
func parseReservationStatus(s string) (domain.ReservationStatus, error) {
	switch s {
	case "pending":
		return domain.ReservationPending, nil
	case "confirmed":
		return domain.ReservationConfirmed, nil
	default:
		return domain.ReservationPending, fmt.Errorf("永続化された予約状態が不正です: %q", s)
	}
}
