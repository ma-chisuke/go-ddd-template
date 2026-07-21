package application

import (
	"context"
	"log/slog"
	"time"

	"github.com/example/go-ddd-template/contexts/inventory/internal/domain/inventory"
	"github.com/example/go-ddd-template/shared/uow"
)

// ReserveLine は予約要求の 1 行（SKU と数量）。境界（HTTP など）から受け取った
// プリミティブ値を保持する。
type ReserveLine struct {
	SKU      string
	Quantity int
}

// ReserveInput は在庫予約ユースケースの入力。1 つの予約参照（相関 ID）に対して
// 複数 SKU をまとめて予約する（マルチ SKU 予約）。
type ReserveInput struct {
	Ref   string
	Lines []ReserveLine
}

// Reserver は在庫予約（書き込み）ユースケース。作業単位（UnitOfWork）の内側で
// 「読み込み → ドメイン操作 → 保存」を行い、楽観的排他制御の衝突時は Executor により
// 再試行される。マルチ SKU 予約は全か無か（all-or-nothing）で割り当てる。
type Reserver struct {
	exec     uow.Executor
	work     UnitOfWork
	dispatch EventDispatcher
	log      *slog.Logger
	service  inventory.ReservationService
	ttl      time.Duration
}

// NewReserver は在庫予約ユースケースを生成する。ttl は仮予約（pending）の有効期限。
func NewReserver(exec uow.Executor, work UnitOfWork, dispatch EventDispatcher, log *slog.Logger, ttl time.Duration) *Reserver {
	return &Reserver{exec: exec, work: work, dispatch: dispatch, log: log, ttl: ttl}
}

// Reserve は指定参照に対して複数 SKU をまとめて予約する。1 つでも在庫不足なら全体を
// 失敗させ（ErrInsufficientStock）、部分予約を作らない。既に有効な予約を持つ参照への
// Reserve は冪等な no-op になる。
//
// ドメインイベントは外側の変数に退避し、作業単位が成功（uow.Run が nil を返す）した
// あとにのみ配信する。これにより「保存に失敗したのにイベントだけ配信される」ことを防ぐ。
func (r *Reserver) Reserve(ctx context.Context, in ReserveInput) error {
	ref, err := inventory.NewReservationRef(in.Ref)
	if err != nil {
		return err
	}
	lines, skus, err := toReservationLines(in.Lines)
	if err != nil {
		return err
	}

	var events []inventory.DomainEvent
	err = uow.Run(ctx, r.exec, r.work, func(ctx context.Context, repos Repos) error {
		stocks, err := repos.Stock().LoadMany(ctx, skus)
		if err != nil {
			return err
		}
		if err := r.service.Allocate(stocks, ref, lines, r.ttl); err != nil {
			return err
		}
		if err := repos.Stock().Save(ctx, stocks...); err != nil {
			return err
		}
		events = collectEvents(stocks)
		return nil
	})
	if err != nil {
		return err
	}

	r.dispatch.Dispatch(ctx, events...)
	r.log.InfoContext(ctx, "在庫を予約しました", slog.String("ref", ref.String()), slog.Int("lines", len(lines)))
	return nil
}

// toReservationLines は入力行をドメインの ReservationLine と SKU 一覧へ変換・検証する。
func toReservationLines(in []ReserveLine) ([]inventory.ReservationLine, []inventory.SKU, error) {
	lines := make([]inventory.ReservationLine, 0, len(in))
	skus := make([]inventory.SKU, 0, len(in))
	for _, l := range in {
		sku, err := inventory.NewSKU(l.SKU)
		if err != nil {
			return nil, nil, err
		}
		qty, err := inventory.NewQuantity(l.Quantity)
		if err != nil {
			return nil, nil, err
		}
		lines = append(lines, inventory.ReservationLine{SKU: sku, Quantity: qty})
		skus = append(skus, sku)
	}
	return lines, skus, nil
}
