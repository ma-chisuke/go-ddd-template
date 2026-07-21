// Package mock は application 層のポート（interface）の gomock 生成モックを収める。
//
// ここに置く型は uber-go/mock（mockgen）が `go generate ./...` で生成する
// コミット済みの生成物であり、手で編集しない（ポート＝契約を変えたら再生成する）。
// モックはアプリケーション層のポート相互作用（use case がポートを正しく呼ぶか）を
// 検証するテストからのみ使う。ドメイン・アプリケーションの本番コードはこのパッケージを
// import しない（依存の向きを内側に保つ）。
//
// なぜインメモリアダプタ（adapter/outbound/memory）と別に用意するのか:
//   - memory は「本物の送信アダプタ」で、擬似トランザクションや楽観的排他制御まで含めた
//     統合的な振る舞いを DB 非依存で高速に検証するために使う。
//   - こちらの gomock モックは「ポートを狙い撃ちで検証する」ための道具で、
//     「Load はこの SKU で 1 回だけ呼ばれ、その後 Save が呼ばれる」といった
//     相互作用そのものを EXPECT で表明する用途に使う。
//
// 生成コマンドは reflect（パッケージ）モードで、application パッケージの 5 つのポートを
// まとめて 1 ファイルへ出力する。`go tool mockgen` を用いるため、mockgen をグローバルに
// インストールする必要はない（go.mod の tool ディレクティブで解決される）。
//
// 配置場所の意図: 生成モックはカバレッジ計測の対象（domain + application）を汚さないよう、
// application パッケージの外（internal/mock）へ置く。テストのないパッケージはカバレッジ 0%
// として合算され計測を押し下げるため、計測グロブ ./internal/application/... の外に出している。
package mock

//go:generate go tool mockgen -typed -destination mock_application.go -package mock github.com/example/go-ddd-template/contexts/inventory/internal/application StockStore,MessagePublisher,Clock,EventDispatcher,Repos
