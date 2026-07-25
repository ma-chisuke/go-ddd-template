package problem_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/example/go-ddd-template/shared/problem"
)

func TestJoinPath(t *testing.T) {
	cases := []struct {
		name string
		segs []string
		want string
	}{
		{name: "境界: 断片が無ければ空文字列になる", segs: nil, want: ""},
		{name: "正常系: 断片 1 つはそのまま返る", segs: []string{"quantity"}, want: "quantity"},
		{name: "正常系: 入れ子はドットで繋ぐ", segs: []string{"lines", "unitPrice", "amount"}, want: "lines.unitPrice.amount"},
		{name: "正常系: 添字はドットを挟まない", segs: []string{"lines", "[0]"}, want: "lines[0]"},
		{name: "正常系: 添字のあとはドットで繋ぐ", segs: []string{"lines", "[0]", "quantity"}, want: "lines[0].quantity"},
		{name: "正常系: 深い入れ子と添字を混ぜても組み立てる", segs: []string{"lines", "[0]", "unitPrice", "amount"}, want: "lines[0].unitPrice.amount"},
		{name: "正常系: 多次元の添字は連結する", segs: []string{"matrix", "[1]", "[2]"}, want: "matrix[1][2]"},
		{name: "境界: 空の断片は無視する", segs: []string{"", "lines", "", "[0]"}, want: "lines[0]"},
		{name: "境界: 先頭が添字でも壊れない", segs: []string{"[0]", "sku"}, want: "[0].sku"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, problem.JoinPath(tc.segs))
		})
	}
}

func TestTitleOf(t *testing.T) {
	t.Run("正常系: 台帳にある種別は固有の title を返す", func(t *testing.T) {
		assert.Equal(t, "Not Found", problem.TitleOf(problem.TypeNotFound, "fallback"))
		assert.Equal(t, "Resource Not Found", problem.TitleOf(problem.TypeResourceNotFound, "fallback"))
		assert.Equal(t, "Conflict", problem.TitleOf(problem.TypeConflict, "fallback"))
		assert.Equal(t, "Reservation Rejected", problem.TitleOf(problem.TypeReservationRejected, "fallback"))
	})

	t.Run("正常系: 台帳に無い種別は fallback を返す", func(t *testing.T) {
		assert.Equal(t, http.StatusText(http.StatusTeapot), problem.TitleOf("unknown", http.StatusText(http.StatusTeapot)))
	})

	// 規則 R-3: title から type を逆引きできること。status を共有する 2 つの種別
	// （404 が 2 つ、409 が 2 つ）にも別の title が付いていることを機械的に固定する。
	t.Run("契約: title は type と 1 対 1 で重複しない", func(t *testing.T) {
		suffixes := []string{
			problem.TypeValidationError,
			problem.TypeUnsupportedMediaType,
			problem.TypeNotFound,
			problem.TypeMethodNotAllowed,
			problem.TypeInvalidInput,
			problem.TypeResourceNotFound,
			problem.TypeConflict,
			problem.TypeReservationRejected,
			problem.TypeServiceUnavailable,
			problem.TypeInternalError,
		}
		seen := make(map[string]string, len(suffixes))
		for _, s := range suffixes {
			title := problem.TitleOf(s, "")
			assert.NotEmpty(t, title, "%s に title が定義されていること", s)
			if prev, dup := seen[title]; dup {
				t.Errorf("title %q が %q と %q で重複している（逆引きできない）", title, prev, s)
			}
			seen[title] = s
		}
	})
}

func TestReasonOf(t *testing.T) {
	t.Run("正常系: 語彙にある code は定型文を返す", func(t *testing.T) {
		assert.Equal(t, "必須項目です", problem.ReasonOf(problem.CodeRequired))
		assert.Equal(t, "型が一致しません", problem.ReasonOf(problem.CodeType))
	})

	t.Run("正常系: 未知の code は汎用文言へフォールバックする", func(t *testing.T) {
		assert.Equal(t, problem.ReasonOf(problem.CodeInvalid), problem.ReasonOf("なにか未知の code"))
		assert.NotEmpty(t, problem.ReasonOf("なにか未知の code"))
	})

	// FR-2.3 / FR-2.4: reason は定型文であり、ogen 由来の文言も受信値も閾値も含まない。
	t.Run("契約: reason は閉じた語彙だけを返し ogen 由来の文言や数値を混ぜない", func(t *testing.T) {
		codes := []string{
			problem.CodeRequired, problem.CodeType, problem.CodeMinLength, problem.CodeMaxLength,
			problem.CodePattern, problem.CodeUniqueItems, problem.CodeInvalidParam,
			problem.CodeBodyRequired, problem.CodeInvalid,
		}
		for _, c := range codes {
			r := problem.ReasonOf(c)
			assert.NotContains(t, r, "less than", "code=%s", c)
			assert.NotContains(t, r, "greater than", "code=%s", c)
			assert.NotContains(t, r, "no regex match", "code=%s", c)
			assert.NotRegexp(t, `[0-9]`, r, "閾値などの数値を載せない: code=%s", c)
		}
	})
}
