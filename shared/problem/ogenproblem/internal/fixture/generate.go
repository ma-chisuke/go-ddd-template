// Package fixture はコード生成の指示だけを持つ薄いパッケージ。抽出アルゴリズムの
// テスト専用 OpenAPI 契約（openapi.yaml）から、ogen のサーバコードを oas/ へ生成する。
//
//	cd shared && go generate ./...
//
// internal/ 配下に置いているので shared の公開 API を汚さず、外部モジュールからは
// 参照できない。カバレッジゲート（domain + application 限定）の対象外でもあるため、
// 生成コードが 80% 閾値を薄めることもない（internal/mock と同じ扱い）。
//
// 生成物はコミットし、手で編集しない（制約 C-3）。CI では再生成して差分が出ないこと
// （冪等性）を検証する。
package fixture

//go:generate go tool ogen --config .ogen.yaml --target oas --package oas --clean openapi.yaml
