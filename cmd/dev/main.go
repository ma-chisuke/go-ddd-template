// Command dev は、在庫コンテキストと注文コンテキストを 1 プロセスにインメモリで結線した
// 開発・テスト用ハーネス。Docker も DB も不要で、`go run ./cmd/dev` を実行するだけで
// 「まず動く」様子（予約 → 確定 → 取消 → 解放 → 在庫不足による拒否）を確認できる。
//
// 位置づけ（重要）:
//   - これは dev/test 専用であり、出荷ランタイムではない。本番は各コンテキストを独立した
//     サービス（コンテナ）として動かす（docker-compose を参照）。
//   - 注文 → 在庫のクロスコンテキストメッセージは同期 in-process publisher で即時配送される。
//     これは decoupling（注文が在庫のドメイン型を知らずに契約だけで到達すること）を示すが、
//     実運用の遅延ある eventual consistency（結果整合）のタイミングは示さない。遅延を伴う
//     本物の結果整合は、PostgreSQL のアウトボックス + 送信中継（docker-compose 経路）で観察できる。
package main

import (
	"context"
	"log/slog"
	"os"
	"time"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := run(log); err != nil {
		log.Error("デモの実行に失敗しました", "error", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	// 背景ワーカー（在庫の Reaper など）を止められるよう、キャンセル可能な context を用いる。
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	log.InfoContext(ctx, "開発ハーネスを起動します（Docker/DB 不要・両コンテキストを 1 プロセスで結線）")

	h, err := newHarness(harnessDeps{
		logger:         log,
		reservationTTL: 30 * time.Second,
		reaperInterval: 500 * time.Millisecond, // 背景ワーカーが動くことを示す（デモは reaper のタイミングに依存しない）
	})
	if err != nil {
		return err
	}
	h.startWorkers(ctx)

	res, err := runScenario(ctx, log, h)
	if err != nil {
		return err
	}

	log.InfoContext(ctx, "デモが完了しました",
		slog.String("order_id", res.orderID),
		slog.String("placed_status", res.placedStatus),
		slog.Int("reserved_after_place", res.reservedAfterPlace),
		slog.String("cancelled_status", res.cancelledStatus),
		slog.Int("reserved_after_cancel", res.reservedAfterCancel),
		slog.Int("rejected_status_code", res.rejectedStatusCode),
	)
	return nil
}
