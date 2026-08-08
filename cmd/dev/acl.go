package main

import (
	"context"
	"fmt"

	"github.com/example/go-ddd-template/contexts/inventory"
	invport "github.com/example/go-ddd-template/contexts/inventory/port"
	ordport "github.com/example/go-ddd-template/contexts/ordering/port"
)

// inProcessReserver は注文コンテキストの腐敗防止層（ACL）ポート StockReserver を、
// 在庫モジュールの公開シーム（inventory.Module.Reserve/Release）を直接呼んで満たす
// 同一プロセス向けのアダプタ。分散構成の aclhttp（生成クライアント越しの HTTP 呼び出し）と
// 同じポート契約を満たし、差し替わるのはこのアダプタだけである。
//
// 境界規則: 引数・戻り値は翻訳済み DTO（ordering/port.ReserveLine → inventory/port.SKUQty）
// だけを跨がせ、在庫のドメイン型は一切現れない。在庫側の失敗は注文コンテキスト自身の番兵
// （ordering/port.ErrReservationRejected）へ翻訳し、在庫側の番兵名は漏らさない。
//
// StockReserver ポート自体は注文の internal/application にあり、cmd/dev はそれを名指し
// できない。しかし Go のインターフェースは構造的に満たされるため、必要なメソッド
// （Reserve / Release）を持つこのアダプタは、型名を書かずに ordering.NewInMemory の
// Reserver フィールドへそのまま注入できる。
type inProcessReserver struct {
	inv *inventory.Module
}

// Reserve は予約参照 ref に対して lines をまとめて在庫予約する。翻訳済み DTO を在庫側の
// DTO へ写像し、在庫の失敗を注文側の番兵へ翻訳する。
//
// in-process 呼び出しゆえネットワーク不達は起こらないため、失敗はすべて業務的な拒否
// （ErrReservationRejected → HTTP 409）として扱う（分散構成の 503 相当は発生しない）。
// 在庫側のエラーは %v（文字列のみ）で説明に含め、%w では注文側の番兵だけを保持することで、
// 在庫側のセンチネル同一性を注文側のエラー鎖へ漏らさない。
func (a *inProcessReserver) Reserve(ctx context.Context, ref string, lines []ordport.ReserveLine) error {
	items := make([]invport.SKUQty, 0, len(lines))
	for _, l := range lines {
		items = append(items, invport.SKUQty{SKU: l.SKU, Qty: l.Qty})
	}
	if err := a.inv.Reserve(ctx, ref, items); err != nil {
		return fmt.Errorf("在庫の予約が拒否されました（%v）: %w", err, ordport.ErrReservationRejected)
	}
	return nil
}

// Release は予約参照 ref の予約を解放する（保存失敗時の best-effort な補償解放）。冪等であり、
// 未知の参照でも安全に呼べる。呼び出し側（PlaceOrder）は失敗をログに留めるだけなので、
// ここでは在庫側のエラーをそのまま返す（分類には用いられない）。
func (a *inProcessReserver) Release(ctx context.Context, ref string) error {
	return a.inv.Release(ctx, ref)
}
