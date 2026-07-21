// Package internalhttp は「内部」HTTP 受信アダプタ（inbound adapter＝駆動側）。
//
// 公開 API（補充・照会）とは別の、サービス間連携のためのエンドポイントを実装する。
// 予約・確定・解放と、クロスコンテキストメッセージの取り込み（event-ingest）を、
// アプリケーション層のユースケース／アウトボックス Router 呼び出しへ翻訳する。
// 業務ロジックはここには置かない（薄い変換層）。
//
// 注意: この「internal HTTP router（トランスポート＝ HTTP のルーティング）」と、
// outbox.Router（message_type ごとの Consumer ディスパッチ）は別物である。取り込み
// エンドポイントは、受信メッセージをデコードして outbox.Router.Deliver へ委譲する。
package internalhttp

import (
	"context"
	"log/slog"

	"github.com/example/go-ddd-template/contexts/inventory/internal/adapter/inbound/openapiinternal"
	"github.com/example/go-ddd-template/contexts/inventory/internal/application"
	"github.com/example/go-ddd-template/shared/outbox"
)

// Handler は ogen が生成した openapiinternal.Handler を実装する薄いアダプタ。
type Handler struct {
	reserver  *application.Reserver
	confirmer *application.Confirmer
	releaser  *application.Releaser
	router    *outbox.Router
	log       *slog.Logger
}

// NewHandler は内部 HTTP ハンドラを生成する。router には受信メッセージの
// 種別ディスパッチ（OnConfirmReservation / OnOrderCancelled など）を登録しておく。
func NewHandler(reserver *application.Reserver, confirmer *application.Confirmer, releaser *application.Releaser, router *outbox.Router, log *slog.Logger) *Handler {
	return &Handler{reserver: reserver, confirmer: confirmer, releaser: releaser, router: router, log: log}
}

// コンパイル時に ogen の Handler インターフェースを満たしていることを確認する。
var _ openapiinternal.Handler = (*Handler)(nil)

// ReserveStock は POST /reservations を処理する。
func (h *Handler) ReserveStock(ctx context.Context, req *openapiinternal.ReserveCommand) (*openapiinternal.Ack, error) {
	lines := make([]application.ReserveLine, 0, len(req.Lines))
	for _, l := range req.Lines {
		lines = append(lines, application.ReserveLine{SKU: l.Sku, Quantity: l.Quantity})
	}
	if err := h.reserver.Reserve(ctx, application.ReserveInput{Ref: req.Ref, Lines: lines}); err != nil {
		return nil, err
	}
	return &openapiinternal.Ack{Status: "reserved"}, nil
}

// ConfirmReservation は POST /reservations/{ref}/confirm を処理する。
func (h *Handler) ConfirmReservation(ctx context.Context, params openapiinternal.ConfirmReservationParams) (*openapiinternal.Ack, error) {
	if err := h.confirmer.Confirm(ctx, params.Ref); err != nil {
		return nil, err
	}
	return &openapiinternal.Ack{Status: "confirmed"}, nil
}

// ReleaseReservation は POST /reservations/{ref}/release を処理する。
func (h *Handler) ReleaseReservation(ctx context.Context, params openapiinternal.ReleaseReservationParams) (*openapiinternal.Ack, error) {
	if err := h.releaser.Release(ctx, params.Ref); err != nil {
		return nil, err
	}
	return &openapiinternal.Ack{Status: "released"}, nil
}

// IngestEvent は POST /events を処理する。受信メッセージを outbox.Router へ委譲する。
// 未登録の種別に対しては outbox.ErrNoRoute が返り、NewError が 422 へ翻訳する。
func (h *Handler) IngestEvent(ctx context.Context, req *openapiinternal.InboundMessage) (*openapiinternal.Ack, error) {
	msg := outbox.Message{
		ID:      req.ID,
		Type:    req.Type,
		Payload: []byte(req.Payload),
	}
	if tid, ok := req.TraceID.Get(); ok {
		msg.TraceID = tid
	}
	if at, ok := req.OccurredAt.Get(); ok {
		msg.OccurredAt = at
	}
	if err := h.router.Deliver(ctx, msg); err != nil {
		return nil, err
	}
	return &openapiinternal.Ack{Status: "delivered"}, nil
}
