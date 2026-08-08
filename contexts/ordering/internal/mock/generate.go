// Package mock は application 層のポート（interface）から生成した gomock のモックを収める
// （テスト専用の生成コード）。mock_application.go は下記の go:generate 指示で再生成し、手で
// 編集しない（ポートを変更したら再生成する）。CI は再生成して差分が出ないこと（冪等性）を検証する。
//
// 配置場所の意図: 生成モックはカバレッジ計測の対象（domain + application）を汚さないよう、
// application パッケージの外（internal/mock）へ置く。テストのないパッケージはカバレッジ 0% として
// 合算され計測を押し下げるため、計測グロブ ./internal/application/... の外に出している。
//
// import 循環は生じない（mock は application を import するが、application は mock を import しない）。
// テストは外部テストパッケージ（application_test / httpapi_test）から mock を使う。mockgen は
// go.mod の tool 指示で導入しているため、グローバルなインストールは不要（go tool mockgen で解決する）。
//
// 生成は -typed（型付きレコーダ）モードで行う。EXPECT().Method(...) は *gomock.Call ではなく
// メソッドごとの型付き *MethodCall を返し、Return / DoAndReturn の引数がコンパイル時に検査される。
// inventory コンテキストの mock と生成モードを揃えている。
package mock

//go:generate go tool mockgen -typed -destination mock_application.go -package mock github.com/example/go-ddd-template/contexts/ordering/internal/application StockReserver,OrderStore,ShipmentStore,MessagePublisher,EventDispatcher,Repos
