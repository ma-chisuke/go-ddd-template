package problem

// 契約検証語彙（400 / type: validation-error）と、その reason 表（規則 R-4 §2.1）。
//
// これは JSON Schema / OpenAPI 由来の語彙であり、どのコンテキストでも意味が同じなので
// shared に置ける。ドメイン検証語彙（422 / type: invalid-input）は意味がコンテキストに
// 依存するのでここには置かない（制約 C-6 / 規則 R-7）。
//
// クライアントはこの 2 語彙を type で判別する（規則 R-5）。type が validation-error なら
// 契約語彙、invalid-input ならドメイン語彙。分岐は type → code の 2 段になる。
const (
	// CodeRequired は必須プロパティの欠落（validate.ErrFieldRequired）。
	CodeRequired = "required"
	// CodeType は型不一致。構造化された情報が取れないため、ラップ列の解析から導く。
	CodeType = "type"
	// CodeMinLength は文字列長・配列長の下限違反（*validate.MinLengthError）。
	// ogen は minLength（文字列）と minItems（配列）の双方をこの型で表すため、
	// この code も両方を指す。
	CodeMinLength = "min_length"
	// CodeMaxLength は文字列長・配列長の上限違反（*validate.MaxLengthError）。
	CodeMaxLength = "max_length"
	// CodePattern は正規表現不一致（*validate.NoRegexMatchError）。
	CodePattern = "pattern"
	// CodeUniqueItems は配列の重複（*validate.DuplicateItemsError）。
	CodeUniqueItems = "unique_items"
	// CodeInvalidParam はパラメータ（query / path / header）の解釈失敗
	// （*ogenerrors.DecodeParamError）。
	CodeInvalidParam = "invalid_param"
	// CodeBodyRequired はリクエストボディが空（validate.ErrBodyRequired）。
	CodeBodyRequired = "body_required"
	// CodeInvalid は上記のどれにも当てはまらない契約違反。汎用のフォールバック。
	CodeInvalid = "invalid"
)

// reasons は契約検証 code に対応する人間可読な定型文。
//
// ogen の検証由来文字列（"len 3 less than minimum 5" など）をそのまま転記しない
// （FR-2.3）。受信値も閾値も載せない — 載せると受信値のエコーバック（FR-2.4）や
// 内部実装の推測材料になる。閾値を知りたいクライアントは OpenAPI 契約を読めばよい。
var reasons = map[string]string{
	CodeRequired:     "必須項目です",
	CodeType:         "型が一致しません",
	CodeMinLength:    "長さが下限を下回っています",
	CodeMaxLength:    "長さが上限を超えています",
	CodePattern:      "形式が正しくありません",
	CodeUniqueItems:  "重複した要素があります",
	CodeInvalidParam: "パラメータを解釈できません",
	CodeBodyRequired: "リクエストボディが必要です",
	CodeInvalid:      "値が不正です",
}

// ReasonOf は契約検証 code に対応する定型文を返す。
// 表に無い code は汎用文言へフォールバックする。code 自体は応答に載るので、
// クライアントは code で分岐でき情報は失われない。
func ReasonOf(code string) string {
	if r, ok := reasons[code]; ok {
		return r
	}
	return reasons[CodeInvalid]
}
