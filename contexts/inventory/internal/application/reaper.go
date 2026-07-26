package application

import (
	"context"
	"log/slog"
	"time"

	"github.com/example/go-ddd-template/contexts/inventory/internal/domain"
	"github.com/example/go-ddd-template/shared/uow"
)

// Clock は現在時刻を供給するポート。本番は実時間、テストは擬似時計を注入することで、
// 時間依存の掃除処理（Reaper）を決定的にテストできるようにする。
type Clock interface {
	Now() time.Time
}

// SystemClock は実時間（UTC）を返す Clock 実装。
type SystemClock struct{}

// Now は現在の UTC 時刻を返す。
func (SystemClock) Now() time.Time { return time.Now().UTC() }

// Reaper は期限切れの pending 予約を掃除するユースケース。「reserve は commit したが
// 確定に至らなかった」孤児 pending を解放して在庫を healing する。confirmed 予約は
// 決して解放しない。
type Reaper struct {
	exec     uow.Executor
	work     UnitOfWork
	dispatch EventDispatcher
	clock    Clock
	log      *slog.Logger
	batch    int
}

// NewReaper は掃除ユースケースを生成する。batch は 1 回の Sweep で処理する最大件数。
func NewReaper(exec uow.Executor, work UnitOfWork, dispatch EventDispatcher, clock Clock, log *slog.Logger, batch int) *Reaper {
	if batch <= 0 {
		batch = 100
	}
	return &Reaper{exec: exec, work: work, dispatch: dispatch, clock: clock, log: log, batch: batch}
}

// Sweep は期限切れ pending 予約を 1 回分解放する。時刻は注入された Clock から取得する。
//
// ReapExpired は解放イベントを内部に蓄積せず戻り値で返す設計なので、ここでは各試行の
// 先頭で events をリセットし、戻り値を積み上げる。楽観的排他制御の再試行が起きても、
// 成功した試行のイベントだけが最終的に配信される（重複配信を避ける）。
func (r *Reaper) Sweep(ctx context.Context) error {
	now := r.clock.Now()

	var events []domain.DomainEvent
	err := uow.Run(ctx, r.exec, r.work, func(ctx context.Context, repos Repos) error {
		events = nil // 再試行での重複を避けるため試行ごとにリセット
		expired, err := repos.Stock().LoadExpiredPending(ctx, now, r.batch)
		if err != nil {
			return err
		}
		for _, s := range expired {
			events = append(events, s.ReapExpired(now)...)
		}
		return repos.Stock().Save(ctx, expired...)
	})
	if err != nil {
		return err
	}

	if len(events) > 0 {
		r.dispatch.Dispatch(ctx, events...)
		r.log.InfoContext(ctx, "期限切れの仮予約を解放しました", slog.Int("released", len(events)))
	}
	return nil
}
