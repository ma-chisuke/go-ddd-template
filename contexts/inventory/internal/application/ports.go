// Package application はユースケース（アプリケーションサービス）と、それが依存する
// ポート（インターフェース）を定義する。ヘキサゴナルアーキテクチャにおける
// アプリケーション層であり、ドメイン層のオーケストレーションを担うが、業務ルールそのものは
// ドメイン層に置く。永続化やトランザクションの具体的な実装はここには持たず、
// ポートを通じて送信アダプタ（adapter/outbound）へ委譲する。
package application

import (
	"context"
	"time"

	"github.com/example/go-ddd-template/contexts/inventory/internal/domain/inventory"
	"github.com/example/go-ddd-template/shared/outbox"
	"github.com/example/go-ddd-template/shared/uow"
)

// StockStore は在庫項目の読み書きを抽象化するポート。
// 実装（アダプタ）は adapter/outbound 層に置く（インメモリ版と PostgreSQL 版）。
type StockStore interface {
	// Load は SKU に対応する在庫項目を読み込む。存在しない場合は
	// inventory.ErrStockItemNotFound を返す。
	Load(ctx context.Context, sku inventory.SKU) (*inventory.StockItem, error)

	// LoadMany は複数の SKU に対応する在庫項目をまとめて読み込む。マルチ SKU 予約で用いる。
	// 見つからない SKU があった場合の扱いは実装に委ねず、ドメインサービス側の事前検証で
	// ErrStockItemNotFound として扱う（存在した項目のみを返す）。
	LoadMany(ctx context.Context, skus []inventory.SKU) ([]*inventory.StockItem, error)

	// LoadByReservation は指定の予約参照を持つ「全て」の在庫項目を読み込む。
	// マルチ SKU 予約では同一 ref が複数の StockItem に跨るため、Confirm / Release は
	// これで全項目をロードし、1 つの作業単位で原子的に遷移させる（部分適用による
	// 孤児 pending の誤 Reap を防ぐ）。
	LoadByReservation(ctx context.Context, ref inventory.ReservationRef) ([]*inventory.StockItem, error)

	// LoadExpiredPending は、before 時点で期限切れの pending 予約を持つ在庫項目を
	// 最大 limit 件返す（Reaper 用）。confirmed 予約は対象にしない。
	LoadExpiredPending(ctx context.Context, before time.Time, limit int) ([]*inventory.StockItem, error)

	// Save は 1 つ以上の在庫項目を永続化する（予約状態を含む）。楽観的排他制御の版が
	// 一致しない場合は uow.ErrConcurrencyConflict を返す。可変長引数にしているのは、
	// マルチ SKU 予約のように複数集約を同一トランザクションでまとめて保存するため。
	Save(ctx context.Context, items ...*inventory.StockItem) error
}

// MessagePublisher は、集約書き込みと同一トランザクションでアウトボックスへメッセージを
// 積む送信ポート。クロスコンテキストへの送信（イベント／コマンド）に使う。
//
// このスライスの在庫コンテキストは、まだクロスコンテキストへの送信を行わない
// （StockDepleted も発行＋ログのみ）。したがってユースケースからは呼ばれないが、
// トランザクショナルアウトボックスの構造（集約書き込みと同一 UoW で Enqueue する）を
// 示すためにポートと結線を用意している。
type MessagePublisher interface {
	Enqueue(ctx context.Context, m outbox.Message) error
}

// Repos はひとつのトランザクションに束ねられたリポジトリの束。
// ユースケースはこの束からのみリポジトリを取得するため、トランザクション外の
// 書き込みが構造的に起こり得ない。在庫ストアとアウトボックスを、同一トランザクションに
// 束ねて提供する（集約の保存とメッセージ Enqueue が原子的にコミットされる）。
type Repos interface {
	Stock() StockStore
	Outbox() MessagePublisher
}

// UnitOfWork はこのコンテキスト用に Repos で特殊化した作業単位。
// 実装は adapter/outbound 層に置く（インメモリ版と pgx 版）。
type UnitOfWork = uow.UnitOfWork[Repos]

// collectEvents は複数の在庫項目に蓄積されたドメインイベントをまとめて取り出す。
// 各ユースケースは作業単位の成功後、これで集めたイベントを配信する。
func collectEvents(items []*inventory.StockItem) []inventory.DomainEvent {
	var events []inventory.DomainEvent
	for _, it := range items {
		events = append(events, it.PullEvents()...)
	}
	return events
}
