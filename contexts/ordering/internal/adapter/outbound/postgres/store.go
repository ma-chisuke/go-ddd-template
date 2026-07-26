// Package postgres は PostgreSQL（pgx + sqlc）を用いた送信アダプタ
// （outbound adapter＝被駆動側）を提供する。ヘキサゴナルアーキテクチャの「出口」であり、
// application 層のポート（OrderStore、UnitOfWork、MessagePublisher）を実装して外の世界（DB）へ
// 書き出す。
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/example/go-ddd-template/contexts/ordering/internal/adapter/outbound/postgres/sqlcgen"
	"github.com/example/go-ddd-template/contexts/ordering/internal/domain"
	"github.com/example/go-ddd-template/shared/uow"
)

// pgUniqueViolation は PostgreSQL の一意制約違反を表す SQLSTATE。
const pgUniqueViolation = "23505"

// orderStore は sqlc が生成した Queries を用いた OrderStore アダプタ。
// sqlcgen.Queries は *pgxpool.Pool でも pgx.Tx でも動作するため、
// 読み取り用（プール直結）と書き込み用（トランザクション束縛）の両方でこの型を使える。
type orderStore struct {
	q *sqlcgen.Queries
}

func newOrderStore(q *sqlcgen.Queries) *orderStore {
	return &orderStore{q: q}
}

// Load は ID で注文を読み込み、明細を含めて集約を復元する。
// 行が存在しない場合は domain.ErrOrderNotFound を返す。
func (s *orderStore) Load(ctx context.Context, id domain.OrderID) (*domain.Order, error) {
	row, err := s.q.GetOrderByID(ctx, id.String())
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("注文 %q: %w", id.String(), domain.ErrOrderNotFound)
		}
		return nil, fmt.Errorf("注文の読み込みに失敗しました: %w", err)
	}
	return s.reconstitute(ctx, row)
}

// reconstitute は注文行と、その明細行から集約を復元する。
func (s *orderStore) reconstitute(ctx context.Context, row sqlcgen.GetOrderByIDRow) (*domain.Order, error) {
	orderID, err := domain.NewOrderID(row.ID)
	if err != nil {
		return nil, fmt.Errorf("永続化された注文 ID が不正です: %w", err)
	}
	customer, err := domain.NewCustomerID(row.CustomerID)
	if err != nil {
		return nil, fmt.Errorf("永続化された顧客 ID が不正です: %w", err)
	}
	status, err := parseStatus(row.Status)
	if err != nil {
		return nil, err
	}
	total, err := domain.NewMoney(row.TotalAmount, row.TotalCurrency)
	if err != nil {
		return nil, fmt.Errorf("永続化された合計金額が不正です: %w", err)
	}
	ref, err := domain.NewReservationRef(row.ReservationRef)
	if err != nil {
		return nil, fmt.Errorf("永続化された予約参照が不正です: %w", err)
	}

	lineRows, err := s.q.ListOrderLines(ctx, row.ID)
	if err != nil {
		return nil, fmt.Errorf("注文明細の読み込みに失敗しました: %w", err)
	}
	lines := make([]domain.OrderLine, 0, len(lineRows))
	for _, lr := range lineRows {
		sku, err := domain.NewSKU(lr.Sku)
		if err != nil {
			return nil, fmt.Errorf("永続化された SKU が不正です: %w", err)
		}
		qty, err := domain.NewQuantity(int(lr.Quantity))
		if err != nil {
			return nil, fmt.Errorf("永続化された数量が不正です: %w", err)
		}
		price, err := domain.NewMoney(lr.UnitPrice, lr.Currency)
		if err != nil {
			return nil, fmt.Errorf("永続化された単価が不正です: %w", err)
		}
		lines = append(lines, domain.NewOrderLine(sku, qty, price))
	}
	return domain.ReconstituteOrder(orderID, customer, lines, status, total, ref, int(row.Version)), nil
}

// Save は注文を明細ごと永続化する。version が 0 の集約は新規挿入し、それ以外は
// 楽観的排他制御つきで更新する。版が食い違えば uow.ErrConcurrencyConflict を返す。
// 明細は作成後に変化しないため、更新時（取消）には注文行の状態のみを書き換える。
func (s *orderStore) Save(ctx context.Context, o *domain.Order) error {
	if o.Version() == 0 {
		return s.insert(ctx, o)
	}
	return s.update(ctx, o)
}

// insert は新規注文を明細ごと挿入する。永続化済みのバージョンは 1 から始まる。
func (s *orderStore) insert(ctx context.Context, o *domain.Order) error {
	err := s.q.InsertOrder(ctx, sqlcgen.InsertOrderParams{
		ID:             o.ID().String(),
		CustomerID:     o.CustomerID().String(),
		Status:         o.Status().String(),
		TotalAmount:    o.Total().Amount(),
		TotalCurrency:  o.Total().Currency(),
		ReservationRef: o.ReservationRef().String(),
		Version:        1,
	})
	if err != nil {
		// 同一 ID の同時挿入は一意制約違反になる。再試行で解決できるよう衝突へ翻訳する。
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
			return fmt.Errorf("注文 %q は既に存在します: %w", o.ID().String(), uow.ErrConcurrencyConflict)
		}
		return fmt.Errorf("注文の挿入に失敗しました: %w", err)
	}
	for i, l := range o.Lines() {
		err := s.q.InsertOrderLine(ctx, sqlcgen.InsertOrderLineParams{
			OrderID:   o.ID().String(),
			LineNo:    int32(i),
			Sku:       l.SKU().String(),
			Quantity:  int32(l.Quantity().Int()),
			UnitPrice: l.UnitPrice().Amount(),
			Currency:  l.UnitPrice().Currency(),
		})
		if err != nil {
			return fmt.Errorf("注文明細の挿入に失敗しました: %w", err)
		}
	}
	o.MarkPersisted(1)
	return nil
}

// update は楽観的排他制御つきで注文の状態を更新する（取消など）。
func (s *orderStore) update(ctx context.Context, o *domain.Order) error {
	next := o.Version() + 1
	rows, err := s.q.UpdateOrder(ctx, sqlcgen.UpdateOrderParams{
		Status:    o.Status().String(),
		Version:   int32(next),
		ID:        o.ID().String(),
		Version_2: int32(o.Version()),
	})
	if err != nil {
		return fmt.Errorf("注文の更新に失敗しました: %w", err)
	}
	if rows == 0 {
		// 更新対象が 0 行 = 期待バージョンの行が無い = 他者が先に更新した（衝突）。
		return fmt.Errorf("注文 %q のバージョンが一致しません: %w", o.ID().String(), uow.ErrConcurrencyConflict)
	}
	o.MarkPersisted(next)
	return nil
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
