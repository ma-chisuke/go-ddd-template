// shared モジュールはドメインに依存しない汎用部品だけを収める。
// ここには特定コンテキストのドメイン値オブジェクト（SKU や Quantity など）を
// 置いてはならない。置いてよいのはトランザクション機構（uow）、
// 相関 ID（correlation）、ID 生成（id）といった、どのコンテキストからでも
// 安全に共有できる技術的な建材だけである。
module github.com/example/go-ddd-template/shared

go 1.25.0

require github.com/stretchr/testify v1.11.1

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
