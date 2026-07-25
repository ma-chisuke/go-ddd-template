package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	inventory "github.com/example/go-ddd-template/contexts/inventory"
	ordering "github.com/example/go-ddd-template/contexts/ordering"
)

// harness は両コンテキスト（在庫・注文）を 1 プロセスにインメモリで結線したもの。
// 公開ファサードだけを保持し、internal/ 配下には触れない。
type harness struct {
	inventory *inventory.Module
	ordering  *ordering.Module
}

// newHarness は両コンテキストをインメモリで結線する。
//
// 結線の要点:
//   - 在庫: NewInMemory + no-op publisher（在庫はクロスコンテキスト送信をしない）。
//   - 注文: NewInMemory に、在庫を直接呼ぶ in-process ACL（Reserver）と、コミット時に
//     ピアへ同期配送する publisher を注入する。
//
// これにより注文 → 在庫の 3 つのシーム（同期予約 / 確定コマンド / 取消イベント）が、
// ネットワークも Docker も無しで、同一プロセス内で決定的に動く。
func newHarness(deps harnessDeps) (*harness, error) {
	// 1) 在庫モジュール（インメモリ・no-op publisher）。
	//    reaper は短い間隔で回し、背景ワーカーが動くことを示す（デモは reaper のタイミングには依存しない）。
	inv, err := inventory.NewInMemory(inventory.InMemoryDeps{
		Logger:         deps.logger,
		Publisher:      noopPublisher{},
		ReservationTTL: deps.reservationTTL,
		ReaperInterval: deps.reaperInterval,
	})
	if err != nil {
		return nil, fmt.Errorf("在庫モジュールの構築に失敗しました: %w", err)
	}

	// 2) 注文モジュール（インメモリ・in-process ACL・同期 publisher）。
	ord, err := ordering.NewInMemory(ordering.InMemoryDeps{
		Logger:    deps.logger,
		Reserver:  &inProcessReserver{inv: inv},    // Ordering → Inventory を直接呼ぶ
		Publisher: &syncDeliverPublisher{inv: inv}, // コミット時にピアへ同期配送
	})
	if err != nil {
		return nil, fmt.Errorf("注文モジュールの構築に失敗しました: %w", err)
	}

	return &harness{inventory: inv, ordering: ord}, nil
}

// harnessDeps は newHarness の設定。
type harnessDeps struct {
	logger         *slog.Logger
	reservationTTL time.Duration
	reaperInterval time.Duration
}

// startWorkers は両モジュールの背景ワーカーを起動する（在庫の Reaper など）。
// 注文モジュールは同期配送構成のため送信中継を持たず、実質何も起動しない。
func (h *harness) startWorkers(ctx context.Context) {
	h.inventory.StartWorkers(ctx)
	h.ordering.StartWorkers(ctx)
}

// inventoryHandler は在庫の公開 HTTP ハンドラ（補充・照会）を返す。
func (h *harness) inventoryHandler() http.Handler { return h.inventory.HTTPHandler() }

// orderingHandler は注文の公開 HTTP ハンドラ（作成・照会・取消）を返す。
func (h *harness) orderingHandler() http.Handler { return h.ordering.HTTPHandler() }

// inventoryInternalHandler は在庫の内部 HTTP ハンドラ（予約・確定・解放・取り込み）を返す。
// デモの結線では使わない（in-process のシームを直接呼ぶため）が、3 サーバのエラー応答が
// 一致していることを検証するテスト（problem_parity_test.go）が必要とする。
func (h *harness) inventoryInternalHandler() http.Handler { return h.inventory.InternalHTTPHandler() }
