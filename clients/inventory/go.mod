// clients/inventory は在庫コンテキストの「内部 API」から ogen が生成した Go クライアント
// モジュール。注文コンテキストの腐敗防止層（ACL）がこれを import して在庫サービスへ
// HTTP 越しに到達する。ここには手書きのロジックは無く、生成された翻訳済み契約型と
// クライアントメソッドだけを収める（生成物はコミットし、手で編集しない）。
//
// このクライアントは在庫の内部 OpenAPI 契約（翻訳済み契約）にのみ依存し、
// contexts/inventory の Go パッケージには一切依存しない。両者はこの契約を介してのみ
// 結合し、独立して進化できる。
module github.com/example/go-ddd-template/clients/inventory

go 1.25.0

require (
	github.com/go-faster/errors v0.7.1
	github.com/go-faster/jx v1.2.0
	github.com/ogen-go/ogen v1.23.0
)

require (
	github.com/dlclark/regexp2 v1.12.0 // indirect
	github.com/fatih/color v1.19.0 // indirect
	github.com/ghodss/yaml v1.0.0 // indirect
	github.com/go-faster/yaml v0.4.6 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.22 // indirect
	github.com/segmentio/asm v1.2.1 // indirect
	github.com/shopspring/decimal v1.4.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.28.0 // indirect
	golang.org/x/exp v0.0.0-20230725093048-515e97ebf090 // indirect
	golang.org/x/net v0.57.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.40.0 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
)
