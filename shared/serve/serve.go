// Package serve は 1 個以上の HTTP サーバを起動し、停止指示まで走らせ、まとめて
// グレースフルシャットダウンする薄いランナーを提供する。
//
// 抽出の原則は「間違えやすい部分を共有し、自明な部分は呼び出し側に残す」である。
// goroutine のファンアウト、http.ErrServerClosed の除外、エラーチャネルの容量、
// 停止経路の合流、猶予つき context の採り方 — これらは各サービスの main.go に
// コピーされると、どれか 1 つを取りこぼしても気づきにくい。逆に配線（env 読取・
// 依存の組立・mux の構築）は自明で、採用者が必ず自分のものへ書き換える部分なので残す。
//
// このパッケージが意図的に持たないもの:
//   - シグナル受信 … 呼び出し側が signal.NotifyContext で ctx を作って渡す。DB プールや
//     背景ワーカーがランナー起動より前に同じ cancellation を必要とするため、ランナーが
//     ctx を内部生成すると両者が停止シグナルを共有できない。どのシグナルを扱うか
//     （SIGINT / SIGTERM）が合成ルートに見えていること自体が、コンテナ実行環境との
//     契約として読者に伝わるべき情報でもある。
//   - 資源の解放 … 取得元が defer で閉じる。ランナー到達前に早期 return する経路が
//     あるため（疎通確認の失敗、依存構築の失敗）、解放をランナーへ移すとその経路で
//     資源が漏れる。取得の隣に defer を置くのが Go の資源寿命の定石である。
//   - ヘルスチェック … 運用契約なので各サービスの mux に載せる。採用者はすぐに DB 疎通や
//     liveness/readiness の分離を求めるため、ランナーが持つとその時点で捨てられ、
//     死んだ機能が残る。
//
// 依存は標準ライブラリのみ。特定の DB ドライバやフレームワークを知らない。
package serve

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"
)

const (
	// DefaultReadHeaderTimeout はリクエストヘッダ読み取りの既定タイムアウト。
	// 遅いヘッダ送信でコネクションを占有する攻撃（Slowloris）への基本的な歯止めであり、
	// 各サービスの main.go にコピーされていた値をここへ集約する。
	DefaultReadHeaderTimeout = 10 * time.Second
	// DefaultShutdownTimeout はグレースフルシャットダウンの既定猶予。
	// この猶予内に処理中のリクエストが終わらなければ、待つのをやめてエラーを返す。
	DefaultShutdownTimeout = 15 * time.Second
)

// ErrNoServers は 1 本もサーバを渡さずに Run を呼んだことを表す。
// 走らせる対象が無いランナー呼び出しは結線の誤りなので、黙って戻らず失敗させる。
var ErrNoServers = errors.New("serve: 起動するサーバが 1 本も指定されていません")

// Server は起動する 1 本の HTTP サーバの仕様。
// http.Server と同じ形で読めるよう Addr / Handler をそのまま持ち、ログに出す識別名を
// Name に添える。この仕様構造体を挟むことで ReadHeaderTimeout の既定値が 1 箇所に集まる。
type Server struct {
	// Name はログに出す識別名（"公開" / "内部" など）。
	Name string
	// Addr は待ち受けアドレス（":8080" など）。
	Addr string
	// Handler はこのサーバが処理するハンドラ。
	Handler http.Handler
}

// Run は servers をすべて起動し、ctx の完了かいずれかのサーバのエラーまで待ち、
// 全サーバをグレースフルシャットダウンしてから戻る。
//
// サーバ本数に依存しない（1 本でも 2 本でも同じ経路を通る）。停止の契機が ctx の完了でも
// サーバのエラーでも、同じ停止処理へ合流する — つまりサーバ 1 本が起動に失敗したときも、
// 残りのサーバは放置されずグレースフルに停止する。
//
// 戻り値はサーバのエラーがあればそれ、無ければ最初のシャットダウンエラー、
// どちらも無ければ nil である。
func Run(ctx context.Context, log *slog.Logger, servers ...Server) error {
	return run(ctx, log, DefaultShutdownTimeout, servers...)
}

// run は Run の実装。シャットダウン猶予を引数で受けるのは、猶予切れという異常系を
// テストが既定値（15 秒）を待たずに再現できるようにするためである。公開するのは
// 既定値を固定した Run だけで、猶予は API の設定項目にしない（採用者が調整すべき
// つまみを増やさない）。
func run(ctx context.Context, log *slog.Logger, shutdownTimeout time.Duration, servers ...Server) error {
	if len(servers) == 0 {
		return ErrNoServers
	}

	srvs := newHTTPServers(servers)

	// 容量 = サーバ本数。全サーバが同時に失敗しても goroutine が送信でブロックせず、
	// 受信者が 1 件だけ読んで先へ進んでも goroutine が漏れない。
	errCh := make(chan error, len(servers))
	for i, srv := range srvs {
		spec := servers[i]
		go func() {
			log.InfoContext(ctx, "HTTP サーバを起動します", "name", spec.Name, "addr", spec.Addr)
			// Shutdown 由来の ErrServerClosed は正常終了なのでエラーとして扱わない。
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- err
			}
		}()
	}

	// 停止シグナル（ctx の完了）か、いずれかのサーバのエラーを待つ。
	var runErr error
	select {
	case <-ctx.Done():
		log.Info("停止シグナルを受信しました。グレースフルシャットダウンを開始します")
	case err := <-errCh:
		runErr = err
		log.Error("HTTP サーバがエラーで停止しました。全サーバを停止します", "error", err)
	}

	// どちらの経路も同じ停止処理へ合流する。ctx は既にキャンセル済みの可能性があるため、
	// 猶予つきの context は Background から採る（キャンセル済み ctx を親にすると
	// Shutdown が即座に諦めてしまう）。
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	// 途中で失敗しても残りのサーバを飛ばさない（1 本の失敗で他が停止されないのを防ぐ）。
	// 返すのは最初のシャットダウンエラーだけで、後続はログに残す。
	var firstShutdownErr error
	for i, srv := range srvs {
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Error("HTTP サーバのグレースフルシャットダウンに失敗しました",
				"name", servers[i].Name, "error", err)
			if firstShutdownErr == nil {
				firstShutdownErr = err
			}
		}
	}

	switch {
	case runErr != nil:
		return runErr
	case firstShutdownErr != nil:
		return firstShutdownErr
	default:
		log.Info("サービスを正常に停止しました")
		return nil
	}
}

// newHTTPServers は仕様から *http.Server を組む。
// ReadHeaderTimeout の既定値がここ 1 箇所に集まる（各 main.go にコピーされていた値の集約）。
func newHTTPServers(specs []Server) []*http.Server {
	srvs := make([]*http.Server, len(specs))
	for i, s := range specs {
		srvs[i] = &http.Server{
			Addr:              s.Addr,
			Handler:           s.Handler,
			ReadHeaderTimeout: DefaultReadHeaderTimeout,
		}
	}
	return srvs
}
