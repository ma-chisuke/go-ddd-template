// Command ordering は注文コンテキストを単独のサービスとして起動する合成ルート。
// pgx のアダプタと ogen 製の公開サーバを結線し、在庫コンテキストへは生成クライアント
// clients/inventory を用いる腐敗防止層（aclhttp）越しに到達する。HTTP サーバを立ち上げ、
// 背景ワーカー（アウトボックス送信中継）を起動する。
//
// 分散構成の既定: 在庫予約は HTTP の ACL（aclhttp）で、クロスコンテキストのメッセージ送出は
// 在庫の event-ingest への HTTP push（eventhttp）で行う。どちらも同一の生成クライアントを
// 共有し、在庫サービスの内部 API へ到達する。
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	ordering "github.com/example/go-ddd-template/contexts/ordering"
	"github.com/example/go-ddd-template/contexts/ordering/internal/adapter/outbound/aclhttp"
	"github.com/example/go-ddd-template/contexts/ordering/internal/adapter/outbound/eventhttp"
	sharedlog "github.com/example/go-ddd-template/shared/logging"
	"github.com/example/go-ddd-template/shared/serve"
)

// defaultInventoryTimeout は在庫サービスへの ACL / メッセージ送出の全体タイムアウト既定値。
const defaultInventoryTimeout = 5 * time.Second

func main() {
	log := sharedlog.New(os.Stdout, slog.LevelInfo)
	if err := run(log); err != nil {
		log.Error("サービスが異常終了しました", "error", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	// 設定は環境変数から読む。秘密情報をコードやイメージに焼き込まない。
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return errors.New("環境変数 DATABASE_URL が設定されていません")
	}
	inventoryURL := os.Getenv("INVENTORY_INTERNAL_URL")
	if inventoryURL == "" {
		return errors.New("環境変数 INVENTORY_INTERNAL_URL（在庫の内部 API のベース URL）が設定されていません")
	}
	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	// シグナルで停止をハンドリングする context。
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// コネクションプールを構築する。
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	// 起動時に一度だけ疎通確認する。
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		return err
	}

	// 在庫の内部 API を呼ぶ生成クライアント（相関 ID 伝播 + タイムアウト付き）。
	// これを ACL アダプタ（在庫予約）とイベント送信アダプタ（メッセージ送出）で共有する。
	inventoryClient, err := aclhttp.NewInventoryClient(inventoryURL, defaultInventoryTimeout)
	if err != nil {
		return err
	}

	// 注文コンテキストのモジュールを構築する。
	mod, err := ordering.New(ordering.Deps{
		Pool:      pool,
		Reserver:  aclhttp.NewReserver(inventoryClient),
		Publisher: eventhttp.NewPublisher(inventoryClient),
		Logger:    log,
	})
	if err != nil {
		return err
	}

	// 背景ワーカー（アウトボックス送信中継）を起動する。
	mod.StartWorkers(ctx)

	// 公開サーバ（作成・照会・取消）。ヘルスチェックもここに載せる。
	publicMux := http.NewServeMux()
	publicMux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	publicMux.Handle("/", mod.HTTPHandler())

	// プロセスのライフサイクル（起動・停止待ち・グレースフルシャットダウン）は共有ランナーに
	// 委ねる。ここに残っているのは、このサービス固有の配線だけである。
	return serve.Run(ctx, log, serve.Server{Name: "公開", Addr: addr, Handler: publicMux})
}
