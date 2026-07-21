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

	"github.com/example/go-ddd-template/contexts/inventory/internal/adapter/inbound/openapi"
	"github.com/example/go-ddd-template/contexts/inventory/internal/application"
)

// Handler は ogen が生成した openapi.Handler を実装する薄いアダプタ。
type Handler struct {
	replenisher *application.Replenisher
	viewer      *application.StockViewer
	log         *slog.Logger
}

// NewHandler は HTTP ハンドラを生成する。
func NewHandler(replenisher *application.Replenisher, viewer *application.StockViewer, log *slog.Logger) *Handler {
	return &Handler{replenisher: replenisher, viewer: viewer, log: log}
}

// コンパイル時に ogen の Handler インターフェースを満たしていることを確認する。
var _ openapi.Handler = (*Handler)(nil)

// ReplenishStock は POST /stock/{sku}/replenish を処理する。
func (h *Handler) ReplenishStock(ctx context.Context, req *openapi.ReplenishRequest, params openapi.ReplenishStockParams) (*openapi.StockView, error) {
	res, err := h.replenisher.Replenish(ctx, application.ReplenishInput{
		SKU:      params.Sku,
		Quantity: req.Quantity,
	})
	if err != nil {
		// ドメイン／永続化のエラーはそのまま返す。HTTP への翻訳は NewError が行う。
		return nil, err
	}
	return toStockView(res), nil
}

// GetStock は GET /stock/{sku} を処理する。
func (h *Handler) GetStock(ctx context.Context, params openapi.GetStockParams) (*openapi.StockView, error) {
	res, err := h.viewer.QueryStock(ctx, application.QueryStockInput{SKU: params.Sku})
	if err != nil {
		return nil, err
	}
	return toStockView(res), nil
}

// toStockView はアプリケーション層の DTO を ogen の応答型へ変換する。
func toStockView(r application.StockResult) *openapi.StockView {
	return &openapi.StockView{
		Sku:       r.SKU,
		Available: r.Available,
		Reserved:  r.Reserved,
		Version:   r.Version,
	}
}
