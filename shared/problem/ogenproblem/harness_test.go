package ogenproblem_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/example/go-ddd-template/shared/problem/ogenproblem/internal/fixture/oas"
)

// このパッケージのテストは、手で組み立てた偽の error ではなく **ogen が実際に生成した
// エラー** を検証する。フィクスチャ契約（internal/fixture/openapi.yaml）から生成した
// サーバへ httptest 経由で HTTP を投げ、WithErrorHandler で生の error を捕まえる。
//
// 手書きの偽エラーで代用すると、ogen の版を上げてエラー形式が変わってもテストは緑のまま
// 通り、本番だけが静かに劣化する。それを防ぐのがこの構成の目的である（NFR-5 / A-5）。

// probeHandler は成功応答だけを返す。エラーはすべてデコード／検証段階で起きるので、
// ハンドラ本体は呼ばれない。
type probeHandler struct{}

func (probeHandler) Probe(context.Context, *oas.Probe, oas.ProbeParams) (*oas.Ack, error) {
	return &oas.Ack{Ok: true}, nil
}

// 有効なリクエスト（各テストはここから 1 箇所だけ壊す）。
const validBody = `{"name":"Alpha","lines":[{"sku":"AB","quantity":1}],"nested":{"inner":"ok"}}`

// prober は フィクスチャサーバへリクエストを投げ、ogen が生成した生のエラーを返す。
type prober struct {
	url      string
	client   *http.Client
	captured *error
}

func newProber(t *testing.T) prober {
	t.Helper()
	var captured error
	srv, err := oas.NewServer(probeHandler{}, oas.WithErrorHandler(
		func(_ context.Context, w http.ResponseWriter, _ *http.Request, e error) {
			captured = e
			w.WriteHeader(http.StatusInternalServerError)
		}))
	require.NoError(t, err, "フィクスチャサーバの構築")

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return prober{url: ts.URL, client: ts.Client(), captured: &captured}
}

// send はボディを送り、ogen が生成したエラーを返す。エラーにならなければテストを止める。
func (p prober) send(t *testing.T, body string) error {
	t.Helper()
	return p.sendWith(t, "?attempt=1", "application/json", body)
}

// sendWith はクエリ文字列と Content-Type も指定して送る。
func (p prober) sendWith(t *testing.T, query, contentType, body string) error {
	t.Helper()
	*p.captured = nil

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, p.url+"/probe"+query, strings.NewReader(body))
	require.NoError(t, err, "リクエスト生成")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	resp, err := p.client.Do(req)
	require.NoError(t, err, "送信")
	require.NoError(t, resp.Body.Close())

	require.Error(t, *p.captured, "ogen がエラーを生成すること（生成しないなら前提が崩れている）")
	return *p.captured
}
