// Package inventory は「在庫」境界づけられたコンテキストの公開ファサード。
//
// 外部（合成ルートや、将来追加される他コンテキスト）はこの薄いファサードだけに依存し、
// internal/ 配下には決して触れない。Go の internal パッケージ規則により、
// 兄弟モジュールが internal/ を import するとコンパイルエラーになるため、
// 層の境界はコンパイラによって強制される。
package inventory

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	// ディレクトリ名 http とパッケージ名 httpapi が異なるため、明示的に別名を付ける
	// （パッケージ名を httpapi にしているのは、取り込み側で標準ライブラリ net/http と
	// 識別子が衝突しないようにするため）。
	httpapi "github.com/example/go-ddd-template/contexts/inventory/internal/adapter/inbound/http"
	"github.com/example/go-ddd-template/contexts/inventory/internal/adapter/inbound/internalhttp"
	"github.com/example/go-ddd-template/contexts/inventory/internal/adapter/inbound/openapi"
	"github.com/example/go-ddd-template/contexts/inventory/internal/adapter/inbound/openapiinternal"
	"github.com/example/go-ddd-template/contexts/inventory/internal/adapter/outbound/logging"
	"github.com/example/go-ddd-template/contexts/inventory/internal/adapter/outbound/postgres"
	"github.com/example/go-ddd-template/contexts/inventory/internal/application"
	"github.com/example/go-ddd-template/shared/outbox"
	"github.com/example/go-ddd-template/shared/uow"
)

// 既定の設定値（Deps で 0 が指定されたときに使う）。
const (
	defaultReservationTTL = 30 * time.Second
	defaultReaperInterval = 5 * time.Second
	defaultRelayInterval  = time.Second
	defaultBatchSize      = 100
)

// Deps はモジュールの構築に必要な依存を束ねる。
type Deps struct {
	// Pool は PostgreSQL のコネクションプール（必須）。
	Pool *pgxpool.Pool
	// Logger は構造化ロガー。nil の場合は標準出力への JSON ロガーを既定で用いる。
	Logger *slog.Logger
	// ReservationTTL は仮予約（pending）の有効期限。0 の場合は既定値。
	ReservationTTL time.Duration
	// ReaperInterval は期限切れ掃除（Reaper）の実行間隔。0 の場合は既定値。
	ReaperInterval time.Duration
	// RelayInterval はアウトボックス送信中継の実行間隔。0 の場合は既定値。
	RelayInterval time.Duration
	// Publisher はアウトボックス送信の実トランスポート。nil の場合は開発用 no-op を用いる。
	Publisher outbox.Publisher
}

// Module は在庫コンテキストの実体。合成ルートは HTTPHandler() / InternalHTTPHandler() を
// それぞれ公開サーバ・内部サーバに登録し、StartWorkers() で背景ワーカーを起動する。
type Module struct {
	public         http.Handler
	internal       http.Handler
	reaper         *application.Reaper
	runner         *outbox.Runner
	reaperInterval time.Duration
	log            *slog.Logger
}

// New は依存を組み立ててモジュールを構築する。ここが在庫コンテキストの合成ルート
// （composition root）であり、各層のアダプタを結線する唯一の場所である。
func New(deps Deps) (*Module, error) {
	if deps.Pool == nil {
		return nil, errors.New("inventory: Deps.Pool は必須です")
	}
	log := deps.Logger
	if log == nil {
		log = logging.New(os.Stdout, slog.LevelInfo)
	}
	ttl := orDurationDefault(deps.ReservationTTL, defaultReservationTTL)

	// 書き込み経路: 楽観的排他制御つきの作業単位（在庫ストア + アウトボックスを同一 tx に束ねる）。
	exec := uow.NewExecutor()
	work := postgres.NewUnitOfWork(deps.Pool)
	dispatcher := application.NewInProcessDispatcher(log)

	replenisher := application.NewReplenisher(exec, work, dispatcher, log)
	reserver := application.NewReserver(exec, work, dispatcher, log, ttl)
	confirmer := application.NewConfirmer(exec, work, dispatcher, log)
	releaser := application.NewReleaser(exec, work, dispatcher, log)
	reaper := application.NewReaper(exec, work, dispatcher, application.SystemClock{}, log, defaultBatchSize)

	// 読み取り経路: 書き込み用の作業単位を使わず、プール直結の読み取りストアを注入する。
	viewer := application.NewStockViewer(postgres.NewReadStockStore(deps.Pool), log)

	// 公開サーバ（補充・照会）。
	publicHandler := httpapi.NewHandler(replenisher, viewer, log)
	publicServer, err := openapi.NewServer(publicHandler)
	if err != nil {
		return nil, fmt.Errorf("inventory: 公開 HTTP サーバの構築に失敗しました: %w", err)
	}

	// 受信メッセージの種別ディスパッチ（サブスクライバポリシ）を結線する。
	router := outbox.NewRouter()
	router.Register(application.MessageTypeConfirmReservation, application.OnConfirmReservation(confirmer, log))
	router.Register(application.MessageTypeOrderCancelled, application.OnOrderCancelled(releaser))

	// 内部サーバ（予約・確定・解放・メッセージ取り込み）。公開サーバとは別のルート群。
	internalHandler := internalhttp.NewHandler(reserver, confirmer, releaser, router, log)
	internalServer, err := openapiinternal.NewServer(internalHandler)
	if err != nil {
		return nil, fmt.Errorf("inventory: 内部 HTTP サーバの構築に失敗しました: %w", err)
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
		public:         httpapi.CorrelationMiddleware(publicServer),
		internal:       httpapi.CorrelationMiddleware(internalServer),
		reaper:         reaper,
		runner:         runner,
		reaperInterval: orDurationDefault(deps.ReaperInterval, defaultReaperInterval),
		log:            log,
	}, nil
}

// HTTPHandler はこのコンテキストの公開 HTTP ハンドラ（補充・照会）を返す。
func (m *Module) HTTPHandler() http.Handler {
	return m.public
}

// InternalHTTPHandler はこのコンテキストの内部 HTTP ハンドラ（予約・確定・解放・取り込み）を返す。
func (m *Module) InternalHTTPHandler() http.Handler {
	return m.internal
}

// StartWorkers は背景ワーカー（期限切れ掃除の Reaper と、アウトボックス送信中継の Runner）を
// 起動する。ctx がキャンセルされると両ワーカーは停止する。各ループは recover-and-log で
// 隔離し、想定外の panic でサービス全体を巻き込まないようにする。
func (m *Module) StartWorkers(ctx context.Context) {
	go m.safely(ctx, "reaper", m.runReaperLoop)
	go m.safely(ctx, "outbox-relay", func(ctx context.Context) { _ = m.runner.Run(ctx) })
}

// runReaperLoop は一定間隔で Sweep を呼ぶ。エラーはログに残して継続する。
func (m *Module) runReaperLoop(ctx context.Context) {
	ticker := time.NewTicker(m.reaperInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := m.reaper.Sweep(ctx); err != nil {
				m.log.WarnContext(ctx, "期限切れ掃除でエラーが発生しました", "error", err)
			}
		}
	}
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
