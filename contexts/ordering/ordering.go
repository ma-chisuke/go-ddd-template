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
//
// ファサードは 2 通りの結線を提供する。New は PostgreSQL アダプタで本番／分散構成を
// 組み立て、NewInMemory は Docker/DB を使わない開発・テスト用にインメモリアダプタで
// 組み立てる。どちらも同一の domain / application コードを実行し、差し替わるのはアダプタ
// （永続化と、在庫予約 ACL・クロスコンテキスト送信のトランスポート）だけである。
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

	"github.com/example/go-ddd-template/contexts/ordering/internal/adapter/inbound/httpapi"
	"github.com/example/go-ddd-template/contexts/ordering/internal/adapter/inbound/openapi"
	"github.com/example/go-ddd-template/contexts/ordering/internal/adapter/outbound/memory"
	"github.com/example/go-ddd-template/contexts/ordering/internal/adapter/outbound/postgres"
	"github.com/example/go-ddd-template/contexts/ordering/internal/application"
	"github.com/example/go-ddd-template/contexts/ordering/internal/domain"
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
	defaultRelayInterval = time.Second
	defaultBatchSize     = 100
)

// Deps はモジュールの構築に必要な依存を束ねる（PostgreSQL 構成 = New 用）。
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

// InMemoryDeps は Docker/DB を使わない開発・テスト構成（NewInMemory 用）の依存。
type InMemoryDeps struct {
	// Reserver は在庫予約の ACL ポート（必須）。開発用の in-process 構成では、在庫モジュールの
	// 公開シーム（inventory.Module.Reserve/Release）を直接呼ぶアダプタを注入する。
	Reserver application.StockReserver
	// Publisher はクロスコンテキスト送信の同期 in-process トランスポート。設定すると、
	// 集約書き込みと同一トランザクションで積んだメッセージが、コミット直後にその場で
	// ピア（在庫）の受信経路へ配送される（store も poll も介さない）。nil の場合は
	// 配送されない（アウトボックスに積まれるだけ）。
	Publisher outbox.Publisher
	// Logger は構造化ロガー。nil の場合は標準出力への JSON ロガーを既定で用いる。
	Logger *slog.Logger
}

// Module は注文コンテキストの実体。合成ルートは HTTPHandler() を公開サーバに登録し、
// StartWorkers() で背景ワーカー（アウトボックス送信中継）を起動する。
type Module struct {
	public http.Handler
	runner *outbox.Runner // 開発用の同期配送構成では nil（配送はコミット時に同期実行される）
	log    *slog.Logger
}

// New は PostgreSQL アダプタで注文コンテキストのモジュールを構築する。ここが分散構成の
// 合成ルート（composition root）であり、各層のアダプタを結線する唯一の場所である。
func New(deps Deps) (*Module, error) {
	if deps.Pool == nil {
		return nil, errors.New("ordering: Deps.Pool は必須です")
	}
	if deps.Reserver == nil {
		return nil, errors.New("ordering: Deps.Reserver（在庫予約の ACL ポート）は必須です")
	}
	log := orLoggerDefault(deps.Logger)

	// 書き込み経路: 楽観的排他制御つきの作業単位（注文ストア + アウトボックスを同一 tx に束ねる）。
	work := postgres.NewUnitOfWork(deps.Pool)
	// 読み取り経路: 書き込み用の作業単位を使わず、プール直結の読み取りストアを注入する。
	readStore := postgres.NewReadOrderStore(deps.Pool)

	// アウトボックス送信中継。Publisher が未指定なら開発用 no-op を使う。
	publisher := deps.Publisher
	if publisher == nil {
		publisher = logpub.New(log)
	}
	runner := outbox.NewRunner(
		postgres.NewOutboxStore(deps.Pool),
		publisher,
		log,
		outbox.WithInterval(orDurationDefault(deps.RelayInterval, defaultRelayInterval)),
		outbox.WithBatch(defaultBatchSize),
	)

	return build(log, work, readStore, deps.Reserver, runner)
}

// NewInMemory は Docker/DB を使わない開発・テスト構成でモジュールを構築する。
// 永続化はインメモリアダプタ（本物の実装）、クロスコンテキスト送信は同期 in-process
// publisher（コミット時にその場でピアへ配送）で結線する。背景の送信中継（Runner）は
// 使わない（同期配送が担うため runner は nil）。
func NewInMemory(deps InMemoryDeps) (*Module, error) {
	if deps.Reserver == nil {
		return nil, errors.New("ordering: InMemoryDeps.Reserver（在庫予約の ACL ポート）は必須です")
	}
	log := orLoggerDefault(deps.Logger)

	store := memory.NewStore()
	// 配送キュー（送信後に削除される一時的なもの）と恒久イベントログ（追記のみ）を
	// 束ねた Stores を生成し、同一コミットで両方へ確定させる。
	stores := memory.NewStores()
	// コミット時にその場でピアへ配送する同期シンクを結線する（store/poll なし・決定的）。
	work := memory.NewUnitOfWork(store, stores).WithSyncDelivery(deps.Publisher, log)
	readStore := memory.NewReadOrderStore(store)

	// 同期配送が送信を担うため、背景の送信中継（Runner）は起動しない（runner = nil）。
	return build(log, work, readStore, deps.Reserver, nil)
}

// build は結線済みのアダプタ（作業単位・読み取りストア・ACL・送信中継）を受け取り、
// ユースケースと公開サーバを組み立てる。New / NewInMemory の共通部で、アダプタの構築方法
// （PostgreSQL / インメモリ）と送信中継の有無だけを呼び出し側が差し替える。
func build(
	log *slog.Logger,
	work application.UnitOfWork,
	readStore application.OrderStore,
	reserver application.StockReserver,
	runner *outbox.Runner,
) (*Module, error) {
	exec := uow.NewExecutor()
	// ドメインイベントの配信機構は共有モジュールの型付きディスパッチャを直接使う。
	// 型引数にこのコンテキストのドメインイベント型を綴ることで、共有された 1 実装が
	// application.EventDispatcher ポート（domain.DomainEvent で宣言されている）を
	// そのまま満たす — アダプタは要らない。あえて per-context の委譲コンストラクタを
	// 置かないのは、「機構は共有・型はコンテキスト固有」という設計が呼び出し側から
	// 見えている状態を保つためである。
	dispatcher := event.NewTyped[domain.DomainEvent](log)

	place := application.NewPlaceOrder(exec, work, reserver, dispatcher, log)
	cancel := application.NewCancelOrder(exec, work, log)
	get := application.NewGetOrder(readStore, log)

	// 公開サーバ（作成・照会・取消）。
	//
	// ServerOptions() を必ず渡す。渡さないと ogen の既定エラーハンドラが使われ、
	// デコード失敗・未定義パス・メソッド不許可が problem+json ではなく
	// {"error_message": "operation placeOrder: decode request: ..."} で返る（FR-1）。
	// テストも同じヘルパー経由で組み立て、本番とエラー経路を一致させる（NFR-6）。
	handler := httpapi.NewHandler(place, get, cancel, log)
	server, err := openapi.NewServer(handler, handler.ServerOptions()...)
	if err != nil {
		return nil, fmt.Errorf("ordering: 公開 HTTP サーバの構築に失敗しました: %w", err)
	}

	return &Module{
		public: corrhttp.Middleware(server),
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
//
// 開発用の同期配送構成（NewInMemory）では送信中継を持たない（runner = nil）ため、
// 何も起動しない。配送はコミット時に同期実行される。
func (m *Module) StartWorkers(ctx context.Context) {
	if m.runner == nil {
		return
	}
	go worker.Safely(ctx, m.log, "outbox-relay", func(ctx context.Context) { _ = m.runner.Run(ctx) })
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
