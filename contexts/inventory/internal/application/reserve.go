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
		return locate("", err)
	}
	lines, skus, err := toReservationLines(in.Lines)
	if err != nil {
		return err // toReservationLines が既に位置を解決している
	}

	var events []inventory.DomainEvent
	err = uow.Run(ctx, r.exec, r.work, func(ctx context.Context, repos Repos) error {
		stocks, err := repos.Stock().LoadMany(ctx, skus)
		if err != nil {
			return err
		}
		// Allocate は明細の走査を集約側で行うため、違反は自分で Index を運んでくる。
		// locate はそれを Lines[i].Quantity へ組み立てる。位置を持たない違反
		// （参照が空）や検証以外のエラー（在庫不足・在庫項目なし）は素通しになる。
		if err := r.service.Allocate(stocks, ref, lines, r.ttl); err != nil {
			return locate("", err)
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
//
// この走査は値オブジェクトの検証（SKU 非空・数量非負）だけを行う。数量が 0 かどうかは
// ここでは弾けない（在庫の Quantity は 0 を許容する）ため、ReservationService.Allocate が
// 集約側で弾く。したがって「Lines[i] の位置」はこの走査と Allocate の 2 経路から来る。
func toReservationLines(in []ReserveLine) ([]inventory.ReservationLine, []inventory.SKU, error) {
	lines := make([]inventory.ReservationLine, 0, len(in))
	skus := make([]inventory.SKU, 0, len(in))
	for i, l := range in {
		at := linePath(i)
		sku, err := inventory.NewSKU(l.SKU)
		if err != nil {
			return nil, nil, locate(at, err)
		}
		qty, err := inventory.NewQuantity(l.Quantity)
		if err != nil {
			return nil, nil, locate(at, err)
		}
		lines = append(lines, inventory.ReservationLine{SKU: sku, Quantity: qty})
		skus = append(skus, sku)
	}
	return lines, skus, nil
}
