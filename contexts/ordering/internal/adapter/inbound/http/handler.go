// Package httpapi は受信アダプタ（inbound adapter＝駆動側）の HTTP 実装。
// ヘキサゴナルアーキテクチャの「入口」であり、外の世界（HTTP リクエスト）を
// アプリケーション層のユースケース呼び出しへ翻訳して届ける。ogen が生成した
// HTTP サーバのハンドラを実装し、HTTP とアプリケーション層の相互変換だけを担う。
// 業務ロジックはここには置かない。
//
// パッケージ名を httpapi にしているのは、取り込み側で標準ライブラリ net/http と
// 識別子（http）が衝突しないようにするため。ディレクトリ名は入口の輸送手段を表す http。
package httpapi

import (
	"context"
	"log/slog"

	"github.com/example/go-ddd-template/contexts/ordering/internal/adapter/inbound/openapi"
	"github.com/example/go-ddd-template/contexts/ordering/internal/application"
)

// Handler は ogen が生成した openapi.Handler を実装する薄いアダプタ。
type Handler struct {
	place  *application.PlaceOrder
	get    *application.GetOrder
	cancel *application.CancelOrder
	log    *slog.Logger
}

// NewHandler は HTTP ハンドラを生成する。
func NewHandler(place *application.PlaceOrder, get *application.GetOrder, cancel *application.CancelOrder, log *slog.Logger) *Handler {
	return &Handler{place: place, get: get, cancel: cancel, log: log}
}

// コンパイル時に ogen の Handler インターフェースを満たしていることを確認する。
var _ openapi.Handler = (*Handler)(nil)

// PlaceOrder は POST /orders を処理する。作成後、作成された注文の現在状態を射影して返す。
//
// 戻り値は生成された union（openapi.PlaceOrderRes）。契約が明示ステータス（400/409/422/503）と
// default を宣言するため、ogen は成功応答（*OrderView）とエラー応答をまとめた union をハンドラの
// 戻り型にする。*OrderView は union を満たすので成功は toOrderView をそのまま返し、エラーは
// nil を返して NewError（動的ステータスの ProblemResponseStatusCode）へ委譲する。
func (h *Handler) PlaceOrder(ctx context.Context, req *openapi.PlaceOrderRequest) (openapi.PlaceOrderRes, error) {
	id, err := h.place.Handle(ctx, toPlaceOrderInput(req))
	if err != nil {
		// ドメイン／アプリケーションのエラーはそのまま返す。HTTP への翻訳は NewError が行う。
		return nil, err
	}
	view, err := h.get.Handle(ctx, id.String())
	if err != nil {
		return nil, err
	}
	return toOrderView(view), nil
}

// GetOrder は GET /orders/{id} を処理する。戻り値は union（openapi.GetOrderRes。理由は PlaceOrder 参照）。
func (h *Handler) GetOrder(ctx context.Context, params openapi.GetOrderParams) (openapi.GetOrderRes, error) {
	view, err := h.get.Handle(ctx, params.ID)
	if err != nil {
		return nil, err
	}
	return toOrderView(view), nil
}

// CancelOrder は POST /orders/{id}/cancel を処理する。取消後、注文の現在状態を射影して返す。
// 戻り値は union（openapi.CancelOrderRes。理由は PlaceOrder 参照）。
func (h *Handler) CancelOrder(ctx context.Context, params openapi.CancelOrderParams) (openapi.CancelOrderRes, error) {
	if err := h.cancel.Handle(ctx, params.ID); err != nil {
		return nil, err
	}
	view, err := h.get.Handle(ctx, params.ID)
	if err != nil {
		return nil, err
	}
	return toOrderView(view), nil
}

// toPlaceOrderInput は ogen のリクエスト型をアプリケーション層の入力へ変換する。
func toPlaceOrderInput(req *openapi.PlaceOrderRequest) application.PlaceOrderInput {
	lines := make([]application.PlaceOrderLine, 0, len(req.Lines))
	for _, l := range req.Lines {
		lines = append(lines, application.PlaceOrderLine{
			SKU:             l.Sku,
			Quantity:        l.Quantity,
			UnitPriceAmount: l.UnitPrice.Amount,
			Currency:        l.UnitPrice.Currency,
		})
	}
	return application.PlaceOrderInput{CustomerID: req.CustomerId, Lines: lines}
}

// toOrderView はアプリケーション層の DTO を ogen の応答型へ変換する。
func toOrderView(v application.OrderView) *openapi.OrderView {
	lines := make([]openapi.OrderLineView, 0, len(v.Lines))
	for _, l := range v.Lines {
		lines = append(lines, openapi.OrderLineView{
			Sku:       l.SKU,
			Quantity:  l.Quantity,
			UnitPrice: openapi.Money{Amount: l.UnitPriceAmount, Currency: l.UnitPriceCurrency},
			Subtotal:  openapi.Money{Amount: l.SubtotalAmount, Currency: l.SubtotalCurrency},
		})
	}
	return &openapi.OrderView{
		ID:             v.ID,
		CustomerId:     v.CustomerID,
		Status:         v.Status,
		Lines:          lines,
		Total:          openapi.Money{Amount: v.TotalAmount, Currency: v.TotalCurrency},
		ReservationRef: v.ReservationRef,
		Version:        v.Version,
	}
}
