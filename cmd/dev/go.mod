// cmd/dev は両コンテキストを 1 プロセスに結線する開発・テスト用ハーネスのモジュール。
// go.work の独立モジュールであり、公開ファサード（inventory / ordering）+ 公開 port +
// shared だけを import する。Go の internal パッケージ規則により、各コンテキストの
// internal/ 配下へはコンパイル時に到達できない（構造的な境界）。
//
// これは dev/test 専用であり、出荷ランタイムではない。本番は各コンテキストを
// 独立したサービス（コンテナ）として動かす（docker-compose を参照）。
module github.com/example/go-ddd-template/cmd/dev

go 1.25.0

require (
	github.com/example/go-ddd-template/contexts/inventory v0.0.0
	github.com/example/go-ddd-template/contexts/ordering v0.0.0
	github.com/example/go-ddd-template/shared v0.0.0
	github.com/stretchr/testify v1.11.1
)

require (
	github.com/go-faster/errors v0.7.1 // indirect
	github.com/go-faster/jx v1.2.0 // indirect
	github.com/jackc/pgx/v5 v5.10.0 // indirect
	github.com/ogen-go/ogen v1.23.0 // indirect
)

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/dlclark/regexp2 v1.12.0 // indirect
	github.com/fatih/color v1.19.0 // indirect
	github.com/ghodss/yaml v1.0.0 // indirect
	github.com/go-faster/yaml v0.4.6 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.22 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
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
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

// 同一リポジトリ内の未公開モジュールを相対パスへ解決する（go.work でもこの replace でも解決可）。
replace github.com/example/go-ddd-template/shared => ../../shared

replace github.com/example/go-ddd-template/clients/inventory => ../../clients/inventory

replace github.com/example/go-ddd-template/contexts/inventory => ../../contexts/inventory

replace github.com/example/go-ddd-template/contexts/ordering => ../../contexts/ordering
