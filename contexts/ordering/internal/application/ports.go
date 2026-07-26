// Package application はユースケース（アプリケーションサービス）と、それが依存する
// ポート（インターフェース）を定義する。ヘキサゴナルアーキテクチャにおける
// アプリケーション層であり、ドメイン層のオーケストレーションを担うが、業務ルールそのものは
// ドメイン層に置く。永続化やトランザクション・在庫サービスへの同期呼び出しの具体的な実装は
// ここには持たず、ポートを通じて送信アダプタ（adapter/outbound）へ委譲する。
package application

import (
	"context"

	"github.com/example/go-ddd-template/contexts/ordering/internal/domain"
	"github.com/example/go-ddd-template/contexts/ordering/port"
	"github.com/example/go-ddd-template/shared/outbox"
	"github.com/example/go-ddd-template/shared/uow"
)

// ReserveLine は公開 DTO port.ReserveLine の別名。アプリケーション層内部では短く
// ReserveLine と書けるようにしつつ、実体は境界を跨げる公開型を指す。
type ReserveLine = port.ReserveLine

// OrderStore は注文集約の読み書きを抽象化するポート。
// 実装（アダプタ）は adapter/outbound 層に置く（インメモリ版と PostgreSQL 版）。
type OrderStore interface {
	// Load は注文 ID に対応する注文を読み込む。存在しない場合は
	// domain.ErrOrderNotFound を返す。
	Load(ctx context.Context, id domain.OrderID) (*domain.Order, error)

	// Save は注文を永続化する。楽観的排他制御の版が一致しない場合は
	// uow.ErrConcurrencyConflict を返す。
	Save(ctx context.Context, o *domain.Order) error
}

// MessagePublisher は、集約書き込みと同一トランザクションでアウトボックスへメッセージを
// 積む送信ポート。クロスコンテキストへの送信（コマンド ConfirmReservation / イベント
// OrderCancelled）に使う。UoW の内側で呼ぶことで、注文の保存とメッセージ Enqueue が
// 原子的にコミットされる（二重書き込みを避ける）。
type MessagePublisher interface {
	Enqueue(ctx context.Context, m outbox.Message) error
}

// Repos はひとつのトランザクションに束ねられたリポジトリの束。
// ユースケースはこの束からのみリポジトリを取得するため、トランザクション外の
// 書き込みが構造的に起こり得ない。注文ストアとアウトボックスを、同一トランザクションに
// 束ねて提供する。
//
// 注意: 在庫予約の ACL ポート（StockReserver）は tx に載らない外部同期呼び出しなので
// Repos には含めない。ユースケースは UoW の外で Reserver を呼ぶ（[acl.go] 参照）。
type Repos interface {
	Orders() OrderStore
	Outbox() MessagePublisher
}

// UnitOfWork はこのコンテキスト用に Repos で特殊化した作業単位。
// 実装は adapter/outbound 層に置く（インメモリ版と pgx 版）。
type UnitOfWork = uow.UnitOfWork[Repos]
