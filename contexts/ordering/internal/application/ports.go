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

// --- UoW に束ねられるポート ---------------------------------------------------
//
// Repos 経由でのみ取得し、トランザクションの内側で使う。集約の保存とメッセージの
// Enqueue が同一トランザクションで原子的にコミットされる（二重書き込みを避ける）。

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

// ShipmentStore は出荷集約の読み書きを抽象化するポート。
// 実装（アダプタ）は adapter/outbound 層に置く（インメモリ版と PostgreSQL 版）。
type ShipmentStore interface {
	// Load は出荷 ID に対応する出荷を読み込む。存在しない場合は
	// domain.ErrShipmentNotFound を返す。
	Load(ctx context.Context, id domain.ShipmentID) (*domain.Shipment, error)

	// Save は出荷を永続化する。楽観的排他制御の版が一致しない場合は
	// uow.ErrConcurrencyConflict を返す。
	Save(ctx context.Context, s *domain.Shipment) error
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
// 書き込みが構造的に起こり得ない。集約ストアとアウトボックスを、同一トランザクションに
// 束ねて提供する。
//
// **アクセサは集約ルートと 1 対 1 に対応する**（Outbox は集約ではなくメッセージ配送ポート
// なので対象外）。この対応は検査 13 が双方向に機械強制している — 集約ルートでない型の
// ストアを足すことも、集約ルートのストアを足し忘れることもできない。
//
// **Shipments() が在るのは「Shipment の書き込みとアウトボックス投入を同一トランザクションで
// 行うため」であって、「Order と Shipment を一緒に書くため」ではない。** 出荷のユースケースは
// Orders() を使わない（注文はトランザクションの外で読むだけである）。
// 1 トランザクション 1 集約という指針はこの Repos のもとでも保たれている。
type Repos interface {
	Orders() OrderStore
	Shipments() ShipmentStore
	Outbox() MessagePublisher
}

// UnitOfWork はこのコンテキスト用に Repos で特殊化した作業単位。
// 実装は adapter/outbound 層に置く（インメモリ版と pgx 版）。
type UnitOfWork = uow.UnitOfWork[Repos]

// --- UoW の外で呼ぶポート -----------------------------------------------------
//
// 在庫サービスへの外部同期呼び出し（StockReserver）と、プロセス内のイベント配信
// （EventDispatcher）。どちらもトランザクションには載らないので Repos には含めず、
// ユースケースは UoW の外側でこれらを呼ぶ（HTTP 呼び出しが DB トランザクションを跨いで
// 保持されるアンチパターンを避けるため）。

// StockReserver は在庫コンテキストへの腐敗防止層（ACL）ポート。注文コンテキストは
// このポートを通じてのみ在庫を予約・解放し、在庫のドメイン型を一切知らない
// （翻訳済み DTO port.ReserveLine だけを渡す）。
//
// 重要（境界規則）: これは在庫サービスへの外部同期呼び出し（本番は HTTP）であり、
// トランザクションには載らない。したがって Repos には含めず、ユースケースは UoW の
// 外側でこのポートを呼ぶ（HTTP 呼び出しが DB トランザクションを跨いで保持されるアンチ
// パターンを避けるため）。実装は adapter/outbound 層に置く（本番は生成クライアント
// clients/inventory を用いる aclhttp、開発用は in-process アダプタ）。
type StockReserver interface {
	// Reserve は予約参照 ref に対して lines をまとめて予約する。予約が拒否された
	// （在庫不足）場合は ErrReservationRejected、在庫サービスが不達・タイムアウト・5xx の
	// 場合は ErrReservationUnavailable を返す（いずれも注文コンテキスト自身の番兵へ翻訳し、
	// 在庫側の番兵はそのまま漏らさない）。
	Reserve(ctx context.Context, ref string, lines []port.ReserveLine) error

	// Release は予約参照 ref の予約を解放する（保存失敗時の best-effort な補償解放）。
	// 冪等であり、未知の参照でも安全に呼べる。
	Release(ctx context.Context, ref string) error
}

// EventDispatcher はドメインイベントをプロセス内で配信するポート。
// ユースケースは永続化の成功後にのみ、このポートを通じてイベントを配信する。
// エラーを返さないのは、これが後処理であり、コミット済みのトランザクションを
// 巻き戻せないためである（ハンドラのエラーは実装側がログに残す）。
//
// 実装は共有モジュールの型付きディスパッチャ event.Typed[domain.DomainEvent] が提供し、
// 合成ルート（ordering.go）で結線する。ポートはこのコンテキストのドメイン型で宣言され、
// 実装は共有機構 — 機構は共有し、型はコンテキスト固有に保つ、という境界の引き方である。
//
// このポートが扱うのは「プロセス内のみ」のイベント（v1 の OrderPlaced）である。
// クロスコンテキストイベント（OrderCancelled）は、これとは別に、ユースケースが同一 UoW 内で
// 翻訳済み契約へ変換してアウトボックスへ積む（[messages.go] 参照）。
type EventDispatcher interface {
	Dispatch(ctx context.Context, events ...domain.DomainEvent)
}
