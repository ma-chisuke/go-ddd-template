// Package application はユースケース（アプリケーションサービス）と、それが依存する
// ポート（インターフェース）を定義する。ヘキサゴナルアーキテクチャにおける
// アプリケーション層であり、ドメイン層のオーケストレーションを担うが、業務ルールそのものは
// ドメイン層に置く。永続化やトランザクションの具体的な実装はここには持たず、
// ポートを通じて送信アダプタ（adapter/outbound）へ委譲する。
package application

import (
	"context"
	"time"

	"github.com/example/go-ddd-template/contexts/inventory/internal/domain"
	"github.com/example/go-ddd-template/shared/outbox"
	"github.com/example/go-ddd-template/shared/uow"
)

// --- UoW に束ねられるポート ---------------------------------------------------
//
// Repos 経由でのみ取得し、トランザクションの内側で使う。集約の保存とメッセージの
// Enqueue が同一トランザクションで原子的にコミットされる（二重書き込みを避ける）。

// StockStore は在庫項目の読み書きを抽象化するポート。
// 実装（アダプタ）は adapter/outbound 層に置く（インメモリ版と PostgreSQL 版）。
type StockStore interface {
	// Load は SKU に対応する在庫項目を読み込む。存在しない場合は
	// domain.ErrStockItemNotFound を返す。
	Load(ctx context.Context, sku domain.SKU) (*domain.StockItem, error)

	// LoadMany は複数の SKU に対応する在庫項目をまとめて読み込む。マルチ SKU 予約で用いる。
	// 見つからない SKU があった場合の扱いは実装に委ねず、ドメインサービス側の事前検証で
	// ErrStockItemNotFound として扱う（存在した項目のみを返す）。
	LoadMany(ctx context.Context, skus []domain.SKU) ([]*domain.StockItem, error)

	// LoadByReservation は指定の予約参照を持つ「全て」の在庫項目を読み込む。
	// マルチ SKU 予約では同一 ref が複数の StockItem に跨るため、Confirm / Release は
	// これで全項目をロードし、1 つの作業単位で原子的に遷移させる（部分適用による
	// 孤児 pending の誤 Reap を防ぐ）。
	LoadByReservation(ctx context.Context, ref domain.ReservationRef) ([]*domain.StockItem, error)

	// LoadExpiredPending は、before 時点で期限切れの pending 予約を持つ在庫項目を
	// 最大 limit 件返す（Reaper 用）。confirmed 予約は対象にしない。
	LoadExpiredPending(ctx context.Context, before time.Time, limit int) ([]*domain.StockItem, error)

	// Save は 1 つ以上の在庫項目を永続化する（予約状態を含む）。楽観的排他制御の版が
	// 一致しない場合は uow.ErrConcurrencyConflict を返す。可変長引数にしているのは、
	// マルチ SKU 予約のように複数集約を同一トランザクションでまとめて保存するため。
	Save(ctx context.Context, items ...*domain.StockItem) error
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

// --- UoW の外で呼ぶポート -----------------------------------------------------
//
// プロセス内のイベント配信（EventDispatcher）と時刻の供給（Clock）。どちらも
// トランザクションには載らないので Repos には含めず、ユースケースは UoW の外側で
// これらを呼ぶ。

// EventDispatcher はドメインイベントをプロセス内で配信するポート。
// ユースケースは永続化の成功後にのみ、このポートを通じてイベントを配信する。
// エラーを返さないのは、これが後処理であり、コミット済みのトランザクションを
// 巻き戻せないためである（ハンドラのエラーは実装側がログに残す）。
//
// 実装は共有モジュールの型付きディスパッチャ event.Typed[domain.DomainEvent] が提供し、
// 合成ルート（inventory.go）で結線する。ポートはこのコンテキストのドメイン型で宣言され、
// 実装は共有機構 — 機構は共有し、型はコンテキスト固有に保つ、という境界の引き方である。
//
// このスライスでは購読側はログ出力／記録が中心で、外部への非同期配信は行わない。
// より強い配信保証が必要になれば、アウトボックス方式（shared/outbox）へ差し替えられるよう、
// このポートを挟んでいる。
type EventDispatcher interface {
	Dispatch(ctx context.Context, events ...domain.DomainEvent)
}

// Clock は現在時刻を供給するポート。本番は実時間、テストは擬似時計を注入することで、
// 時間依存の掃除処理（Reaper）を決定的にテストできるようにする。
type Clock interface {
	Now() time.Time
}
