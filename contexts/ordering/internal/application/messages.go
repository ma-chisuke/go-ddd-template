package application

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/example/go-ddd-template/contexts/ordering/internal/domain/order"
	"github.com/example/go-ddd-template/shared/id"
	"github.com/example/go-ddd-template/shared/outbox"
)

// 送信するクロスコンテキストメッセージの種別（message_type）。
// これらは注文コンテキストが公開する「契約」の識別子であり、在庫コンテキストの購読ポリシ
// （OnConfirmReservation / OnOrderCancelled）が受信して処理する。契約の正本は
// contracts/events/ に置く JSON スキーマである。相手コンテキストへは Go の型ではなく、
// 下記の翻訳済みペイロード（JSON）だけを渡す。
const (
	// MessageTypeConfirmReservation は「予約を確定してほしい」というクロスコンテキスト
	// コマンドの種別。注文が durable になったことを受けて確定へ進める（二相予約の第 2 相）。
	MessageTypeConfirmReservation = "ordering.reservation.confirm_requested"

	// MessageTypeOrderCancelled は「注文が取り消された」というドメインイベントの種別。
	// 在庫側はこれを受けて予約を解放する。
	MessageTypeOrderCancelled = "ordering.order.cancelled"
)

// reservationRefPayload は上記いずれのメッセージも運ぶ最小の公開契約（翻訳済み契約）。
// 在庫側がデコードするのは reservation_ref だけであり、order_id は運用時の相関・可観測性の
// ために添える参考情報（在庫側は未知フィールドとして読み飛ばす）。
type reservationRefPayload struct {
	ReservationRef string `json:"reservation_ref"`
	OrderID        string `json:"order_id,omitempty"`
}

// confirmReservationMessage は予約確定コマンド（ConfirmReservation）を組み立てる。
//
// これは **アプリケーション層が構築してアウトボックスへ直接書く「コマンド」** であり、
// ドメインが raise したイベントを collect → dispatch する経路（PullEvents）は通らない。
// 注文が durable になれば、このコマンドは at-least-once で必ず在庫側へ届く。
func confirmReservationMessage(ref order.ReservationRef, traceID string) (outbox.Message, error) {
	payload, err := json.Marshal(reservationRefPayload{ReservationRef: ref.String()})
	if err != nil {
		return outbox.Message{}, fmt.Errorf("予約確定コマンドの組み立てに失敗しました: %w", err)
	}
	return outbox.Message{
		ID:         id.New(),
		Type:       MessageTypeConfirmReservation,
		Payload:    payload,
		TraceID:    traceID,
		OccurredAt: time.Now().UTC(),
	}, nil
}

// toOutboxMessage はドメインが raise したクロスコンテキストイベントを、翻訳済み契約の
// アウトボックスメッセージへ変換する。プロセス内のみのイベント（OrderPlaced など）には
// クロスコンテキストの経路が無いため ok=false を返す（呼び出し側は読み飛ばす）。
//
// これにより「ドメインが append したイベント（OrderCancelled）を PullEvents で collect し、
// 機構経由でアウトボックスへ積む」経路が保たれる（手組みメッセージの近道は取らない）。
func toOutboxMessage(e order.DomainEvent, traceID string) (outbox.Message, bool, error) {
	switch ev := e.(type) {
	case order.OrderCancelled:
		payload, err := json.Marshal(reservationRefPayload{
			ReservationRef: ev.ReservationRef,
			OrderID:        ev.OrderID,
		})
		if err != nil {
			return outbox.Message{}, false, fmt.Errorf("注文取消イベントの変換に失敗しました: %w", err)
		}
		return outbox.Message{
			ID:         id.New(),
			Type:       MessageTypeOrderCancelled,
			Payload:    payload,
			TraceID:    traceID,
			OccurredAt: ev.OccurredAt(),
		}, true, nil
	default:
		// クロスコンテキストの購読者を持たないイベント（v1 の OrderPlaced）は
		// アウトボックスへは積まない（プロセス内ディスパッチャが扱う）。
		return outbox.Message{}, false, nil
	}
}
