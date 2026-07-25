package serve

// このファイルは、外部テストパッケージ（serve_test）からは届かない内部シンボルを
// テストにだけ公開する薄い橋である。実装は 1 行もなく、別名を与えるだけである。
//
// なぜこの形にするのか（CONVENTIONS.md の B-5 / docs/testing-conventions.md の C-4）:
//
// このパッケージのテストは 6 ケースあり、そのうち 3 ケースは公開 API だけでは
// 決定的に再現・観測できない。
//
//   - シャットダウン猶予切れ（「途中失敗でも残りを続行」と戻り値の firstShutdownErr 分岐）は
//     既定の 15 秒を待たずに再現する必要があり、猶予を引数で受ける内部の run を呼ぶ。
//   - ReadHeaderTimeout の適用は、ランナーが組む *http.Server をそのまま見る必要があり、
//     newHTTPServers を呼ぶ。
//
// 素直に書くと「内部テスト（package serve）に 3 ケース」と「外部テスト（package serve_test）に
// 3 ケース」の 2 ファイルに割れるが、両者は同じディレクトリの別パッケージなので
// **非公開ヘルパーを共有できない**。ヘルパーは 9 個あり（アドレス確保・待ち受け確認・
// 停止確認・再 bind 確認など）、その全部が両側から必要になる。2 ファイルに割ると
// 約 90 行を重複させることになり、「分離のために重複を作る」という悪い見本になる。
//
// Go 標準ライブラリの定石はこの export_test.go である。テスト本体は全部を外部テスト
// パッケージへ寄せられるので、ヘルパーは非公開のまま 1 箇所に置ける。testpackage linter の
// 既定 skip-regexp が (export|internal)_test\.go を除外するので、ツール既定とも噛み合う。
//
// export_test.go は go test のときだけコンパイルされる。したがってここで公開しても
// 本番のビルドには 1 バイトも混ざらない（公開 API は Run と Server だけのまま）。

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

// RunWithShutdownTimeout は内部の run をテストへ公開する。Run はシャットダウン猶予を
// 既定値（15 秒）で固定するため、猶予切れの分岐をテストから再現できない。
var RunWithShutdownTimeout = func(
	ctx context.Context,
	log *slog.Logger,
	shutdownTimeout time.Duration,
	servers ...Server,
) error {
	return run(ctx, log, shutdownTimeout, servers...)
}

// NewHTTPServers は内部の newHTTPServers をテストへ公開する。ランナーが組み立てる
// *http.Server にタイムアウトが載っていることを、起動せずに検証するために使う。
var NewHTTPServers = func(specs []Server) []*http.Server {
	return newHTTPServers(specs)
}
