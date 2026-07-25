// Package ogenproblem は ogen が生成したエラーから、RFC 9457 の invalid-params に
// 載せるフィールド情報を取り出す。
//
// なぜ shared に置くのか: この抽出は本ユニットで最も壊れやすい部分であり、3 つの
// ハンドラパッケージ（ordering 公開 / inventory 公開 / inventory 内部）に重複させると、
// 修正漏れがそのままサーバ間の振る舞いのずれになる（NFR-6.1 の懸念）。1 箇所に集約する。
//
// なぜ problem 本体と分けるのか: このパッケージだけが ogen に依存するからである。純粋な
// [github.com/example/go-ddd-template/shared/problem] は標準ライブラリだけに依存し続ける。
// shared/uow（純粋）と shared/uow/pgxuow（ドライバ固有）の分け方と同じ形である。
//
// ここに置かないもの: ProblemDetails の組み立て。ogen 生成の ProblemDetails は契約ごとに
// 別の Go 型であり、shared モジュールはそれらを跨げない。組み立ては各コンテキストの
// インターフェース層に残す（FR-6.2）。
package ogenproblem

import (
	"errors"
	"regexp"
	"strings"

	"github.com/ogen-go/ogen/ogenerrors"
	"github.com/ogen-go/ogen/validate"

	"github.com/example/go-ddd-template/shared/problem"
)

// BodyParamName はリクエストボディ全体を指す擬似的なパス。個々のフィールドではなく
// 「ボディそのもの」が問題である場合（ボディが空）にだけ使う。
const BodyParamName = "body"

// decodeFieldRe は ogen の Decode() 経路が積むラップ列から、通過したフィールド名を拾う。
//
// ogen は入れ子を降りるたびに `decode field "<name>"` でエラーを包む。例:
//
//	decode Probe: callback: decode field "lines": callback:
//	decode Line: callback: decode field "quantity": unexpected byte 34 '"' at 48
//
// ここから ["lines", "quantity"] が出現順に取れる。<name> は契約に書かれたプロパティ名で
// あり生成コード中のリテラルなので、受信値が混ざることはない（規則 R-11）。
//
// 文字列解析に頼るのは、Decode() 経路が構造化されたフィールド情報を残さないためである
// （型不一致・不正 JSON は *validate.Error にならない）。形式が変わればここは空を返し、
// 「情報が減るだけで誤ったフィールド名は出さない」側へ倒れる。その静かな劣化を検知するのが
// [extract_test.go] の特性テストである。
var decodeFieldRe = regexp.MustCompile(`decode field "([^"]+)"`)

// StatusOf は ogen の判定に従って HTTP ステータスを返す（FR-1.5）。
// デコード／パラメータ検証は 400、Content-Type 不一致は 415、未実装は 501、その他は 500。
// 独自の再割り当てはしない。
func StatusOf(err error) int {
	return ogenerrors.ErrorCode(err)
}

// ExtractParams は ogen のエラーから違反フィールドの一覧を取り出す。
//
// 特定できなければ nil を返す（FR-3.4 / 規則 R-14）。空スライスではなく nil を返すのは、
// 呼び出し側が「違反フィールドが 0 件」と「特定できなかった」を区別できるようにするためで、
// 後者では invalid-params をキーごと省略する。
//
// 抽出できる範囲には限界がある（詳細は CONVENTIONS.md）。
//   - 配列の添字は Decode() 経路では取れない。ogen がラップ列に位置を残さないためで、
//     実装の手抜きではない（規則 R-9）。Validate() 経路では "[0]" が取れる。
//   - 同時違反をすべて列挙できるのは「同一オブジェクト内の兄弟の必須欠落」と
//     「Validate() 経路の複数違反」に限る。jx のストリーミングデコーダが最初の
//     コールバックエラーで走査を打ち切るためである（FR-3.3 の限界）。
func ExtractParams(err error) []problem.Param {
	if err == nil {
		return nil
	}

	full := err.Error()

	// 構造化された検証エラー（*validate.Error）を先に取り出す。手順 1 の走査範囲を
	// 決めるのに使う。
	var ve *validate.Error
	hasVE := errors.As(err, &ve)

	// 手順 1: Decode() 経路のラップ列から、通過したフィールド名を出現順に集める。
	//
	// 走査対象は *validate.Error の描画部分より「前」に限定する。これが安全性の要である。
	// validate.Error.Error() は葉のエラー文言を再帰的に連結し、その文言は受信値を含みうる
	// （ogen v1.23.0 の uniqueItems / enum 違反は受信値を文言に埋める）。全体を走査すると、
	// 受信値に紛れ込んだ `decode field "X"` を本物のラップ列と誤認し、攻撃者が選んだ文字列を
	// パスの先頭に混入させてしまう（規則 R-11 / NFR-1 違反）。ラップ列は構造上かならず
	// validate 部分より前に現れるので、境界より前だけを見れば正しいパスを取りこぼさず、
	// かつ受信値には決して触れない。
	scan := full
	if hasVE {
		if i := strings.LastIndex(full, ve.Error()); i >= 0 {
			scan = full[:i]
		}
	}
	path := make([]string, 0, 4)
	for _, m := range decodeFieldRe.FindAllStringSubmatch(scan, -1) {
		path = append(path, m[1])
	}

	// 手順 2: 構造化された検証エラーがあれば、入れ子を再帰的に降りる。
	// Decode() 経路の必須欠落（フラット 1 段）と Validate() 経路の制約違反（多段）の
	// 双方がここを通る。ラップ列（手順 1）と同時に値を返すのは入れ子の必須欠落だけで、
	// その場合の連結は正しい（例: nested.inner）。
	if hasVE {
		if params := walk(ve, path); len(params) > 0 {
			return params
		}
	}

	// 手順 3: パラメータ（query / path / header）の解釈失敗は名前を直接持っている。
	var pe *ogenerrors.DecodeParamError
	if errors.As(err, &pe) {
		return []problem.Param{{Name: pe.Name, Code: problem.CodeInvalidParam}}
	}

	// 手順 4: ボディそのものが無い。個々のフィールドではないので擬似パスを使う。
	if errors.Is(err, validate.ErrBodyRequired) {
		return []problem.Param{{Name: BodyParamName, Code: problem.CodeBodyRequired}}
	}

	// 手順 5: 構造化情報は無いが、ラップ列からパスだけは分かる（型不一致がこれ）。
	if len(path) > 0 {
		return []problem.Param{{Name: problem.JoinPath(path), Code: problem.CodeType}}
	}

	// 手順 6: 何も特定できない（不正 JSON、Content-Type 不一致など）。
	return nil
}

// walk は *validate.Error を再帰的に降り、葉のフィールドごとに Param を作る。
//
// 再帰が必要な理由: validate.FieldError.Error は *validate.Error でありうる。ogen の
// 配列バリデーションは要素を Name="[0]" という FieldError で包み、その中身がさらに
// 入れ子の *validate.Error になる。単層走査だと "lines" で止まり、葉の "sku" と
// 添字 "[0]" を取りこぼす。
//
//	lines -> [0] -> price -> currency   ==>   lines[0].price.currency
func walk(ve *validate.Error, path []string) []problem.Param {
	var out []problem.Param
	for _, f := range ve.Fields {
		// append の再利用でスライスを共有しないよう、枝ごとに新しい配列を作る。
		next := make([]string, 0, len(path)+1)
		next = append(next, path...)
		next = append(next, f.Name)

		var nested *validate.Error
		if errors.As(f.Error, &nested) {
			out = append(out, walk(nested, next)...)
			continue
		}
		out = append(out, problem.Param{
			Name: problem.JoinPath(next),
			Code: codeOf(f.Error),
		})
	}
	return out
}

// codeOf は葉の検証エラーを契約検証語彙の code へ写す（business-rules §2.1）。
//
// 分からないものは problem.CodeInvalid へ落とす。ogen の文言をそのまま転記しない
// （FR-2.3）ので、code が粗くなるぶん情報は減るが、内部文言や受信値は決して漏れない。
// 実際 enum 違反（"invalid value: gamma"）と uniqueItems 違反
// （"duplicate element [0] a"）は ogen v1.23.0 では専用の型にならず受信値を文言に
// 含むため、ここで CodeInvalid へ落として文言ごと捨てるのが正しい振る舞いになる。
func codeOf(err error) string {
	if errors.Is(err, validate.ErrFieldRequired) {
		return problem.CodeRequired
	}

	var minLen *validate.MinLengthError
	if errors.As(err, &minLen) {
		return problem.CodeMinLength
	}
	var maxLen *validate.MaxLengthError
	if errors.As(err, &maxLen) {
		return problem.CodeMaxLength
	}
	var noMatch *validate.NoRegexMatchError
	if errors.As(err, &noMatch) {
		return problem.CodePattern
	}
	var dup *validate.DuplicateItemsError
	if errors.As(err, &dup) {
		return problem.CodeUniqueItems
	}
	return problem.CodeInvalid
}
