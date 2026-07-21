// Package application はユースケース（アプリケーションサービス）と、それが依存する
// ポート（インターフェース）を定義する。ヘキサゴナルアーキテクチャにおける
// アプリケーション層であり、ドメイン層のオーケストレーションを担うが、業務ルールそのものは
// ドメイン層に置く。永続化やトランザクションの具体的な実装はここには持たず、
// ポートを通じて infrastructure 層のアダプタへ委譲する。
package application

import (
	"context"

	"github.com/example/go-ddd-template/contexts/inventory/internal/domain/inventory"
	"github.com/example/go-ddd-template/shared/uow"
)

// StockStore は在庫項目の読み書きを抽象化するポート。
// 実装（アダプタ）は infrastructure 層に置く（インメモリ版と PostgreSQL 版）。
type StockStore interface {
	// Load は SKU に対応する在庫項目を読み込む。存在しない場合は
	// inventory.ErrStockItemNotFound を返す。
	Load(ctx context.Context, sku inventory.SKU) (*inventory.StockItem, error)

	// Save は 1 つ以上の在庫項目を永続化する。楽観的排他制御の版が一致しない場合は
	// uow.ErrConcurrencyConflict を返す。可変長引数にしているのは、将来的に
	// 同一トランザクションで複数集約をまとめて保存できるようにするため。
	Save(ctx context.Context, items ...*inventory.StockItem) error
}

// Repos はひとつのトランザクションに束ねられたリポジトリの束。
// ユースケースはこの束からのみリポジトリを取得するため、トランザクション外の
// 書き込みが構造的に起こり得ない。このスライスでは在庫ストアのみを持つ。
type Repos interface {
	Stock() StockStore
}

// UnitOfWork はこのコンテキスト用に Repos で特殊化した作業単位。
// 実装は infrastructure 層に置く（インメモリ版と pgx 版）。
type UnitOfWork = uow.UnitOfWork[Repos]
