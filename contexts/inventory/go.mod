// inventory は「在庫」境界づけられたコンテキストのモジュール。
// ヘキサゴナルアーキテクチャ（ports and adapters）の 4 層を internal/ 配下に持ち、
// コンテキストルートに薄い公開ファサード（inventory.Module）を公開する。
// このモジュールはワークスペースから切り出して単独モジュールとしても利用できる。
module github.com/example/go-ddd-template/contexts/inventory

go 1.25.0

require (
	github.com/example/go-ddd-template/shared v0.0.0
	github.com/go-faster/errors v0.7.1
	github.com/go-faster/jx v1.2.0
	github.com/jackc/pgx/v5 v5.10.0
	github.com/ogen-go/ogen v1.23.0
	github.com/stretchr/testify v1.11.1
	go.uber.org/mock v0.6.0
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
	github.com/rogpeppe/go-internal v1.15.0 // indirect
	github.com/segmentio/asm v1.2.1 // indirect
	github.com/shopspring/decimal v1.4.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.28.0 // indirect
	golang.org/x/exp v0.0.0-20230725093048-515e97ebf090 // indirect
	golang.org/x/mod v0.38.0 // indirect
	golang.org/x/net v0.57.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.40.0 // indirect
	golang.org/x/tools v0.48.0 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

// shared モジュールは未公開のため、同一リポジトリ内の相対パスへ解決する。
// go.work（ワークスペースモード）でもこの replace（単一モジュールモード）でも
// ローカルの shared を参照でき、コンテナ内での単一モジュールビルドも成立する。
replace github.com/example/go-ddd-template/shared => ../../shared

tool go.uber.org/mock/mockgen
