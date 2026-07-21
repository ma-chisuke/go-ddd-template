// Package aclhttp は在庫コンテキストへの腐敗防止層（ACL）の HTTP 実装を提供する
// 送信アダプタ（outbound adapter＝被駆動側）。application.StockReserver を、生成クライアント
// clients/inventory（在庫の内部 API から生成）で実装する。
//
// 重要（境界規則）: 本パッケージは生成クライアント clients/inventory のみを import し、
// contexts/inventory の Go パッケージは一切 import しない（在庫へは HTTP 越しにのみ到達する）。
// 依存方向は一方向であり、静的解析（depguard）で機械的に強制する。翻訳の要点は、
// port.ReserveLine をクライアントの request 型へ写像し、クライアントが返す HTTP ステータス/
// エラーを注文コンテキスト自身の番兵（ErrReservationRejected / ErrReservationUnavailable）へ
// 翻訳することである。在庫側の番兵名は注文側へ漏らさない。
package aclhttp

import (
	"context"
	"errors"
	"fmt"

	invclient "github.com/example/go-ddd-template/clients/inventory/invclient"
	"github.com/example/go-ddd-template/contexts/ordering/internal/application"
	"github.com/example/go-ddd-template/contexts/ordering/port"
)

// Reserver は application.StockReserver を生成クライアントで実装する ACL アダプタ。
// クライアントはテスト容易性のためインターフェース invclient.Invoker として受け取る
// （httptest 経由の実クライアントでも、フェイクでも差し替えられる）。
type Reserver struct {
	client invclient.Invoker
}

// NewReserver は ACL アダプタを生成する。
func NewReserver(client invclient.Invoker) *Reserver {
	return &Reserver{client: client}
}

// コンパイル時にポートを満たしていることを確認する。
var _ application.StockReserver = (*Reserver)(nil)

// Reserve は在庫内部 API の予約エンドポイントを呼ぶ。port.ReserveLine を生成 request 型へ
// 翻訳し、失敗を注文コンテキスト自身の番兵へ翻訳する。
func (r *Reserver) Reserve(ctx context.Context, ref string, lines []port.ReserveLine) error {
	req := &invclient.ReserveCommand{Ref: ref, Lines: toClientLines(lines)}
	if _, err := r.client.ReserveStock(ctx, req); err != nil {
		return translate(err, "在庫の予約")
	}
	return nil
}

// Release は在庫内部 API の解放エンドポイントを呼ぶ（best-effort な補償解放）。
func (r *Reserver) Release(ctx context.Context, ref string) error {
	if _, err := r.client.ReleaseReservation(ctx, invclient.ReleaseReservationParams{Ref: ref}); err != nil {
		return translate(err, "在庫予約の解放")
	}
	return nil
}

// toClientLines は翻訳済み DTO port.ReserveLine を生成クライアントの request 型へ写像する。
func toClientLines(lines []port.ReserveLine) []invclient.ReserveLine {
	out := make([]invclient.ReserveLine, 0, len(lines))
	for _, l := range lines {
		out = append(out, invclient.ReserveLine{Sku: l.SKU, Quantity: l.Qty})
	}
	return out
}

// translate はクライアントが返すエラー（HTTP ステータス/トランスポート失敗）を、注文
// コンテキスト自身の番兵へ翻訳する。
//
//   - problem+json の 4xx（在庫不足の 409 など）      -> ErrReservationRejected（業務的拒否 -> 409）
//   - problem+json の 5xx / 接続不可 / タイムアウト等  -> ErrReservationRejected かつ
//     ErrReservationUnavailable（不達 -> 503）
//
// いずれも原因（err）を %w で保持しつつ、番兵へ一致させる（errors.Join）。在庫側の
// センチネル名（ErrInsufficientStock など）は翻訳しても保持せず、注文側へ漏らさない。
func translate(err error, op string) error {
	var problem *invclient.ProblemResponseStatusCode
	if errors.As(err, &problem) && problem.StatusCode < 500 {
		return fmt.Errorf("%sが拒否されました: %w", op, errors.Join(application.ErrReservationRejected, err))
	}
	return fmt.Errorf("%sで在庫サービスが利用できません: %w", op,
		errors.Join(application.ErrReservationRejected, application.ErrReservationUnavailable, err))
}
