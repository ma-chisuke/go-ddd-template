// Command inventory は在庫コンテキストを単独のサービスとして起動する合成ルート。
// pgx のアダプタと ogen 製のサーバ（公開・内部）を結線し、HTTP サーバを立ち上げ、
// 背景ワーカー（期限切れ掃除・アウトボックス送信中継）を起動する。
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

	inventory "github.com/example/go-ddd-template/contexts/inventory"
	sharedlog "github.com/example/go-ddd-template/shared/logging"
)

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
	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	internalAddr := os.Getenv("INTERNAL_HTTP_ADDR")
	if internalAddr == "" {
		internalAddr = ":8081"
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

	// 在庫コンテキストのモジュールを構築する。
	mod, err := inventory.New(inventory.Deps{Pool: pool, Logger: log})
	if err != nil {
		return err
	}

	// 背景ワーカー（Reaper と アウトボックス送信中継）を起動する。
	mod.StartWorkers(ctx)

	// 公開サーバ（補充・照会）。ヘルスチェックもここに載せる。
	publicMux := http.NewServeMux()
	publicMux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	publicMux.Handle("/", mod.HTTPHandler())
	publicSrv := &http.Server{Addr: addr, Handler: publicMux, ReadHeaderTimeout: 10 * time.Second}

	// 内部サーバ（予約・確定・解放・メッセージ取り込み）。公開サーバとは別ポートで待ち受ける。
	internalSrv := &http.Server{Addr: internalAddr, Handler: mod.InternalHTTPHandler(), ReadHeaderTimeout: 10 * time.Second}

	// 2 つのサーバを別 goroutine で起動する。
	serverErr := make(chan error, 2)
	go func() {
		log.Info("公開 HTTP サーバを起動します", "addr", addr)
		if err := publicSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
			return
		}
		serverErr <- nil
	}()
	go func() {
		log.Info("内部 HTTP サーバを起動します", "addr", internalAddr)
		if err := internalSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
			return
		}
		serverErr <- nil
	}()

	// 停止シグナルかサーバエラーのどちらかを待つ。
	select {
	case <-ctx.Done():
		log.Info("停止シグナルを受信しました。グレースフルシャットダウンを開始します")
	case err := <-serverErr:
		return err
	}

	// グレースフルシャットダウン（両サーバ）。
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()
	if err := publicSrv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	if err := internalSrv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	log.Info("サービスを正常に停止しました")
	return nil
}
