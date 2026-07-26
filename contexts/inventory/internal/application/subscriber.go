package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/example/go-ddd-template/contexts/inventory/internal/domain"
	"github.com/example/go-ddd-template/shared/outbox"
)

// 受信するクロスコンテキストメッセージの種別（message_type）。
// これらは相手コンテキスト（注文側）が公開する「契約」の識別子であり、在庫側は相手の
// Go 型ではなく、この契約（下記 JSON スキーマ）だけをデコードする。相手コンテキストの
// 実装（送信側）は後続のスライスで追加されるが、在庫側の受信ポリシと内部エンドポイントは
// 先に用意しておく。
const (
	// MessageTypeConfirmReservation は「予約を確定してほしい」というクロスコンテキスト
	// コマンドの種別。注文が durable になったことを受けて確定へ進める（二相予約の第 2 相）。
	MessageTypeConfirmReservation = "ordering.reservation.confirm_requested"

	// MessageTypeOrderCancelled は「注文が取り消された」というドメインイベントの種別。
	// これを受けて予約を解放する。
	MessageTypeOrderCancelled = "ordering.order.cancelled"
)

// reservationRefPayload は上記いずれのメッセージも運ぶ最小の公開契約。
// 相手側の OrderID は境界（腐敗防止層）で在庫側の予約参照へ翻訳済みであり、ここには
// 不透明な相関 ID（reservation_ref）だけが載る。
type reservationRefPayload struct {
	ReservationRef string `json:"reservation_ref"`
}

func decodeReservationRef(m outbox.Message) (string, error) {
	var p reservationRefPayload
	if err := json.Unmarshal(m.Payload, &p); err != nil {
		return "", fmt.Errorf("メッセージ %q のペイロード解釈に失敗しました: %w", m.ID, err)
	}
	if p.ReservationRef == "" {
		return "", fmt.Errorf("メッセージ %q に reservation_ref がありません", m.ID)
	}
	return p.ReservationRef, nil
}

// OnConfirmReservation は予約確定コマンドの Consumer を返す。公開契約をデコードして
// Confirm を呼ぶ。有効な予約が無い場合（速い取消による解放済み、または TTL 失効による
// Reap 済み）は良性の冪等 no-op として扱い、trace_id 付きの警告ログを残す。
//
// 分散トポロジでは、この ErrReservationNotFound が「期待された速い取消」なのか「真の
// 不整合」なのかをコードで判別しない。両サービスのログを共有 trace_id で相関する運用
// （オペレータ／観測性レベル）で整合させる（コードレベルの逆照会は逆方向イベントを要し、
// このスライスの対象外）。
func OnConfirmReservation(confirm *Confirmer, log *slog.Logger) outbox.Consumer {
	return func(ctx context.Context, m outbox.Message) error {
		ref, err := decodeReservationRef(m)
		if err != nil {
			return err
		}
		err = confirm.Confirm(ctx, ref)
		if errors.Is(err, domain.ErrReservationNotFound) {
			log.WarnContext(ctx, "確定対象の有効な予約がありません（速い取消 or TTL 失効の可能性・良性 no-op）",
				slog.String("ref", ref),
				slog.String("trace_id", m.TraceID),
			)
			return nil
		}
		return err
	}
}

// OnOrderCancelled は注文取消イベントの Consumer を返す。公開契約をデコードして Release を
// 呼ぶ。Release は冪等なので at-least-once の再配送のもとでも安全。
func OnOrderCancelled(release *Releaser) outbox.Consumer {
	return func(ctx context.Context, m outbox.Message) error {
		ref, err := decodeReservationRef(m)
		if err != nil {
			return err
		}
		return release.Release(ctx, ref)
	}
}
