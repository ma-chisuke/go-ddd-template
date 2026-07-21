// Package inventory は「在庫」境界づけられたコンテキストの公開ファサード。
//
// 外部（合成ルートや、将来追加される他コンテキスト）はこの薄いファサードだけに依存し、
// internal/ 配下には決して触れない。Go の internal パッケージ規則により、
// 兄弟モジュールが internal/ を import するとコンパイルエラーになるため、
// 層の境界はコンパイラによって強制される。
package inventory

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/example/go-ddd-template/contexts/inventory/internal/application"
	"github.com/example/go-ddd-template/contexts/inventory/internal/infrastructure/logging"
	"github.com/example/go-ddd-template/contexts/inventory/internal/infrastructure/postgres"
	"github.com/example/go-ddd-template/contexts/inventory/internal/interfaces"
	"github.com/example/go-ddd-template/contexts/inventory/internal/interfaces/openapi"
	"github.com/example/go-ddd-template/shared/uow"
)

// Deps はモジュールの構築に必要な依存を束ねる。
type Deps struct {
	// Pool は PostgreSQL のコネクションプール（必須）。
	Pool *pgxpool.Pool
	// Logger は構造化ロガー。nil の場合は標準出力への JSON ロガーを既定で用いる。
	Logger *slog.Logger
}

// Module は在庫コンテキストの実体。合成ルートは HTTPHandler() をサーバに登録する。
type Module struct {
	handler http.Handler
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

	// 書き込み経路: 楽観的排他制御つきの作業単位。
	exec := uow.NewExecutor()
	work := postgres.NewUnitOfWork(deps.Pool)
	dispatcher := application.NewInProcessDispatcher(log)
	replenisher := application.NewReplenisher(exec, work, dispatcher, log)

	// 読み取り経路: 書き込み用の作業単位を使わず、プール直結の読み取りストアを注入する。
	viewer := application.NewStockViewer(postgres.NewReadStockStore(deps.Pool), log)

	apiHandler := interfaces.NewHandler(replenisher, viewer, log)
	server, err := openapi.NewServer(apiHandler)
	if err != nil {
		return nil, fmt.Errorf("inventory: HTTP サーバの構築に失敗しました: %w", err)
	}

	return &Module{
		handler: interfaces.CorrelationMiddleware(server),
	}, nil
}

// HTTPHandler はこのコンテキストの HTTP ハンドラを返す。
func (m *Module) HTTPHandler() http.Handler {
	return m.handler
}
