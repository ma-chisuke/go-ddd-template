package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/go-ddd-template/shared/problem"
)

// このテストは NFR-6.1 / 規則 R-18 に対する機械的な安全網である。
//
// 背景: FR-6.5 により ProblemDetails スキーマは 3 契約に **重複定義されたまま** 維持する
// （各コンテキストを単体で切り出せるというテンプレートの独立性を優先したため）。放置すると
// 3 つの定義とその実装は静かにドリフトする。
//
// 採用した検出手段は「契約 YAML の同値比較」ではなく **振る舞いの一致** である。契約が
// 同一でも実装がずれれば意味が無く、逆に実装が一致していれば契約の些細な差は無害だからだ。
// 同じ種類の契約違反を 3 サーバへ投げ、応答の形（Content-Type・type・title・detail・
// キー集合）が一致することを確かめる。
//
// cmd/dev がこのテストの置き場所になるのは、ここだけが 3 サーバすべてに公開ファサード
// 経由で到達できるためである（各コンテキストのテストからは他コンテキストへ到達できない）。

// server は比較対象の 1 サーバ。
type server struct {
	// label はサーバの表示名。アサーションメッセージとサブテスト名の組み立てに使う。
	// 「ケース名」ではないので name という名前にはしない — name はテーブル駆動のケース名に
	// 予約されている（docs/testing-conventions.md D-6）。
	label string
	ts    *httptest.Server
	// postPath は「JSON ボディを受け取る POST エンドポイント」。E1 の比較に使う。
	postPath string
}

func newParityServers(t *testing.T) []server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	h, err := newHarness(harnessDeps{logger: log})
	require.NoError(t, err, "ハーネスの構築")

	servers := []server{
		{label: "ordering 公開", ts: httptest.NewServer(h.orderingHandler()), postPath: "/orders"},
		{label: "inventory 公開", ts: httptest.NewServer(h.inventoryHandler()), postPath: "/stock/WIDGET-001/replenish"},
		{label: "inventory 内部", ts: httptest.NewServer(h.inventoryInternalHandler()), postPath: "/reservations"},
	}
	for _, s := range servers {
		t.Cleanup(s.ts.Close)
	}
	return servers
}

// shape は応答の「形」。値のうち、サーバ間で一致していなければならないものだけを持つ。
type shape struct {
	Status      int
	ContentType string
	Type        string
	Title       string
	Detail      string
	// Keys は本文のトップ階層キー（並び順に依存しないよう整列する）。
	Keys []string
	// ParamKeys は invalid-params の要素のキー（要素があるときだけ）。
	ParamKeys []string
}

func readShape(t *testing.T, resp *http.Response) shape {
	t.Helper()
	defer func() { require.NoError(t, resp.Body.Close()) }()

	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err, "本文の読み取り")

	var body map[string]any
	require.NoError(t, json.Unmarshal(raw, &body), "problem+json のデコード: %s", raw)

	s := shape{
		Status:      resp.StatusCode,
		ContentType: resp.Header.Get("Content-Type"),
		Type:        str(body["type"]),
		Title:       str(body["title"]),
		Detail:      str(body["detail"]),
		Keys:        sortedKeys(body),
	}
	if params, ok := body["invalid-params"].([]any); ok && len(params) > 0 {
		first, ok := params[0].(map[string]any)
		require.True(t, ok, "invalid-params の要素はオブジェクト")
		s.ParamKeys = sortedKeys(first)
	}
	return s
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func request(t *testing.T, s server, method, path, contentType, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, s.ts.URL+path, strings.NewReader(body))
	require.NoError(t, err, "リクエスト生成")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := s.ts.Client().Do(req)
	require.NoError(t, err, "送信")
	return resp
}

// TestProblemParity_AcrossThreeServers は同種の契約違反に対する応答の形が 3 サーバで
// 一致することを確かめる。1 つでもサーバオプションの注入を忘れれば、そのサーバだけ
// Content-Type が application/json（ogen の既定）になり、ここで落ちる。
func TestProblemParity_AcrossThreeServers(t *testing.T) {
	servers := newParityServers(t)

	kinds := []struct {
		name string
		// send は 1 サーバに対して「その種類の違反」を起こすリクエストを送る。
		send func(t *testing.T, s server) *http.Response
		// wantTypeSuffix は 3 サーバで一致すべき type サフィックス。
		wantTypeSuffix string
		wantStatus     int
		// wantParams は invalid-params が載るべきかどうか。
		wantParams bool
	}{
		{
			name: "契約: 未定義パス（E2・404）は 3 サーバで同じ形になる",
			send: func(t *testing.T, s server) *http.Response {
				t.Helper()
				return request(t, s, http.MethodGet, "/definitely-not-a-route", "", "")
			},
			wantTypeSuffix: problem.TypeNotFound,
			wantStatus:     http.StatusNotFound,
		},
		{
			name: "契約: 許可外メソッド（E3・405）は 3 サーバで同じ形になる",
			send: func(t *testing.T, s server) *http.Response {
				t.Helper()
				return request(t, s, http.MethodDelete, s.postPath, "", "")
			},
			wantTypeSuffix: problem.TypeMethodNotAllowed,
			wantStatus:     http.StatusMethodNotAllowed,
		},
		{
			name: "契約: サポート外 Content-Type（E1・415）は 3 サーバで同じ形になる",
			send: func(t *testing.T, s server) *http.Response {
				t.Helper()
				return request(t, s, http.MethodPost, s.postPath, "text/plain", `{}`)
			},
			wantTypeSuffix: problem.TypeUnsupportedMediaType,
			wantStatus:     http.StatusUnsupportedMediaType,
		},
		{
			name: "契約: 不正 JSON（E1・400・フィールド特定不能）は 3 サーバで同じ形になる",
			send: func(t *testing.T, s server) *http.Response {
				t.Helper()
				return request(t, s, http.MethodPost, s.postPath, "application/json", `{"x":`)
			},
			wantTypeSuffix: problem.TypeValidationError,
			wantStatus:     http.StatusBadRequest,
		},
		{
			name: "契約: 必須欠落（E1・400・フィールド特定可能）は 3 サーバで同じ形になる",
			send: func(t *testing.T, s server) *http.Response {
				t.Helper()
				return request(t, s, http.MethodPost, s.postPath, "application/json", `{}`)
			},
			wantTypeSuffix: problem.TypeValidationError,
			wantStatus:     http.StatusBadRequest,
			wantParams:     true,
		},
	}

	for _, kind := range kinds {
		t.Run(kind.name, func(t *testing.T) {
			shapes := make(map[string]shape, len(servers))
			for _, s := range servers {
				shapes[s.label] = readShape(t, kind.send(t, s))
			}

			var reference *shape
			var referenceName string
			for _, s := range servers {
				got := shapes[s.label]

				assert.Equal(t, kind.wantStatus, got.Status, "%s: status", s.label)
				assert.Equal(t, "application/problem+json", got.ContentType, "%s: Content-Type", s.label)
				// type は名前空間 + サフィックス。名前空間はコンテキストごとの定数だが、
				// テンプレート既定では 3 つとも同じ値である。
				assert.True(t, strings.HasSuffix(got.Type, "/"+kind.wantTypeSuffix),
					"%s: type が %q で終わること: %s", s.label, kind.wantTypeSuffix, got.Type)

				if kind.wantParams {
					assert.Equal(t, []string{"code", "name", "reason"}, got.ParamKeys,
						"%s: invalid-params の要素構造", s.label)
				} else {
					assert.NotContains(t, got.Keys, "invalid-params",
						"%s: 特定できないときは invalid-params ごと省略する（規則 R-14）", s.label)
				}

				if reference == nil {
					r := got
					reference, referenceName = &r, s.label
					continue
				}
				assert.Equal(t, reference.Type, got.Type, "%s と %s で type が一致しない", referenceName, s.label)
				assert.Equal(t, reference.Title, got.Title, "%s と %s で title が一致しない", referenceName, s.label)
				assert.Equal(t, reference.Detail, got.Detail, "%s と %s で detail が一致しない", referenceName, s.label)
				assert.Equal(t, reference.Keys, got.Keys, "%s と %s で本文のキー集合が一致しない", referenceName, s.label)
				assert.Equal(t, reference.ParamKeys, got.ParamKeys, "%s と %s で invalid-params の構造が一致しない", referenceName, s.label)
			}
		})
	}
}

// 3 サーバのどれからも ogen 由来の文言が出ないこと（FR-2.1〜FR-2.3 / NFR-1）。
// ogen の既定エラーハンドラが 1 つでも残っていればここで落ちる。
func TestProblemParity_NoOgenLeak(t *testing.T) {
	servers := newParityServers(t)
	leaks := []string{
		"error_message", "operation ", "decode request", "decode params",
		"callback:", "unexpected byte", "404 page not found",
	}

	for _, s := range servers {
		t.Run(fmt.Sprintf("契約: %s は ogen 由来の文言を出さない", s.label), func(t *testing.T) {
			for _, resp := range []*http.Response{
				request(t, s, http.MethodPost, s.postPath, "application/json", `{"x":`),
				request(t, s, http.MethodPost, s.postPath, "application/json", `{}`),
				request(t, s, http.MethodGet, "/definitely-not-a-route", "", ""),
				request(t, s, http.MethodDelete, s.postPath, "", ""),
				request(t, s, http.MethodPost, s.postPath, "text/plain", `{}`),
			} {
				raw, err := io.ReadAll(resp.Body)
				require.NoError(t, err)
				require.NoError(t, resp.Body.Close())
				for _, leak := range leaks {
					assert.NotContains(t, string(raw), leak, "内部文言が漏れている")
				}
			}
		})
	}
}
