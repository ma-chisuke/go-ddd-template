// Package inventory は「在庫」境界づけられたコンテキストの公開ファサード。
//
// 外部（合成ルートや、将来追加される他コンテキスト）はこの薄いファサードだけに依存し、
// internal/ 配下には決して触れない。Go の internal パッケージ規則により、
// 兄弟モジュールが internal/ を import するとコンパイルエラーになるため、
// 層の境界はコンパイラによって強制される。
//
// ファサードは 2 通りの結線を提供する。New は PostgreSQL アダプタで本番／分散構成を
// 組み立て、NewInMemory は Docker/DB を使わない開発・テスト用にインメモリアダプタ
// （モックではなく擬似トランザクションと楽観ロックを備えた本物のアダプタ）で組み立てる。
// どちらも同一の domain / application コードを実行し、差し替わるのはアダプタだけである。
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
	"github.com/example/go-ddd-template/contexts/inventory/internal/adapter/inbound/httpapi"
	"github.com/example/go-ddd-template/contexts/inventory/internal/adapter/inbound/internalhttp"
	"github.com/example/go-ddd-template/contexts/inventory/internal/adapter/inbound/openapi"
	"github.com/example/go-ddd-template/contexts/inventory/internal/adapter/inbound/openapiinternal"
	"github.com/example/go-ddd-template/contexts/inventory/internal/adapter/outbound/memory"
	"github.com/example/go-ddd-template/contexts/inventory/internal/adapter/outbound/postgres"
	"github.com/example/go-ddd-template/contexts/inventory/internal/application"
	"github.com/example/go-ddd-template/contexts/inventory/internal/domain"
	"github.com/example/go-ddd-template/contexts/inventory/port"
	"github.com/example/go-ddd-template/shared/clock"
	"github.com/example/go-ddd-template/shared/correlation/corrhttp"
	"github.com/example/go-ddd-template/shared/event"
	sharedlog "github.com/example/go-ddd-template/shared/logging"
	"github.com/example/go-ddd-template/shared/outbox"
	"github.com/example/go-ddd-template/shared/outbox/logpub"
	"github.com/example/go-ddd-template/shared/uow"
	"github.com/example/go-ddd-template/shared/worker"
)

// 既定の設定値（Deps で 0 が指定されたときに使う）。
const (
	defaultReservationTTL = 30 * time.Second
	defaultReaperInterval = 5 * time.Second
	defaultRelayInterval  = time.Second
	defaultBatchSize      = 100
)

// Deps はモジュールの構築に必要な依存を束ねる（PostgreSQL 構成 = New 用）。
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

// InMemoryDeps は Docker/DB を使わない開発・テスト構成（NewInMemory 用）の依存。
type InMemoryDeps struct {
	// Logger は構造化ロガー。nil の場合は標準出力への JSON ロガーを既定で用いる。
	Logger *slog.Logger
	// ReservationTTL は仮予約（pending）の有効期限。0 の場合は既定値。
	ReservationTTL time.Duration
	// ReaperInterval は期限切れ掃除（Reaper）の実行間隔。0 の場合は既定値。
	ReaperInterval time.Duration
	// Publisher はアウトボックス送信の実トランスポート。在庫コンテキストは v1 では
	// クロスコンテキストへの送信を行わないため、通常は no-op を注入する（nil でもよい）。
	Publisher outbox.Publisher
}

// Module は在庫コンテキストの実体。合成ルートは HTTPHandler() / InternalHTTPHandler() を
// それぞれ公開サーバ・内部サーバに登録し、StartWorkers() で背景ワーカーを起動する。
//
// 併せて、in-process 結線（cmd/dev など）向けにシームのエントリポイント
// （Reserve / Confirm / Release / Deliver / Sweep）を公開する。分散構成では同じ処理へ
// 内部 HTTP 経由で到達するが、同一プロセス結線ではこれらを直接呼べる。
type Module struct {
	public         http.Handler
	internal       http.Handler
	reaper         *application.Reaper
	runner         *outbox.Runner
	reserver       *application.Reserver
	confirmer      *application.Confirmer
	releaser       *application.Releaser
	router         *outbox.Router
	reaperInterval time.Duration
	log            *slog.Logger
}

// New は PostgreSQL アダプタで在庫コンテキストのモジュールを構築する。ここが分散構成の
// 合成ルート（composition root）であり、各層のアダプタを結線する唯一の場所である。
func New(deps Deps) (*Module, error) {
	if deps.Pool == nil {
		return nil, errors.New("inventory: Deps.Pool は必須です")
	}
	log := orLoggerDefault(deps.Logger)

	// 書き込み経路: 楽観的排他制御つきの作業単位（在庫ストア + アウトボックスを同一 tx に束ねる）。
	work := postgres.NewUnitOfWork(deps.Pool)
	// 読み取り経路: 書き込み用の作業単位を使わず、プール直結の読み取りストアを注入する。
	readStock := postgres.NewReadStockStore(deps.Pool)
	// 送信中継が読むアウトボックスストア。
	msgStore := postgres.NewOutboxStore(deps.Pool)

	return assembleModule(log, work, readStock, msgStore, deps.Publisher,
		deps.ReservationTTL, deps.ReaperInterval, deps.RelayInterval)
}

// NewInMemory は Docker/DB を使わない開発・テスト構成でモジュールを構築する。
// 永続化はインメモリアダプタ（本物の実装）で結線し、外部依存を一切持たない。
// 分散構成（New）との差はアダプタだけで、domain / application は同一コードを実行する。
func NewInMemory(deps InMemoryDeps) (*Module, error) {
	log := orLoggerDefault(deps.Logger)

	stockRows := memory.NewStockRows()
	// 配送キュー（送信後に削除される一時的なもの）と恒久イベントログ（追記のみ）を
	// 束ねた Stores を生成し、同一コミットで両方へ確定させる。送信中継へは配送キュー
	// ビュー（stores.Outbox()）を渡す。
	stores := memory.NewStores()
	work := memory.NewUnitOfWork(stockRows, stores)
	readStock := memory.NewReadStockStore(stockRows)

	return assembleModule(log, work, readStock, stores.Outbox(), deps.Publisher,
		deps.ReservationTTL, deps.ReaperInterval, defaultRelayInterval)
}

// assembleModule は結線済みのアダプタ（作業単位・読み取りストア・アウトボックスストア）を
// 受け取り、ユースケース・サーバ・背景ワーカーを組み立てる。New / NewInMemory の共通部で、
// アダプタの構築方法（PostgreSQL / インメモリ）だけを呼び出し側が差し替える。
func assembleModule(
	log *slog.Logger,
	work application.UnitOfWork,
	readStock application.StockStore,
	msgStore outbox.MessageStore,
	publisher outbox.Publisher,
	ttl, reaperInterval, relayInterval time.Duration,
) (*Module, error) {
	ttl = orDurationDefault(ttl, defaultReservationTTL)

	exec := uow.NewExecutor()
	// ドメインイベントの配信機構は共有モジュールの型付きディスパッチャを直接使う。
	// 型引数にこのコンテキストのドメインイベント型を綴ることで、共有された 1 実装が
	// application.EventDispatcher ポート（domain.DomainEvent で宣言されている）を
	// そのまま満たす — アダプタは要らない。あえて per-context の委譲コンストラクタを
	// 置かないのは、「機構は共有・型はコンテキスト固有」という設計が呼び出し側から
	// 見えている状態を保つためである。
	dispatcher := event.NewTyped[domain.DomainEvent](log)

	replenisher := application.NewReplenisher(exec, work, dispatcher, log)
	reserver := application.NewReserver(exec, work, dispatcher, log, ttl)
	confirmer := application.NewConfirmer(exec, work, dispatcher, log)
	releaser := application.NewReleaser(exec, work, dispatcher, log)
	reaper := application.NewReaper(exec, work, dispatcher, clock.System{}, log, defaultBatchSize)
	viewer := application.NewStockViewer(readStock, log)

	// 公開サーバ（補充・照会）。
	//
	// ServerOptions() を必ず渡す。渡さないと ogen の既定エラーハンドラが使われ、
	// デコード失敗・未定義パス・メソッド不許可が problem+json ではなく
	// {"error_message": "operation replenishStock: decode request: ..."} で返る（FR-1）。
	// テストも同じヘルパー経由で組み立て、本番とエラー経路を一致させる（NFR-6）。
	publicHandler := httpapi.NewHandler(replenisher, viewer, log)
	publicServer, err := openapi.NewServer(publicHandler, publicHandler.ServerOptions()...)
	if err != nil {
		return nil, fmt.Errorf("inventory: 公開 HTTP サーバの構築に失敗しました: %w", err)
	}

	// 受信メッセージの種別ディスパッチ（サブスクライバポリシ）を結線する。
	router := outbox.NewRouter()
	router.Register(application.MessageTypeConfirmReservation, application.OnConfirmReservation(confirmer, log))
	router.Register(application.MessageTypeOrderCancelled, application.OnOrderCancelled(releaser))

	// 内部サーバ（予約・確定・解放・メッセージ取り込み）。公開サーバとは別のルート群。
	internalHandler := internalhttp.NewHandler(reserver, confirmer, releaser, router, log)
	internalServer, err := openapiinternal.NewServer(internalHandler, internalHandler.ServerOptions()...)
	if err != nil {
		return nil, fmt.Errorf("inventory: 内部 HTTP サーバの構築に失敗しました: %w", err)
	}

	// アウトボックス送信中継。Publisher が未指定なら開発用 no-op を使う。
	if publisher == nil {
		publisher = logpub.New(log)
	}
	runner := outbox.NewRunner(
		msgStore,
		publisher,
		log,
		outbox.WithInterval(orDurationDefault(relayInterval, defaultRelayInterval)),
		outbox.WithBatch(defaultBatchSize),
	)

	return &Module{
		public:         corrhttp.Middleware(publicServer),
		internal:       corrhttp.Middleware(internalServer),
		reaper:         reaper,
		runner:         runner,
		reserver:       reserver,
		confirmer:      confirmer,
		releaser:       releaser,
		router:         router,
		reaperInterval: orDurationDefault(reaperInterval, defaultReaperInterval),
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

// Reserve は予約参照 ref に対して items をまとめて予約するシームのエントリポイント。
// 翻訳済み DTO port.SKUQty を受け取り、内部ユースケースへ委譲する（在庫不足なら
// domain.ErrInsufficientStock、在庫項目が無ければ domain.ErrStockItemNotFound）。
//
// 分散構成では在庫の内部 HTTP（POST /reservations）越しに同じ処理へ到達する。同一プロセス
// 結線（cmd/dev の in-process ACL）ではこのメソッドを直接呼ぶ。
func (m *Module) Reserve(ctx context.Context, ref string, items []port.SKUQty) error {
	lines := make([]application.ReserveLine, 0, len(items))
	for _, it := range items {
		lines = append(lines, application.ReserveLine{SKU: it.SKU, Quantity: it.Qty})
	}
	return m.reserver.Reserve(ctx, application.ReserveInput{Ref: ref, Lines: lines})
}

// Confirm は予約参照 ref の予約を確定するシームのエントリポイント（二相予約の第 2 相）。
func (m *Module) Confirm(ctx context.Context, ref string) error {
	return m.confirmer.Confirm(ctx, ref)
}

// Release は予約参照 ref の予約を解放するシームのエントリポイント（冪等）。
func (m *Module) Release(ctx context.Context, ref string) error {
	return m.releaser.Release(ctx, ref)
}

// Deliver はクロスコンテキストの受信メッセージを、その種別（message_type）に応じた
// 購読ポリシ（OnConfirmReservation / OnOrderCancelled）へ振り分けるシームのエントリポイント。
// 分散構成では内部 HTTP の event-ingest（POST /events）が同じ outbox.Router へ委譲する。
// 同一プロセス結線では、送信側の同期 publisher がこのメソッドを直接呼ぶ。
func (m *Module) Deliver(ctx context.Context, msg outbox.Message) error {
	return m.router.Deliver(ctx, msg)
}

// Sweep は期限切れの仮予約を 1 回分掃除する（Reaper を明示的に駆動する）。背景ワーカー
// （StartWorkers）が一定間隔で呼ぶのと同じ処理であり、テストやデモから決定的に駆動できる。
func (m *Module) Sweep(ctx context.Context) error {
	return m.reaper.Sweep(ctx)
}

// StartWorkers は背景ワーカー（期限切れ掃除の Reaper と、アウトボックス送信中継の Runner）を
// 起動する。ctx がキャンセルされると両ワーカーは停止する。各ループは recover-and-log で
// 隔離し、想定外の panic でサービス全体を巻き込まないようにする。
func (m *Module) StartWorkers(ctx context.Context) {
	go worker.Safely(ctx, m.log, "reaper", m.runReaperLoop)
	go worker.Safely(ctx, m.log, "outbox-relay", func(ctx context.Context) { _ = m.runner.Run(ctx) })
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

func orDurationDefault(v, def time.Duration) time.Duration {
	if v <= 0 {
		return def
	}
	return v
}

func orLoggerDefault(log *slog.Logger) *slog.Logger {
	if log == nil {
		return sharedlog.New(os.Stdout, slog.LevelInfo)
	}
	return log
}
