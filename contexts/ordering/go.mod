// ordering は「注文」境界づけられたコンテキストのモジュール。
// ヘキサゴナルアーキテクチャ（ports and adapters）の 4 層を internal/ 配下に持ち、
// コンテキストルートに薄い公開ファサード（ordering.Module）を公開する。
// このモジュールはワークスペースから切り出して単独モジュールとしても利用できる。
//
// 在庫コンテキストへは、生成クライアント clients/inventory を介して HTTP 越しにのみ
// 到達する（腐敗防止層）。contexts/inventory の Go パッケージは import しない。
module github.com/example/go-ddd-template/contexts/ordering

go 1.26.0

require (
	github.com/example/go-ddd-template/clients/inventory v0.0.0
	github.com/example/go-ddd-template/shared v0.0.0
	github.com/go-faster/errors v0.7.1
	github.com/go-faster/jx v1.2.0
	github.com/jackc/pgx/v5 v5.10.0
	github.com/ogen-go/ogen v1.23.0
	github.com/stretchr/testify v1.11.1
	go.uber.org/mock v0.6.0
)

require (
	cel.dev/expr v0.25.1 // indirect
	filippo.io/edwards25519 v1.1.1 // indirect
	github.com/antlr4-go/antlr/v4 v4.13.1 // indirect
	github.com/coreos/go-semver v0.3.1 // indirect
	github.com/cubicdaiya/gonp v1.0.4 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/dlclark/regexp2 v1.12.0 // indirect
	github.com/fatih/color v1.19.0 // indirect
	github.com/fatih/structtag v1.2.0 // indirect
	github.com/ghodss/yaml v1.0.0 // indirect
	github.com/go-faster/yaml v0.4.6 // indirect
	github.com/go-sql-driver/mysql v1.9.3 // indirect
	github.com/google/cel-go v0.28.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.22 // indirect
	github.com/ncruces/go-sqlite3 v0.32.0 // indirect
	github.com/ncruces/julianday v1.0.0 // indirect
	github.com/pganalyze/pg_query_go/v6 v6.2.2 // indirect
	github.com/pingcap/errors v0.11.5-0.20250523034308-74f78ae071ee // indirect
	github.com/pingcap/failpoint v0.0.0-20240528011301-b51a646c7c86 // indirect
	github.com/pingcap/log v1.1.0 // indirect
	github.com/pingcap/tidb/pkg/parser v0.0.0-20260418072757-ce92298d1124 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/riza-io/grpc-go v0.2.0 // indirect
	github.com/segmentio/asm v1.2.1 // indirect
	github.com/shopspring/decimal v1.4.0 // indirect
	github.com/spf13/cobra v1.10.2 // indirect
	github.com/spf13/pflag v1.0.10 // indirect
	github.com/sqlc-dev/doubleclick v1.0.0 // indirect
	github.com/sqlc-dev/sqlc v1.31.1 // indirect
	github.com/tetratelabs/wazero v1.11.0 // indirect
	github.com/wasilibs/go-pgquery v0.0.0-20250409022910-10ac41983c07 // indirect
	github.com/wasilibs/wazero-helpers v0.0.0-20240620070341-3dff1577cd52 // indirect
	github.com/yuin/goldmark v1.8.2 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.28.0 // indirect
	golang.org/x/exp v0.0.0-20250620022241-b7579e27df2b // indirect
	golang.org/x/mod v0.38.0 // indirect
	golang.org/x/net v0.57.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.40.0 // indirect
	golang.org/x/tools v0.48.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260120221211-b8f7ae30c516 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260120221211-b8f7ae30c516 // indirect
	google.golang.org/grpc v1.80.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
	gopkg.in/natefinch/lumberjack.v2 v2.2.1 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

// shared と clients/inventory は未公開のため、同一リポジトリ内の相対パスへ解決する。
// go.work（ワークスペースモード）でもこの replace（単一モジュールモード）でも
// ローカルのモジュールを参照でき、コンテナ内での単一モジュールビルドも成立する。
replace github.com/example/go-ddd-template/shared => ../../shared

replace github.com/example/go-ddd-template/clients/inventory => ../../clients/inventory

tool (
	github.com/ogen-go/ogen/cmd/ogen
	github.com/sqlc-dev/sqlc/cmd/sqlc
	go.uber.org/mock/mockgen
)
