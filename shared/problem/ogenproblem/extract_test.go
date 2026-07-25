package ogenproblem_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/ogen-go/ogen/ogenerrors"
	"github.com/ogen-go/ogen/validate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/go-ddd-template/shared/problem"
	"github.com/example/go-ddd-template/shared/problem/ogenproblem"
)

// このファイルには 2 種類のテストがある。役割が違うので混ぜないこと。
//
// (A) 特性テスト（TestOgenCharacteristics_*）
//
//	目的は診断性。抽出アルゴリズムが依存している「ogen の振る舞いに関する仮定」を
//	そのまま表明する。ogen の版を上げてこれが落ちたら、原因は「ogen の内部エラー形式が
//	変わった」の一点であると即座に分かる。負の仮定（型不一致は *validate.Error に
//	ならない）も固定する — そこが変われば抽出の分岐設計そのものを見直す必要があるからだ。
//
// (B) 振る舞いテスト（TestExtractParams_*）
//
//	目的は実害の検知。両方向を捕まえる必要がある。
//	  - 形式変更でフォールバックに落ちた → 期待したパスが出ずに落ちる
//	  - 形式変更で誤ったパスを組んだ    → 期待と違うパスが出て落ちる
//	フォールバック設計だけでは「静かな劣化」（誤情報は出ないが情報が減ったことに誰も
//	気づかない）を防げない。このテストがその穴を塞ぐ。

// ---------------------------------------------------------------------------
// (A) 特性テスト: ogen の振る舞いに関する仮定を固定する
// ---------------------------------------------------------------------------

func TestOgenCharacteristics_RequiredMissingBecomesValidateError(t *testing.T) {
	p := newProber(t)

	t.Run("契約: トップ階層の必須欠落は *validate.Error になり兄弟をすべて列挙する", func(t *testing.T) {
		err := p.send(t, `{}`)

		var ve *validate.Error
		require.ErrorAs(t, err, &ve, "*validate.Error であること")
		require.Len(t, ve.Fields, 3, "同一オブジェクト内の兄弟は打ち切られずに全件入る")

		names := make([]string, 0, len(ve.Fields))
		for _, f := range ve.Fields {
			names = append(names, f.Name)
			assert.ErrorIs(t, f.Error, validate.ErrFieldRequired, "葉は ErrFieldRequired")
		}
		assert.ElementsMatch(t, []string{"name", "lines", "nested"}, names)
	})

	t.Run("契約: 入れ子の必須欠落はラップ列と *validate.Error の両方が値を返す", func(t *testing.T) {
		err := p.send(t, `{"name":"Alpha","lines":[{"sku":"AB","quantity":1}],"nested":{}}`)

		// ラップ列は "nested" までしか降りない。
		assert.Contains(t, err.Error(), `decode field "nested"`)
		// 葉の名前は構造化側にしか無い。
		var ve *validate.Error
		require.ErrorAs(t, err, &ve)
		require.Len(t, ve.Fields, 1)
		assert.Equal(t, "inner", ve.Fields[0].Name)
		// この 2 系統の合成が §4.2 の要である。
	})
}

// 負の仮定。型不一致と不正 JSON は構造化されない。ここが変わったら（構造化されるように
// なったら）、抽出は正規表現ではなく構造から組み立てられるようになるので設計を見直す。
func TestOgenCharacteristics_DecodeFailuresAreNotStructured(t *testing.T) {
	p := newProber(t)

	t.Run("契約: 型不一致は *validate.Error にならない", func(t *testing.T) {
		err := p.send(t, `{"name":"Alpha","lines":[{"sku":"AB","quantity":"x"}],"nested":{"inner":"ok"}}`)

		var ve *validate.Error
		assert.False(t, errors.As(err, &ve), "構造化されない（ゆえにラップ列の解析が要る）")
		assert.Contains(t, err.Error(), `decode field "lines"`)
		assert.Contains(t, err.Error(), `decode field "quantity"`)
	})

	t.Run("契約: 不正 JSON は構造化もラップ列も持たない", func(t *testing.T) {
		err := p.send(t, `{`)

		var ve *validate.Error
		assert.False(t, errors.As(err, &ve))
		assert.NotContains(t, err.Error(), `decode field "`, "フィールドに到達する前に失敗する")
	})
}

// Validate() 経路。現行の 3 契約は本文プロパティに制約を持たない（OOS-4）ためこの経路は
// 発火しないが、利用者が契約に制約を足した瞬間に正しく動く必要がある（NFR-7）。
func TestOgenCharacteristics_ValidatePathNestsAndIndexesArrays(t *testing.T) {
	p := newProber(t)

	t.Run("契約: 配列要素は Name が \"[0]\" の FieldError で包まれ中身が入れ子になる", func(t *testing.T) {
		err := p.send(t, `{"name":"Alpha","lines":[{"sku":"A","quantity":1}],"nested":{"inner":"ok"}}`)

		var ve *validate.Error
		require.ErrorAs(t, err, &ve)
		require.Len(t, ve.Fields, 1)
		assert.Equal(t, "lines", ve.Fields[0].Name)

		var elems *validate.Error
		require.ErrorAs(t, ve.Fields[0].Error, &elems, "配列は入れ子の *validate.Error になる")
		require.Len(t, elems.Fields, 1)
		assert.Equal(t, "[0]", elems.Fields[0].Name, "配列要素の名前は添字そのもの")

		var leaf *validate.Error
		require.ErrorAs(t, elems.Fields[0].Error, &leaf)
		assert.Equal(t, "sku", leaf.Fields[0].Name)
	})

	t.Run("契約: Validate() 経路にはラップ列が無い", func(t *testing.T) {
		err := p.send(t, `{"name":"Ab","lines":[{"sku":"AB","quantity":1}],"nested":{"inner":"ok"}}`)
		assert.NotContains(t, err.Error(), `decode field "`,
			"Decode() を通過してから検証で落ちるため、ラップ列は積まれない")
	})

	t.Run("契約: 複数要素の同時違反は打ち切られずに全件入る", func(t *testing.T) {
		err := p.send(t, `{"name":"Alpha","lines":[{"sku":"A","quantity":1},{"sku":"B","quantity":1}],"nested":{"inner":"ok"}}`)

		var ve *validate.Error
		require.ErrorAs(t, err, &ve)
		var elems *validate.Error
		require.ErrorAs(t, ve.Fields[0].Error, &elems)
		assert.Len(t, elems.Fields, 2, "Validate() のループは失敗要素があっても走査し尽くす")
	})
}

// 葉のエラー型。code への写像がこれに依存している。
func TestOgenCharacteristics_LeafErrorTypes(t *testing.T) {
	p := newProber(t)

	leafOf := func(t *testing.T, err error, depth int) error {
		t.Helper()
		var ve *validate.Error
		require.ErrorAs(t, err, &ve)
		cur := ve
		for range depth {
			require.Len(t, cur.Fields, 1)
			var next *validate.Error
			require.ErrorAs(t, cur.Fields[0].Error, &next)
			cur = next
		}
		require.Len(t, cur.Fields, 1)
		return cur.Fields[0].Error
	}

	t.Run("契約: minLength と minItems はどちらも *validate.MinLengthError", func(t *testing.T) {
		var minLen *validate.MinLengthError

		strErr := leafOf(t, p.send(t, `{"name":"Ab","lines":[{"sku":"AB","quantity":1}],"nested":{"inner":"ok"}}`), 0)
		assert.ErrorAs(t, strErr, &minLen, "文字列の minLength")

		arrErr := leafOf(t, p.send(t, `{"name":"Alpha","lines":[],"nested":{"inner":"ok"}}`), 0)
		assert.ErrorAs(t, arrErr, &minLen, "配列の minItems も同じ型（ゆえに code も同じ）")
	})

	t.Run("契約: maxLength は *validate.MaxLengthError", func(t *testing.T) {
		err := leafOf(t, p.send(t, `{"name":"ABCDEFGHIJK","lines":[{"sku":"AB","quantity":1}],"nested":{"inner":"ok"}}`), 0)
		var maxLen *validate.MaxLengthError
		assert.ErrorAs(t, err, &maxLen)
	})

	t.Run("契約: pattern は *validate.NoRegexMatchError", func(t *testing.T) {
		err := leafOf(t, p.send(t, `{"name":"abcd","lines":[{"sku":"AB","quantity":1}],"nested":{"inner":"ok"}}`), 0)
		var noMatch *validate.NoRegexMatchError
		assert.ErrorAs(t, err, &noMatch)
	})

	// 負の仮定。ogen v1.23.0 では uniqueItems / enum の違反は専用の型にならず、
	// 受信値を含む素のエラーになる。だから CodeInvalid へ落として文言ごと捨てる。
	// 専用型になったら（＝ここが落ちたら）語彙を細かくできる。
	t.Run("契約: uniqueItems は *validate.DuplicateItemsError にならない", func(t *testing.T) {
		err := leafOf(t, p.send(t, `{"name":"Alpha","tags":["a","a"],"lines":[{"sku":"AB","quantity":1}],"nested":{"inner":"ok"}}`), 0)
		var dup *validate.DuplicateItemsError
		assert.False(t, errors.As(err, &dup), "専用型ではない（受信値を含む素のエラー）")
		assert.Contains(t, err.Error(), "duplicate", "受信値を含むので文言は外へ出せない")
	})

	t.Run("契約: enum 違反は専用の型にならない", func(t *testing.T) {
		err := leafOf(t, p.send(t, `{"name":"Alpha","kind":"gamma","lines":[{"sku":"AB","quantity":1}],"nested":{"inner":"ok"}}`), 0)
		assert.Contains(t, err.Error(), "gamma", "受信値をそのまま含むので文言は外へ出せない")
	})
}

func TestOgenCharacteristics_ParamAndBodyErrors(t *testing.T) {
	p := newProber(t)

	t.Run("契約: パラメータの解釈失敗は *ogenerrors.DecodeParamError で名前を持つ", func(t *testing.T) {
		for _, tc := range []struct{ name, query string }{
			{"型不一致", "?attempt=xyz"},
			{"欠落", ""},
		} {
			t.Run(tc.name, func(t *testing.T) {
				err := p.sendWith(t, tc.query, "application/json", validBody)
				var pe *ogenerrors.DecodeParamError
				require.ErrorAs(t, err, &pe)
				assert.Equal(t, "attempt", pe.Name)
			})
		}
	})

	t.Run("契約: 空ボディは validate.ErrBodyRequired", func(t *testing.T) {
		err := p.sendWith(t, "?attempt=1", "application/json", "")
		assert.ErrorIs(t, err, validate.ErrBodyRequired)
	})

	t.Run("契約: Content-Type 不正は *validate.InvalidContentTypeError", func(t *testing.T) {
		err := p.sendWith(t, "?attempt=1", "text/plain", validBody)
		var ct *validate.InvalidContentTypeError
		assert.ErrorAs(t, err, &ct)
	})
}

// ---------------------------------------------------------------------------
// (B) 振る舞いテスト: 抽出結果そのもの
// ---------------------------------------------------------------------------

// TestExtractParams_BuildsParamPaths は Validate() 経路と Decode() 経路の両方について
// invalid-params の name（フィールドパス）と code の組み立てを網羅する。
func TestExtractParams_BuildsParamPaths(t *testing.T) {
	p := newProber(t)

	cases := []struct {
		name string
		body string
		want []problem.Param
	}{
		{
			name: "境界: トップ階層の必須欠落は兄弟を全件列挙する",
			body: `{}`,
			want: []problem.Param{
				{Name: "name", Code: problem.CodeRequired},
				{Name: "lines", Code: problem.CodeRequired},
				{Name: "nested", Code: problem.CodeRequired},
			},
		},
		{
			name: "境界: 入れ子の必須欠落はラップ列と構造化を合成する",
			body: `{"name":"Alpha","lines":[{"sku":"AB","quantity":1}],"nested":{}}`,
			want: []problem.Param{{Name: "nested.inner", Code: problem.CodeRequired}},
		},
		{
			name: "境界: 配列要素の必須欠落は Decode() 経路なので添字が付かない（規則 R-9）",
			body: `{"name":"Alpha","lines":[{"sku":"AB"}],"nested":{"inner":"ok"}}`,
			want: []problem.Param{{Name: "lines.quantity", Code: problem.CodeRequired}},
		},
		{
			name: "異常系: 型不一致はラップ列だけからパスを組む",
			body: `{"name":"Alpha","lines":[{"sku":"AB","quantity":"x"}],"nested":{"inner":"ok"}}`,
			want: []problem.Param{{Name: "lines.quantity", Code: problem.CodeType}},
		},
		{
			name: "異常系: 不正 JSON は特定できないので nil を返す（invalid-params ごと省略）",
			body: `{`,
			want: nil,
		},
		{
			name: "境界: Validate() 経路の minLength は name を min_length で返す",
			body: `{"name":"Ab","lines":[{"sku":"AB","quantity":1}],"nested":{"inner":"ok"}}`,
			want: []problem.Param{{Name: "name", Code: problem.CodeMinLength}},
		},
		{
			name: "境界: Validate() 経路の maxLength は name を max_length で返す",
			body: `{"name":"ABCDEFGHIJK","lines":[{"sku":"AB","quantity":1}],"nested":{"inner":"ok"}}`,
			want: []problem.Param{{Name: "name", Code: problem.CodeMaxLength}},
		},
		{
			name: "異常系: Validate() 経路の pattern は name を pattern で返す",
			body: `{"name":"abcd","lines":[{"sku":"AB","quantity":1}],"nested":{"inner":"ok"}}`,
			want: []problem.Param{{Name: "name", Code: problem.CodePattern}},
		},
		{
			name: "境界: Validate() 経路の minItems は lines を min_length で返す（配列長も MinLengthError）",
			body: `{"name":"Alpha","lines":[],"nested":{"inner":"ok"}}`,
			want: []problem.Param{{Name: "lines", Code: problem.CodeMinLength}},
		},
		{
			name: "境界: Validate() 経路は配列要素に添字を付ける（再帰降下の中核）",
			body: `{"name":"Alpha","lines":[{"sku":"A","quantity":1}],"nested":{"inner":"ok"}}`,
			want: []problem.Param{{Name: "lines[0].sku", Code: problem.CodeMinLength}},
		},
		{
			name: "境界: Validate() 経路の添字は要素の位置を指し常に [0] ではない",
			body: `{"name":"Alpha","lines":[{"sku":"AB","quantity":1},{"sku":"A","quantity":1}],"nested":{"inner":"ok"}}`,
			want: []problem.Param{{Name: "lines[1].sku", Code: problem.CodeMinLength}},
		},
		{
			name: "境界: Validate() 経路は 3 段の入れ子（配列 → オブジェクト → 葉）までパスを組む",
			body: `{"name":"Alpha","lines":[{"sku":"AB","quantity":1,"price":{"amount":1,"currency":"JP"}}],"nested":{"inner":"ok"}}`,
			want: []problem.Param{{Name: "lines[0].price.currency", Code: problem.CodeMinLength}},
		},
		{
			name: "境界: Validate() 経路は複数要素の同時違反を全件列挙する",
			body: `{"name":"Alpha","lines":[{"sku":"A","quantity":1},{"sku":"B","quantity":1}],"nested":{"inner":"ok"}}`,
			want: []problem.Param{
				{Name: "lines[0].sku", Code: problem.CodeMinLength},
				{Name: "lines[1].sku", Code: problem.CodeMinLength},
			},
		},
		{
			name: "境界: Validate() 経路は入れ子オブジェクトのパスを組む",
			body: `{"name":"Alpha","lines":[{"sku":"AB","quantity":1}],"nested":{"inner":"x"}}`,
			want: []problem.Param{{Name: "nested.inner", Code: problem.CodeMinLength}},
		},
		{
			name: "異常系: 語彙に無い制約（enum・uniqueItems）は汎用 code へ落とす",
			body: `{"name":"Alpha","kind":"gamma","lines":[{"sku":"AB","quantity":1}],"nested":{"inner":"ok"}}`,
			want: []problem.Param{{Name: "kind", Code: problem.CodeInvalid}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ogenproblem.ExtractParams(p.send(t, tc.body))
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestExtractParams_ParamsAndBody(t *testing.T) {
	p := newProber(t)

	t.Run("異常系: パラメータの解釈失敗は attempt を invalid_param として返す", func(t *testing.T) {
		got := ogenproblem.ExtractParams(p.sendWith(t, "?attempt=xyz", "application/json", validBody))
		assert.Equal(t, []problem.Param{{Name: "attempt", Code: problem.CodeInvalidParam}}, got)
	})

	t.Run("境界: 空ボディは body を body_required として返す", func(t *testing.T) {
		got := ogenproblem.ExtractParams(p.sendWith(t, "?attempt=1", "application/json", ""))
		assert.Equal(t, []problem.Param{{Name: ogenproblem.BodyParamName, Code: problem.CodeBodyRequired}}, got)
	})

	t.Run("異常系: Content-Type 不正はフィールドに帰着しないので nil を返す", func(t *testing.T) {
		got := ogenproblem.ExtractParams(p.sendWith(t, "?attempt=1", "text/plain", validBody))
		assert.Nil(t, got)
	})

	t.Run("境界: nil は nil を返す", func(t *testing.T) {
		assert.Nil(t, ogenproblem.ExtractParams(nil))
	})

	t.Run("異常系: ogen と無関係のエラーは nil を返し誤ったパスを組まない", func(t *testing.T) {
		assert.Nil(t, ogenproblem.ExtractParams(errors.New("なにか別のエラー")))
	})
}

// 抽出結果に ogen / Go 由来の文言や受信値が混ざらないこと（FR-2.1〜FR-2.4 / NFR-1）。
// name は契約に書かれたプロパティ名だけ、code は本プロジェクトの語彙だけ。
func TestExtractParams_LeaksNothingInternal(t *testing.T) {
	p := newProber(t)

	bodies := []string{
		`{}`,
		`{"name":"Alpha","lines":[{"sku":"AB","quantity":"x"}],"nested":{"inner":"ok"}}`,
		`{"name":"Ab","lines":[{"sku":"AB","quantity":1}],"nested":{"inner":"ok"}}`,
		`{"name":"Alpha","kind":"gamma","lines":[{"sku":"AB","quantity":1}],"nested":{"inner":"ok"}}`,
		`{"name":"Alpha","tags":["dup","dup"],"lines":[{"sku":"AB","quantity":1}],"nested":{"inner":"ok"}}`,
	}
	forbidden := []string{
		"operation ", "decode request", "decode params", "callback:", "unexpected byte",
		"less than minimum", "no regex match", "duplicate element",
		"gamma", "dup", "validate", "ogen",
	}

	for _, body := range bodies {
		params := ogenproblem.ExtractParams(p.send(t, body))
		for _, prm := range params {
			for _, bad := range forbidden {
				assert.NotContains(t, prm.Name, bad, "name に内部文言／受信値が混ざらない")
				assert.NotContains(t, prm.Code, bad, "code に内部文言／受信値が混ざらない")
			}
			assert.NotEmpty(t, problem.ReasonOf(prm.Code), "code から reason が引けること")
		}
	}
}

// TestExtractParams_ReceivedValueCannotPoisonPath は、受信値がラップ列のパターン
// `decode field "X"` を含んでいても、それがパスのセグメントとして混入しないことを固定する。
//
// これは回帰テストである。以前は err.Error() の全体を正規表現で走査していたため、
// validate.Error が葉の受信値（uniqueItems / enum は受信値を文言に埋める）を再帰連結した
// 文字列の中の `decode field "X"` を本物のラップ列と誤認し、攻撃者が選んだ文字列 X を
// invalid-params[].name の先頭に混ぜてしまっていた（規則 R-11 / NFR-1 違反）。
// 走査範囲を validate 部分より前に限定することで塞いだ。
func TestExtractParams_ReceivedValueCannotPoisonPath(t *testing.T) {
	p := newProber(t)

	// tags は uniqueItems。重複する 2 要素の値そのものをラップ列のパターンにする。
	poison := `decode field \"quantity\"`
	body := `{"name":"Alpha","tags":["` + poison + `","` + poison +
		`"],"lines":[{"sku":"AB","quantity":1}],"nested":{"inner":"ok"}}`

	params := ogenproblem.ExtractParams(p.send(t, body))

	require.NotEmpty(t, params, "uniqueItems 違反は tags を報告する")
	for _, prm := range params {
		// 受信値由来の "quantity" がパスに現れてはならない。正しくは "tags" だけ。
		assert.NotContains(t, prm.Name, "quantity",
			"受信値がパスに混入していない（正しくは tags）")
		assert.Equal(t, "tags", prm.Name, "報告されるのは実際に違反したフィールドだけ")
	}
}

func TestStatusOf(t *testing.T) {
	p := newProber(t)

	assert.Equal(t, http.StatusBadRequest, ogenproblem.StatusOf(p.send(t, `{}`)))
	assert.Equal(t, http.StatusUnsupportedMediaType,
		ogenproblem.StatusOf(p.sendWith(t, "?attempt=1", "text/plain", validBody)))
	assert.Equal(t, http.StatusInternalServerError, ogenproblem.StatusOf(errors.New("無関係")),
		"判定できないものは 500（ogen の既定に従う）")
}
