package inventory

import (
	"fmt"
	"time"
)

// ReservationLine はマルチ SKU 予約の 1 行（SKU と要求数量の対）を表す。
type ReservationLine struct {
	SKU      SKU
	Quantity Quantity
}

// ReservationService は複数の在庫項目（StockItem）に跨る予約を扱うドメインサービス。
//
// ドメインサービスは「集約をひとつに閉じ込められない振る舞い」を担う。ここではマルチ SKU
// 予約の「全か無か（all-or-nothing）」割り当てがそれにあたる。ドメインサービスは自らは
// リポジトリを引かず、ユースケースがロード済みの StockItem 群を受け取って操作する
// （純粋なドメインを保つための規約）。
type ReservationService struct{}

// Allocate は参照 ref に対して lines の各 SKU をまとめて予約する。1 つでも在庫不足なら
// 全体を失敗させ（部分予約は作らない）、ErrInsufficientStock を返す。
//
// これは「1 トランザクション 1 集約」という既定の例外であり、マルチ SKU 予約は複数の
// StockItem 集約を 1 つの作業単位で跨ぐ唯一の意図的なケースである（このライフサイクル
// 全体 — reserve だけでなく confirm / release も — に及ぶ）。
//
// 冪等性: 既に ref の有効な予約を持つ StockItem はそのまま no-op になる（各 StockItem の
// Reserve が冪等なため）。事前検証でもそれらの項目は在庫チェックの対象外にする。
func (ReservationService) Allocate(items []*StockItem, ref ReservationRef, lines []ReservationLine, ttl time.Duration) error {
	if ref.IsZero() {
		return VReservationRef.Violated("予約参照は空にできません")
	}

	// SKU で在庫項目を索引する。
	bySKU := make(map[string]*StockItem, len(items))
	for _, it := range items {
		bySKU[it.SKU().String()] = it
	}

	// 第 1 相: 全行を事前検証する（ここでは一切変更しない）。1 行でも不成立なら全体を失敗させる。
	//
	// 明細の走査はここ（集約側）で行うため、何番目の明細で失敗したかを知っているのは
	// このループだけである。アプリケーション層の toReservationLines は別の走査であり、
	// ここに到達した時点でその位置情報は失われている。よって違反に Index を載せて返す
	// （アプリケーション層がそれを入力 DTO 上のパス Lines[i].Quantity へ組み立てる）。
	for i, l := range lines {
		it, ok := bySKU[l.SKU.String()]
		if !ok {
			return fmt.Errorf("SKU %q: %w", l.SKU.String(), ErrStockItemNotFound)
		}
		if l.Quantity.IsZero() {
			// NewQuantity は 0 を通すため、「1 以上」の判定はドメインサービスが担う（FR-4.7）。
			// 走査しているのはここなので、何番目の明細かを名乗れるのもここだけである。
			return VQuantity.ViolatedAt(i, "SKU %q の予約数量は 1 以上でなければなりません", l.SKU.String())
		}
		// 既に同一 ref の予約を持つ項目は冪等 no-op になるため在庫チェックから除外する。
		if it.hasReservation(ref) {
			continue
		}
		if l.Quantity.GreaterThan(it.Available()) {
			return fmt.Errorf("SKU %q: 要求 %d > 利用可能 %d: %w", l.SKU.String(), l.Quantity.Int(), it.Available().Int(), ErrInsufficientStock)
		}
	}

	// 第 2 相: 事前検証を通過したので、全行を実際に予約する。第 1 相が参照・数量・在庫の
	// すべてを検査済みなので、ここでの失敗は防御的な経路であり実際には到達しない。
	// そのため位置を付け直す仕組みは置かず、そのまま伝播させる。
	for _, l := range lines {
		it := bySKU[l.SKU.String()]
		if err := it.Reserve(ref, l.Quantity, ttl); err != nil {
			return err
		}
	}
	return nil
}
