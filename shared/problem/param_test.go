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
		{"空", nil, ""},
		{"単一", []string{"quantity"}, "quantity"},
		{"入れ子", []string{"lines", "unitPrice", "amount"}, "lines.unitPrice.amount"},
		{"添字はドットを挟まない", []string{"lines", "[0]"}, "lines[0]"},
		{"添字のあとはドットで繋ぐ", []string{"lines", "[0]", "quantity"}, "lines[0].quantity"},
		{"深い入れ子と添字", []string{"lines", "[0]", "unitPrice", "amount"}, "lines[0].unitPrice.amount"},
		{"多次元の添字", []string{"matrix", "[1]", "[2]"}, "matrix[1][2]"},
		{"空の断片は無視する", []string{"", "lines", "", "[0]"}, "lines[0]"},
		{"先頭が添字でも壊れない", []string{"[0]", "sku"}, "[0].sku"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, problem.JoinPath(tc.segs))
		})
	}
}

func TestTitleOf(t *testing.T) {
	t.Run("台帳にある種別は固有の title を返す", func(t *testing.T) {
		assert.Equal(t, "Not Found", problem.TitleOf(problem.TypeNotFound, "fallback"))
		assert.Equal(t, "Resource Not Found", problem.TitleOf(problem.TypeResourceNotFound, "fallback"))
		assert.Equal(t, "Conflict", problem.TitleOf(problem.TypeConflict, "fallback"))
		assert.Equal(t, "Reservation Rejected", problem.TitleOf(problem.TypeReservationRejected, "fallback"))
	})

	t.Run("台帳に無い種別は fallback を返す", func(t *testing.T) {
		assert.Equal(t, http.StatusText(http.StatusTeapot), problem.TitleOf("unknown", http.StatusText(http.StatusTeapot)))
	})

	// 規則 R-3: title から type を逆引きできること。status を共有する 2 つの種別
	// （404 が 2 つ、409 が 2 つ）にも別の title が付いていることを機械的に固定する。
	t.Run("title は type と 1 対 1（重複しない）", func(t *testing.T) {
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
	t.Run("語彙にある code は定型文を返す", func(t *testing.T) {
		assert.Equal(t, "必須項目です", problem.ReasonOf(problem.CodeRequired))
		assert.Equal(t, "型が一致しません", problem.ReasonOf(problem.CodeType))
	})

	t.Run("未知の code は汎用文言へフォールバックする", func(t *testing.T) {
		assert.Equal(t, problem.ReasonOf(problem.CodeInvalid), problem.ReasonOf("なにか未知の code"))
		assert.NotEmpty(t, problem.ReasonOf("なにか未知の code"))
	})

	// FR-2.3 / FR-2.4: reason は定型文であり、ogen 由来の文言も受信値も閾値も含まない。
	t.Run("reason に ogen 由来の文言や数値が混ざらない", func(t *testing.T) {
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
