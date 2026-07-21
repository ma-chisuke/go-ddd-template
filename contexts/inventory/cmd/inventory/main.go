// Command inventory は在庫コンテキストを単独のサービスとして起動する合成ルート。
// pgx のアダプタと ogen 製の公開サーバを結線し、HTTP サーバを立ち上げる。
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
	"github.com/example/go-ddd-template/contexts/inventory/internal/infrastructure/logging"
)

func main() {
	log := logging.New(os.Stdout, slog.LevelInfo)
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

	mux := http.NewServeMux()
	// ヘルスチェック（コンテナ／ロードバランサ用）。
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	// 在庫コンテキストの公開 API をマウントする。
	mux.Handle("/", mod.HTTPHandler())

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// サーバを別 goroutine で起動する。
	serverErr := make(chan error, 1)
	go func() {
		log.Info("HTTP サーバを起動します", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
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

	// グレースフルシャットダウン。
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	log.Info("サービスを正常に停止しました")
	return nil
}
