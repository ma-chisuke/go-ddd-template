package inventory

// このファイルはコード生成の指示だけを持つ。リポジトリのルートで
//
//	cd contexts/inventory && go generate ./...
//
// を実行すると、ogen（OpenAPI からサーバ）と sqlc（SQL から型安全な Go）が走る。
// 生成物はコミットし、手で編集しない。CI では生成後に差分が出ないこと（冪等性）を検証する。
//
// パスはこのファイルのあるディレクトリ（コンテキストのモジュールルート）からの相対。

//go:generate go tool ogen --config ../../contracts/inventory/.ogen.yaml --target internal/adapter/inbound/openapi --package openapi --clean ../../contracts/inventory/openapi.yaml
//go:generate go tool ogen --config ../../contracts/inventory/internal.ogen.yaml --target internal/adapter/inbound/openapiinternal --package openapiinternal --clean ../../contracts/inventory/internal.openapi.yaml
//go:generate go tool sqlc generate
