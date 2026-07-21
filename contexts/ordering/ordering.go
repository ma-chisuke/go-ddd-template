// Package ordering は「注文」境界づけられたコンテキストの公開ファサード。
//
// 外部（合成ルートや、将来追加される他コンテキスト）はこの薄いファサードだけに依存し、
// internal/ 配下には決して触れない。Go の internal パッケージ規則により、
// 兄弟モジュールが internal/ を import するとコンパイルエラーになるため、
// 層の境界はコンパイラによって強制される。
//
// 在庫コンテキストへの結合は、Deps 経由で注入される腐敗防止層ポート（StockReserver）と
// アウトボックス送信トランスポート（outbox.Publisher）に閉じている。分散構成では
// これらは生成クライアント clients/inventory を用いる HTTP アダプタ（aclhttp / eventhttp）で
// 満たされ、注文サービスは在庫の Go パッケージをリンクせず HTTP 越しにのみ到達する。
package ordering

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	httpapi "github.com/example/go-ddd-template/contexts/ordering/internal/adapter/inbound/http"
	"github.com/example/go-ddd-template/contexts/ordering/internal/adapter/inbound/openapi"
	"github.com/example/go-ddd-template/contexts/ordering/internal/adapter/outbound/logging"
	"github.com/example/go-ddd-template/contexts/ordering/internal/adapter/outbound/postgres"
	"github.com/example/go-ddd-template/contexts/ordering/internal/application"
	"github.com/example/go-ddd-template/shared/outbox"
	"github.com/example/go-ddd-template/shared/uow"
)

// 既定の設定値（Deps で 0 が指定されたときに使う）。
const (
	defaultRelayInterval = time.Second
	defaultBatchSize     = 100
)

// Deps はモジュールの構築に必要な依存を束ねる。
type Deps struct {
	// Pool は PostgreSQL のコネクションプール（必須）。
	Pool *pgxpool.Pool
	// Reserver は在庫予約の腐敗防止層（ACL）ポート（必須）。分散構成では aclhttp、
	// 開発用の in-process 構成では在庫モジュールを直接呼ぶアダプタを注入する。
	Reserver application.StockReserver
	// Logger は構造化ロガー。nil の場合は標準出力への JSON ロガーを既定で用いる。
	Logger *slog.Logger
	// Publisher はアウトボックス送信の実トランスポート。nil の場合は開発用 no-op を用いる。
	// 分散構成では eventhttp.Publisher（在庫の event-ingest への HTTP push）を注入する。
	Publisher outbox.Publisher
	// RelayInterval はアウトボックス送信中継の実行間隔。0 の場合は既定値。
	RelayInterval time.Duration
}

// Module は注文コンテキストの実体。合成ルートは HTTPHandler() を公開サーバに登録し、
// StartWorkers() で背景ワーカー（アウトボックス送信中継）を起動する。
type Module struct {
	public http.Handler
	runner *outbox.Runner
	log    *slog.Logger
}

// New は依存を組み立ててモジュールを構築する。ここが注文コンテキストの合成ルート
// （composition root）であり、各層のアダプタを結線する唯一の場所である。
func New(deps Deps) (*Module, error) {
	if deps.Pool == nil {
		return nil, errors.New("ordering: Deps.Pool は必須です")
	}
	if deps.Reserver == nil {
		return nil, errors.New("ordering: Deps.Reserver（在庫予約の ACL ポート）は必須です")
	}
	log := deps.Logger
	if log == nil {
		log = logging.New(os.Stdout, slog.LevelInfo)
	}

	// 書き込み経路: 楽観的排他制御つきの作業単位（注文ストア + アウトボックスを同一 tx に束ねる）。
	exec := uow.NewExecutor()
	work := postgres.NewUnitOfWork(deps.Pool)
	dispatcher := application.NewInProcessDispatcher(log)

	place := application.NewPlaceOrder(exec, work, deps.Reserver, dispatcher, log)
	cancel := application.NewCancelOrder(exec, work, log)

	// 読み取り経路: 書き込み用の作業単位を使わず、プール直結の読み取りストアを注入する。
	get := application.NewGetOrder(postgres.NewReadOrderStore(deps.Pool), log)

	// 公開サーバ（作成・照会・取消）。
	handler := httpapi.NewHandler(place, get, cancel, log)
	server, err := openapi.NewServer(handler)
	if err != nil {
		return nil, fmt.Errorf("ordering: 公開 HTTP サーバの構築に失敗しました: %w", err)
	}

	// アウトボックス送信中継。Publisher が未指定なら開発用 no-op を使う。
	publisher := deps.Publisher
	if publisher == nil {
		publisher = logging.NewPublisher(log)
	}
	runner := outbox.NewRunner(
		postgres.NewOutboxStore(deps.Pool),
		publisher,
		log,
		outbox.WithInterval(orDurationDefault(deps.RelayInterval, defaultRelayInterval)),
		outbox.WithBatch(defaultBatchSize),
	)

	return &Module{
		public: httpapi.CorrelationMiddleware(server),
		runner: runner,
		log:    log,
	}, nil
}

// HTTPHandler はこのコンテキストの公開 HTTP ハンドラ（作成・照会・取消）を返す。
func (m *Module) HTTPHandler() http.Handler {
	return m.public
}

// StartWorkers は背景ワーカー（アウトボックス送信中継の Runner）を起動する。
// ctx がキャンセルされるとワーカーは停止する。ループは recover-and-log で隔離し、
// 想定外の panic でサービス全体を巻き込まないようにする。
func (m *Module) StartWorkers(ctx context.Context) {
	go m.safely(ctx, "outbox-relay", func(ctx context.Context) { _ = m.runner.Run(ctx) })
}

// safely は fn を recover-and-log で包んで実行する（panic でサービスを巻き込まない）。
func (m *Module) safely(ctx context.Context, name string, fn func(context.Context)) {
	defer func() {
		if r := recover(); r != nil {
			m.log.ErrorContext(ctx, "背景ワーカーが panic から回復しました", "worker", name, "panic", r)
		}
	}()
	fn(ctx)
}

func orDurationDefault(v, def time.Duration) time.Duration {
	if v <= 0 {
		return def
	}
	return v
}
