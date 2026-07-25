package inventory

import (
	"errors"
	"fmt"
)

// ドメイン層のセンチネルエラー。呼び出し側は errors.Is で判定する。
// いずれも「予期される失敗」であり、panic ではなく値として返す。
// 上位層（アプリケーション／インターフェース）でラップする際は %w を用いて
// これらを保持し、errors.Is による判定を壊さないこと。
var (
	// ErrStockItemNotFound は、指定した SKU の在庫項目が存在しないことを表す。
	ErrStockItemNotFound = errors.New("在庫項目が見つかりません")

	// ErrInvalidSKU は、SKU が不正（空文字など）であることを表す。
	ErrInvalidSKU = errors.New("SKU が不正です")

	// ErrInvalidQuantity は、数量が不正（負数、または要求数が 0）であることを表す。
	ErrInvalidQuantity = errors.New("数量が不正です")

	// ErrInvalidReservationRef は、予約参照（相関 ID）が不正（空文字など）であることを表す。
	ErrInvalidReservationRef = errors.New("予約参照が不正です")

	// ErrInsufficientStock は、要求数量が利用可能在庫を上回り予約できないことを表す。
	// 予約が許容されるのは、要求 Quantity が available 以下のときのみ（在庫不変条件）。
	ErrInsufficientStock = errors.New("在庫が不足しています")

	// ErrReservationNotFound は、Confirm 対象の有効な予約が存在しないことを表す。
	// 既に Reap 済み、または速い取消で解放済みの参照に対する Confirm で返る。
	ErrReservationNotFound = errors.New("予約が見つかりません")
)

// Rule は検証規則。「どのフィールドが」「どの code で」「どの番兵に」対応するかを
// 1 箇所に束ねる。以前はこの 3 つを別々の定数リストに書き分けていたが、ほぼ 1 対 1 に
// 対応する 3 つの語彙を並行して保守するのは誤りの温床であり、規則を 1 つ足すたびに
// 4 箇所を編集する必要があった。
//
// 新しい検証規則を足す手順は 2 箇所の編集で済む。
//  1. 下の var ブロックに Rule を 1 行足す。
//  2. インターフェース層の reason 表（internal/adapter/inbound/{http,internalhttp}/problem.go）
//     に 1 行足す（規則 R-19）。
//
// 番兵の定義（上の var ブロック）は変えない。番兵は errors.Is の判定単位であり、
// 既存の公開 API だからである。Rule はそれを指すだけで、置き換えない。
type Rule struct {
	// Field はドメインの語彙でのフィールド名（"sku" / "quantity" / "reservationRef"）。
	//
	// 重要: これは HTTP のフィールドパス（lines[0].quantity）ではない。ドメインは転送形式も
	// 配列の添字も知らない。位置の付与はアプリケーション層、JSON 名への翻訳はインターフェース層
	// の責務である（FR-4.4 / FR-4.5 / FR-4.6）。
	Field string
	// Code は 422 応答の invalid-params[].code に載る安定識別子。
	Code string
	// Err は対応する番兵エラー。errors.Is の判定単位。
	Err error
}

// Violated は規則違反のエラーを返す。format には状況の説明を書く（番兵の文言は
// 自動で後ろに連結されるので繰り返さなくてよい）。
//
//	VReservationRef.Violated("予約参照は空にできません")
//	VQuantity.Violated("数量は 0 以上でなければなりません（指定値: %d）", n)
//
// 返り値は番兵まで unwrap されるので errors.Is は従来どおり機能する（規則 R-15）。
func (r Rule) Violated(format string, args ...any) error {
	return &FieldViolation{Rule: r, Err: r.wrap(format, args...)}
}

// ViolatedAt は「受け取ったコレクションの i 番目（0 始まり）」という位置つきの違反を返す。
//
// これが必要な理由: ReservationService.Allocate は明細（lines）の走査を集約側で行う。
// アプリケーション層の toReservationLines は別の走査であり、Allocate が何番目の明細で
// 失敗したかはアプリ層の走査が終わった時点では失われている。だからドメイン側が位置を
// 名乗る。「渡された何番目か」はドメイン自身の知識であり、HTTP のパス（lines[1].quantity）
// ではないので FR-4.4 に抵触しない。この位置を入力 DTO 上のパスへ組み立てるのは
// アプリケーション層である（FR-4.5）。
func (r Rule) ViolatedAt(i int, format string, args ...any) error {
	return &FieldViolation{Rule: r, Index: &i, Err: r.wrap(format, args...)}
}

// wrap は説明文の後ろに番兵を %w で連結する。
func (r Rule) wrap(format string, args ...any) error {
	all := make([]any, 0, len(args)+1)
	all = append(all, args...)
	all = append(all, r.Err)
	return fmt.Errorf(format+": %w", all...)
}

// 検証規則の一覧。この 1 つのリストが、フィールド名・code 語彙・番兵の対応関係に関する
// 唯一の情報源である。
//
// この語彙は「在庫」コンテキストが所有する。注文コンテキストに同名の code
// （invalid_sku / invalid_quantity）があるが、別コンテキストの別語彙であり共有型に
// 切り出さない（制約 C-6 / 規則 R-7）。とくに invalid_quantity は意味が異なる:
// 在庫の Quantity は 0 を許容する（>= 0）が、注文行は 1 以上（>= 1）である。
// この値域差は reason の文言差として現れる。
var (
	VSKU            = Rule{Field: "sku", Code: "invalid_sku", Err: ErrInvalidSKU}
	VQuantity       = Rule{Field: "quantity", Code: "invalid_quantity", Err: ErrInvalidQuantity}
	VReservationRef = Rule{Field: "reservationRef", Code: "invalid_reservation_ref", Err: ErrInvalidReservationRef}
)

// FieldViolation は規則違反を表す構造化エラー。Rule.Violated / Rule.ViolatedAt が返す。
//
// 番兵エラーを Err に包み Unwrap で公開するため、errors.Is による既存の判定は
// 一切変わらない（規則 R-15）。上位層は errors.As で取り出して Rule.Field / Rule.Code を読む。
//
// ポインタレシーバで error を実装しているため、errors.As の対象は
// `var v *FieldViolation; errors.As(err, &v)` である。
//
// この型は標準ライブラリだけに依存する（純粋ドメイン、制約 C-1）。注文コンテキストにも
// 同名・同形の型があるが、別々に定義し共有型に切り出さない（制約 C-6 / 規則 R-7）。
type FieldViolation struct {
	// Rule は違反した検証規則。
	Rule Rule
	// Index は「集約が受け取ったコレクションの何番目か」（0 始まり）。
	// 位置を持たない違反（ViolatedAt を使わなかった場合）では nil。
	Index *int
	// Err は包んでいるエラー（説明文 + 番兵）。
	Err error
}

// Error は包んでいるエラーの文言をそのまま返す。
func (e *FieldViolation) Error() string { return e.Err.Error() }

// Unwrap は包んでいるエラーを返す。これが errors.Is(err, ErrInvalidQuantity) を
// 従来どおり真に保つ仕組みである（規則 R-15）。
func (e *FieldViolation) Unwrap() error { return e.Err }
