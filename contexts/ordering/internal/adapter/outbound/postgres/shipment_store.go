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

// shipmentStore は sqlc が生成した Queries を用いた ShipmentStore アダプタ。
// sqlcgen.Queries は *pgxpool.Pool でも pgx.Tx でも動作するため、
// 読み取り用（プール直結）と書き込み用（トランザクション束縛）の両方でこの型を使える。
//
// orderStore と同じ形をしている。新しい集約を足すときに書くのはこのファイルだけで、
// トランザクション境界を持つ shared/uow/pgxuow には差分が出ない。
type shipmentStore struct {
	q *sqlcgen.Queries
}

func newShipmentStore(q *sqlcgen.Queries) *shipmentStore {
	return &shipmentStore{q: q}
}

// Load は ID で出荷を読み込み、集約を復元する。
// 行が存在しない場合は domain.ErrShipmentNotFound を返す。
func (s *shipmentStore) Load(ctx context.Context, id domain.ShipmentID) (*domain.Shipment, error) {
	row, err := s.q.GetShipmentByID(ctx, id.String())
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("出荷 %q: %w", id.String(), domain.ErrShipmentNotFound)
		}
		return nil, fmt.Errorf("出荷の読み込みに失敗しました: %w", err)
	}
	return reconstituteShipment(row)
}

// reconstituteShipment は出荷行から集約を復元する。
//
// 注文は識別子でしか参照しないため、ここで注文行を読みに行くことは無い
// （orderStore.reconstitute が明細行を読むのとは対照的である）。
func reconstituteShipment(row sqlcgen.GetShipmentByIDRow) (*domain.Shipment, error) {
	shipmentID, err := domain.NewShipmentID(row.ID)
	if err != nil {
		return nil, fmt.Errorf("永続化された出荷 ID が不正です: %w", err)
	}
	orderID, err := domain.NewOrderID(row.OrderID)
	if err != nil {
		return nil, fmt.Errorf("永続化された注文 ID が不正です: %w", err)
	}
	status, err := parseShipmentStatus(row.Status)
	if err != nil {
		return nil, err
	}
	// preparing の出荷は追跡番号を持たない。空文字はゼロ値として復元する
	// （NewTrackingNumber は空文字を弾くので、ここを通してはならない）。
	var tn domain.TrackingNumber
	if row.TrackingNumber != "" {
		tn, err = domain.NewTrackingNumber(row.TrackingNumber)
		if err != nil {
			return nil, fmt.Errorf("永続化された追跡番号が不正です: %w", err)
		}
	}
	return domain.ReconstituteShipment(shipmentID, orderID, status, tn, int(row.Version)), nil
}

// Save は出荷を永続化する。version が 0 の集約は新規挿入し、それ以外は
// 楽観的排他制御つきで更新する。版が食い違えば uow.ErrConcurrencyConflict を返す。
func (s *shipmentStore) Save(ctx context.Context, sh *domain.Shipment) error {
	if sh.Version() == 0 {
		return s.insert(ctx, sh)
	}
	return s.update(ctx, sh)
}

// insert は新規出荷を挿入する。永続化済みのバージョンは 1 から始まる。
func (s *shipmentStore) insert(ctx context.Context, sh *domain.Shipment) error {
	err := s.q.InsertShipment(ctx, sqlcgen.InsertShipmentParams{
		ID:             sh.ID().String(),
		OrderID:        sh.OrderID().String(),
		Status:         sh.Status().String(),
		TrackingNumber: sh.TrackingNumber().String(),
		Version:        1,
	})
	if err != nil {
		// 同一 ID の同時挿入は一意制約違反になる。再試行で解決できるよう衝突へ翻訳する。
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
			return fmt.Errorf("出荷 %q は既に存在します: %w", sh.ID().String(), uow.ErrConcurrencyConflict)
		}
		return fmt.Errorf("出荷の挿入に失敗しました: %w", err)
	}
	sh.MarkPersisted(1)
	return nil
}

// update は楽観的排他制御つきで出荷の状態と追跡番号を更新する（発送など）。
func (s *shipmentStore) update(ctx context.Context, sh *domain.Shipment) error {
	next := sh.Version() + 1
	rows, err := s.q.UpdateShipment(ctx, sqlcgen.UpdateShipmentParams{
		Status:         sh.Status().String(),
		TrackingNumber: sh.TrackingNumber().String(),
		Version:        int32(next),
		ID:             sh.ID().String(),
		Version_2:      int32(sh.Version()),
	})
	if err != nil {
		return fmt.Errorf("出荷の更新に失敗しました: %w", err)
	}
	if rows == 0 {
		// 更新対象が 0 行 = 期待バージョンの行が無い = 他者が先に更新した（衝突）。
		return fmt.Errorf("出荷 %q のバージョンが一致しません: %w", sh.ID().String(), uow.ErrConcurrencyConflict)
	}
	sh.MarkPersisted(next)
	return nil
}

// parseShipmentStatus は永続化された文字列を出荷状態へ変換する。
func parseShipmentStatus(s string) (domain.ShipmentStatus, error) {
	switch s {
	case "preparing":
		return domain.ShipmentPreparing, nil
	case "shipped":
		return domain.ShipmentShipped, nil
	default:
		return domain.ShipmentPreparing, fmt.Errorf("永続化された出荷状態が不正です: %q", s)
	}
}
