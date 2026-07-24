// shared モジュールはドメインに依存しない汎用部品だけを収める。
// ここには特定コンテキストのドメイン値オブジェクト（SKU や Quantity など）を
// 置いてはならない。置いてよいのはトランザクション機構（uow）、
// 相関 ID（correlation）、ID 生成（id）といった、どのコンテキストからでも
// 安全に共有できる技術的な建材だけである。
module github.com/example/go-ddd-template/shared

go 1.26.0

require (
	github.com/jackc/pgx/v5 v5.10.0
	github.com/stretchr/testify v1.11.1
)

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/rogpeppe/go-internal v1.15.0 // indirect
	golang.org/x/sync v0.17.0 // indirect
	golang.org/x/text v0.29.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
