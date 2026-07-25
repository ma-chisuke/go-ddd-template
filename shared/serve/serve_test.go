package serve_test

// このテストは外部テストパッケージ（package serve_test）に置いている。公開 API だけで
// 検証できないケースは、同じディレクトリの export_test.go が内部シンボルへ薄い橋を
// 張っているのでそれを使う（理由は export_test.go の冒頭に書いてある）。

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/go-ddd-template/shared/serve"
)

// --- テストヘルパー -------------------------------------------------------------

// discardLogger は出力を捨てる構造化ロガー。
func discardLogger() *slog.Logger { return slog.New(slog.NewJSONHandler(io.Discard, nil)) }

// okHandler は 200 を返す最小のハンドラ。
func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
}

// freeAddr は空きポートのアドレスを返す（確保してすぐ手放す）。
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err, "空きポートの確保")
	addr := ln.Addr().String()
	require.NoError(t, ln.Close(), "確保したリスナのクローズ")
	return addr
}

// occupiedAddr はテスト終了まで使用中のままにするアドレスを返す。
// ここへ待ち受けようとするサーバは bind に失敗する。
func occupiedAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err, "使用中ポートの確保")
	t.Cleanup(func() { _ = ln.Close() })
	return ln.Addr().String()
}

// dialable は addr が TCP 接続を受け付けるか（= 待ち受けているか）を返す。
// HTTP のやり取りをしないので、ハンドラが処理を止めているサーバにも使える。
func dialable(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// waitServing は addr が待ち受けを開始するまで待つ。
func waitServing(t *testing.T, addr string) {
	t.Helper()
	require.Eventually(t, func() bool { return dialable(addr) }, 5*time.Second, 10*time.Millisecond,
		"サーバ %s が待ち受けを開始しない", addr)
}

// requireStopped は addr が「待ち受けていない」ことを一定時間ずっと満たすことを確認する。
//
// 1 回だけ見るのでは足りない。停止させ忘れたサーバは、bind が数ミリ秒遅れただけでも
// その直後に待ち受けを開始してしまう（ListenAndServe の goroutine が生き残るため）。
// 猶予の間ずっと到達不能であることを見て初めて「確実に停止した」と言える。
func requireStopped(t *testing.T, addr string) {
	t.Helper()
	assert.Never(t, func() bool { return dialable(addr) }, 300*time.Millisecond, 15*time.Millisecond,
		"停止したはずのサーバ %s が待ち受けている", addr)
}

// requireRebindable は addr を自分で bind できることを確認する（誰も待ち受けていない）。
//
// dial が拒否されることより強い観測である。dial の失敗は「ランナーのサーバが停止した」
// 場合にも「そのポートを無関係のプロセスが握っていてランナーは最初から bind できて
// いなかった」場合にも成立してしまうが、bind が成功するのは後者では起こり得ない。
// 「停止している」という検証が空虚に満たされる経路を潰すために使う。
func requireRebindable(t *testing.T, addr string) {
	t.Helper()
	var held net.Listener
	require.Eventually(t, func() bool {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			return false
		}
		held = ln
		return true
	}, 2*time.Second, 20*time.Millisecond,
		"%s を bind できない = まだ誰かが待ち受けている（ランナーが停止させ切れていない）", addr)
	require.NoError(t, held.Close(), "確認用リスナのクローズ")
}

// requireHTTPOK は addr へ GET して 200 が返ることを確認する（実際に処理できていること）。
func requireHTTPOK(t *testing.T, addr string) {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://" + addr + "/")
	require.NoError(t, err, "GET %s", addr)
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	require.Equal(t, http.StatusOK, resp.StatusCode, "GET %s のステータス", addr)
}

// waitRun は Run / run の戻りを待つ（戻らなければテストを失敗させる）。
func waitRun(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("ランナーが戻らない")
		return nil
	}
}

// runWithStuckShutdown は「1 本目のサーバが Shutdown の猶予切れで失敗する」状況を作って
// run を呼び、その戻り値を返す。extra に渡したサーバは 1 本目の後ろに並ぶ。
//
// 手順は決定的である。1 本目に「解放されるまで戻らないハンドラ」を持たせ、全サーバの
// 待ち受け開始を確認してからリクエストを 1 本投げ、ハンドラへ入ったことを確認したうえで
// ctx をキャンセルする。こうすると Shutdown は処理中のコネクションを待たねばならず、
// 極小の猶予（50ms）では待ちきれずに context.DeadlineExceeded を返す。
func runWithStuckShutdown(t *testing.T, extra ...serve.Server) error {
	t.Helper()

	release := make(chan struct{})
	entered := make(chan struct{})
	var once sync.Once
	stuckAddr := freeAddr(t)
	stuck := serve.Server{Name: "詰まり", Addr: stuckAddr, Handler: http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			once.Do(func() { close(entered) })
			<-release
			w.WriteHeader(http.StatusOK)
		})}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- serve.RunWithShutdownTimeout(ctx, discardLogger(), 50*time.Millisecond, append([]serve.Server{stuck}, extra...)...)
	}()

	waitServing(t, stuckAddr)
	for _, s := range extra {
		waitServing(t, s.Addr)
	}

	// 処理中のリクエストを 1 本作る。応答はハンドラを解放するまで返らない。
	reqDone := make(chan struct{})
	go func() {
		defer close(reqDone)
		resp, err := (&http.Client{}).Get("http://" + stuckAddr + "/")
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
	}()
	t.Cleanup(func() { close(release); <-reqDone })

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("ハンドラへ到達しなかった")
	}

	cancel()
	return waitRun(t, done)
}

// --- ケース 1: サーバ 0 本 ------------------------------------------------------

func TestRun_NoServersIsWiringError(t *testing.T) {
	done := make(chan error, 1)
	go func() { done <- serve.Run(context.Background(), discardLogger()) }()

	select {
	case err := <-done:
		require.ErrorIs(t, err, serve.ErrNoServers, "サーバ 0 本の呼び出しは結線の誤りとして失敗すべき")
	case <-time.After(2 * time.Second):
		t.Fatal("サーバ 0 本では待たずに戻るべきだが Run がブロックした")
	}
}

// --- ケース 2: 本数非依存の正常系 ------------------------------------------------

func TestRun_IsServerCountAgnostic(t *testing.T) {
	// ordering は 1 本（公開）、inventory は 2 本（公開 + 内部）。同じ経路を通ることを確認する。
	for _, count := range []int{1, 2} {
		t.Run(fmt.Sprintf("%d本", count), func(t *testing.T) {
			specs := make([]serve.Server, 0, count)
			addrs := make([]string, 0, count)
			for i := range count {
				addr := freeAddr(t)
				addrs = append(addrs, addr)
				specs = append(specs, serve.Server{
					Name:    fmt.Sprintf("サーバ%d", i+1),
					Addr:    addr,
					Handler: okHandler(),
				})
			}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			done := make(chan error, 1)
			go func() { done <- serve.Run(ctx, discardLogger(), specs...) }()

			// 全サーバの待ち受けが立ち、実際にリクエストを処理できる。
			for _, addr := range addrs {
				waitServing(t, addr)
				requireHTTPOK(t, addr)
			}

			// 停止シグナル相当（ctx のキャンセル）で全サーバが止まり、nil が返る。
			cancel()
			require.NoError(t, waitRun(t, done), "正常停止では nil を返すべき")
			for _, addr := range addrs {
				requireStopped(t, addr)
			}
		})
	}
}

// --- ケース 3: サーバエラー時に兄弟サーバも停止する（EMPTY+PRESENT 対）-------------

func TestRun_ServerErrorAlsoShutsDownSiblings(t *testing.T) {
	// 2 本に「同じアドレス」を渡す。これは採用者が現実に踏む設定ミス（HTTP_ADDR と
	// INTERNAL_HTTP_ADDR を同じ値にしてしまう）であり、同時に、この検証が必要とする
	// 因果順序を保証する構成でもある。
	//
	// 後から bind する側が EADDRINUSE で失敗するのは、**先に bind した側が既に
	// 待ち受けているから**である。つまり「bind 失敗が起きた」という事実そのものが、
	// 兄弟サーバが待ち受けを開始していたことの証明になる（順序は偶然ではなく因果）。
	//
	// 「空きポート 1 本 + 別途占有しておいたポート 1 本」という組み方だと、占有側の
	// bind 失敗は即座に起きるため、健全側が bind する前に停止処理へ入り得る。その場合
	// 「停止している」は「そもそも起動していなかった」でも満たせてしまい、EMPTY 側が
	// 空虚になる（何も停止させない壊れた実装でも通ってしまう）。
	addr := freeAddr(t)

	// ctx はキャンセルしない。停止の契機はサーバのエラーだけである。
	done := make(chan error, 1)
	go func() {
		done <- serve.Run(context.Background(), discardLogger(),
			serve.Server{Name: "競合A", Addr: addr, Handler: okHandler()},
			serve.Server{Name: "競合B", Addr: addr, Handler: okHandler()},
		)
	}()

	err := waitRun(t, done)

	// PRESENT: bind に失敗した側のエラーが呼び出し元へ返る（握り潰されない）。
	// かつ、このエラーが返ったこと自体が「もう 1 本が待ち受けていた」ことの証拠である。
	require.Error(t, err, "bind の失敗は Run の戻り値として返るべき")
	assert.Contains(t, err.Error(), addr, "bind に失敗したアドレスがエラーに含まれるべき")

	// EMPTY: 待ち受けていた兄弟サーバは、プロセス終了任せに放置されず確実に停止している。
	// 片方だけでは壊れた実装でも満たせるため、この 2 点を同じ観測で同時に確認する。
	//
	// まず「待ち受けていないこと」を窓で見る（停止させ忘れた側は bind が数ミリ秒遅れても
	// その直後に待ち受けを始めるため、この窓で捕まる）。
	requireStopped(t, addr)
	// そのうえで自分で bind できることまで確認する。これで「無関係のプロセスがポートを
	// 握っていて、ランナーは 2 本とも bind に失敗していた」という空虚な成立経路も潰れる。
	requireRebindable(t, addr)
}

// --- ケース 4: shutdown 途中失敗でも残りを続行する ---------------------------------

func TestRun_ShutdownFailureDoesNotSkipRemainingServers(t *testing.T) {
	healthyAddr := freeAddr(t)

	// 1 本目の Shutdown が猶予切れで失敗する状況を作る。2 本目は健全。
	err := runWithStuckShutdown(t, serve.Server{Name: "健全", Addr: healthyAddr, Handler: okHandler()})

	// PRESENT: 1 本目の失敗が戻り値として現れる。
	require.ErrorIs(t, err, context.DeadlineExceeded, "1 本目の Shutdown は猶予切れで失敗すべき")
	// EMPTY: それでも 2 本目には Shutdown が呼ばれ、待ち受けを残していない。
	// 2 本目が起動していたことは runWithStuckShutdown の waitServing で確認済みなので、
	// この EMPTY 側が空虚に満たされることはない。
	requireStopped(t, healthyAddr)
	requireRebindable(t, healthyAddr)
}

// --- ケース 5: 戻り値の優先順位（3 分岐）------------------------------------------

func TestRun_ReturnValuePrecedence(t *testing.T) {
	t.Run("異常系: サーバエラーがあればそれを返す", func(t *testing.T) {
		busyAddr := occupiedAddr(t)
		done := make(chan error, 1)
		go func() {
			done <- serve.Run(context.Background(), discardLogger(),
				serve.Server{Name: "失敗", Addr: busyAddr, Handler: okHandler()})
		}()
		err := waitRun(t, done)
		require.Error(t, err, "サーバエラーは返るべき")
		assert.Contains(t, err.Error(), busyAddr, "返るのは bind 失敗そのもの")
	})

	t.Run("異常系: サーバエラーが無ければ最初の shutdown エラーを返す", func(t *testing.T) {
		err := runWithStuckShutdown(t)
		require.ErrorIs(t, err, context.DeadlineExceeded, "shutdown の猶予切れが返るべき")
	})

	t.Run("正常系: どちらも無ければ nil を返す", func(t *testing.T) {
		addr := freeAddr(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		done := make(chan error, 1)
		go func() {
			done <- serve.Run(ctx, discardLogger(), serve.Server{Name: "公開", Addr: addr, Handler: okHandler()})
		}()
		waitServing(t, addr)
		cancel()
		require.NoError(t, waitRun(t, done), "正常停止では nil")
	})
}

// --- ケース 6: 既定値の集約（ReadHeaderTimeout）------------------------------------

func TestNewHTTPServers_AppliesDefaultTimeouts(t *testing.T) {
	specs := []serve.Server{
		{Name: "公開", Addr: ":8080", Handler: okHandler()},
		{Name: "内部", Addr: ":8081", Handler: okHandler()},
	}

	srvs := serve.NewHTTPServers(specs)

	require.Len(t, srvs, len(specs), "仕様の本数だけ *http.Server を組むべき")
	for i, srv := range srvs {
		assert.Equal(t, specs[i].Addr, srv.Addr, "%d 本目の Addr", i+1)
		assert.NotNil(t, srv.Handler, "%d 本目の Handler", i+1)
		// 素通りすると Slowloris 対策が静かに失われるため、既定値の適用を明示的に見る。
		assert.Equal(t, serve.DefaultReadHeaderTimeout, srv.ReadHeaderTimeout,
			"%d 本目に ReadHeaderTimeout の既定値が入るべき", i+1)
	}

	// 既定値そのものを固定する（I-7: 抽出で「切りの良い数字」へ動かさない）。
	assert.Equal(t, 10*time.Second, serve.DefaultReadHeaderTimeout, "ヘッダ読み取り猶予は 10 秒")
	assert.Equal(t, 15*time.Second, serve.DefaultShutdownTimeout, "シャットダウン猶予は 15 秒")
}
