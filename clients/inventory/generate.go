// Package inventory はコード生成の指示だけを持つ薄いパッケージ。リポジトリのルートで
//
//	cd clients/inventory && go generate ./...
//
// を実行すると、ogen が在庫の内部 API 契約から Go クライアントを invclient パッケージへ
// 生成する。サーバ生成を無効化した「クライアントのみ」の生成設定（client.ogen.yaml）を
// 使う。生成物はコミットし、手で編集しない。CI では生成後に差分が出ないこと（冪等性）を
// 検証する。
//
// パスはこのファイルのあるディレクトリ（モジュールルート）からの相対。
package inventory

//go:generate go tool ogen --config ../../contracts/api/inventory/client.ogen.yaml --target invclient --package invclient --clean ../../contracts/api/inventory/internal.openapi.yaml
