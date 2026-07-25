package application

import (
	"errors"
	"fmt"
	"unicode"

	"github.com/example/go-ddd-template/contexts/ordering/internal/domain/order"
)

// このファイルは「フィールド識別情報を 3 層で段階的に組み立てる」の第 2 段を担う。
//
//	[domain]                    [application]              [interfaces]
//	「数量は 1 以上」            「入力 DTO のどの位置か」   「JSON のどの名前か」
//	FieldViolation{             ValidationError{           InvalidParam{
//	  Rule:  VQuantity     →      Path: "Lines[0].Quantity"  Name: "lines[0].quantity"
//	  Index: nil }                Code: "invalid_quantity" } Code: "invalid_quantity"
//	                                                         Reason: "1 以上の値を..." }
//
// 各層は自分が知っていることだけを足す。ドメインは JSON の名前を知らず、アプリケーション層は
// 転送形式を知らない（FR-4.4 / FR-4.5 / FR-4.6）。

// FieldViolation は入力 DTO 上の位置まで解決した違反。
type FieldViolation struct {
	// Path は入力 DTO 上のパス（"Lines[0].UnitPrice.Amount"）。Go / DTO の識別子で
	// 表記し、JSON 名（lines[0].unitPrice.amount）への翻訳はインターフェース層が担う
	// （規則 R-10 / FR-4.6）。
	Path string
	// Code はドメインの Rule から引き継ぐ機械可読な違反理由。
	Code string
}

// ValidationError は 1 件以上の入力検証違反を運ぶ。
//
// Err に元のエラーを包んで Unwrap で公開するため、errors.Is による番兵判定は
// 従来どおり機能する（規則 R-15）。インターフェース層の classify（ステータス判定）は
// この型の存在を知らないまま無変更で動く。
type ValidationError struct {
	Violations []FieldViolation
	Err        error
}

// Error は包んでいるエラーの文言をそのまま返す（ログ出力とエラー文言を変えないため）。
func (e *ValidationError) Error() string { return e.Err.Error() }

// Unwrap は包んでいるエラーを返す。これが番兵までの errors.Is 連鎖を保つ。
func (e *ValidationError) Unwrap() error { return e.Err }

// linesField は明細コレクションを指す入力 DTO 上の名前（PlaceOrderInput.Lines）。
const linesField = "Lines"

// dtoPaths は「ドメインの語彙でのフィールド名 → 入力 DTO 上のパス断片」の **上書き表**。
//
// 既定は機械的な変換（先頭 1 文字を大文字にする）で足りる: quantity -> Quantity、
// sku -> Sku、reservationRef -> ReservationRef。ここに書くのは、その変換では正しく
// ならないものだけである。
//
// 現状の唯一の例外は金額である。入力 DTO（PlaceOrderLine）は amount / currency を
// UnitPriceAmount / Currency として平らに持つのに対し、API 上は unitPrice の入れ子だから、
// その差をここで吸収する。
//
// **したがって、ドメインに検証規則を 1 つ足しても通常はこの表を触らなくてよい**。
var dtoPaths = map[string]string{
	order.VMoneyAmount.Field:   "UnitPrice.Amount",
	order.VMoneyCurrency.Field: "UnitPrice.Currency",
}

// locate はドメインの FieldViolation に、入力 DTO 上の位置 at を前置して
// ValidationError へ包み直す。at が空なら入力のトップ階層を指す。
//
// 違反が明細位置（Index）を運んでいる場合は、at ではなく Lines[i] を前置する。これが
// ドメインが自分でコレクションを走査した場合（Rule.ViolatedAt）の位置情報をパスへ載せる
// 経路である。
//
// ドメインの違反でなければ元のエラーをそのまま返す（透過）。この透過がとても重要である。
// リポジトリの失敗や楽観的排他制御の衝突（uow.ErrConcurrencyConflict）が誤って
// 「入力検証エラー」に化けると、リクエスターに「あなたの入力が悪い」と嘘をつくことになる。
func locate(at string, err error) error {
	// 既に位置が解決済みなら二重に包まない（前置が上書きされて誤ったパスになるため）。
	var already *ValidationError
	if errors.As(err, &already) {
		return err
	}

	var v *order.FieldViolation
	if !errors.As(err, &v) {
		return err
	}

	prefix := at
	if v.Index != nil {
		prefix = linePath(*v.Index)
	}

	return &ValidationError{
		Violations: []FieldViolation{{
			Path: joinDTOPath(prefix, dtoSegment(v.Rule.Field)),
			Code: v.Rule.Code,
		}},
		Err: err,
	}
}

// dtoSegment はドメインのフィールド名を入力 DTO 上のパス断片へ写す。
// 上書き表に無ければ先頭 1 文字を大文字にするだけでよい。
func dtoSegment(field string) string {
	if seg, ok := dtoPaths[field]; ok {
		return seg
	}
	return upperFirst(field)
}

// linePath は明細の位置を表すパス断片を作る（"Lines[0]" など）。
// 添字の記法（規則 R-8）を 1 箇所に閉じ込める。
func linePath(i int) string {
	return fmt.Sprintf("%s[%d]", linesField, i)
}

// joinDTOPath はパス断片をドットで連結する。at が空なら断片だけを返す。
func joinDTOPath(at, seg string) string {
	if at == "" {
		return seg
	}
	return at + "." + seg
}

// upperFirst は先頭 1 文字を大文字にする（quantity -> Quantity）。
func upperFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}
