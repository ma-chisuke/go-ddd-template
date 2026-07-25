# go-ddd-template のコマンド単一入口。
#
# 「何を打てば動くか」の唯一の情報源はこのファイルである。README.md・AGENTS.md・
# .github/workflows/ci.yml は生のコマンド列を持たず、すべてここのターゲットを呼ぶ。
# 同じ手順が 3 箇所に書き分けられて drift する状態を、構造的に作らないためである。
#
# モジュール一覧は下の変数 1 箇所にある。モジュールを 1 つ足すときに直すのはそこだけで、
# ターゲットは 1 つも触らなくてよい（各ターゲットはモジュール名を列挙せず、変数をシェルループで
# 回す）。ツールの版はここに書かない — 単一情報源は tools/versions.env（横断ツール）と
# 各モジュールの go.mod tool ディレクティブ（コード生成ツール）である。
#
# 引数なしの `make` は help を出す（破壊的な操作を始めない）。

# --- 変数（モジュール一覧の単一情報源） -------------------------------------

# 全 Go モジュール。go.work の use と一致する。モジュールを足すときはここだけを直す。
MODULES       := shared clients/inventory contexts/inventory contexts/ordering cmd/dev

# go generate を持つモジュールと、その実行順。順序に意味がある —
# クライアント → 各コンテキスト → shared（shared はエラー抽出テスト用のフィクスチャ契約を
# 生成するため最後）。cmd/dev に go:generate は無いので含めない。
GEN_MODULES   := clients/inventory contexts/inventory contexts/ordering shared

# 統合テスト（build tag integration。DB 接続時のみ実行される）を持つモジュール。
ITEST_MODULES := contexts/inventory contexts/ordering

# 整形の対象ディレクトリ。gofmt / goimports はモジュール単位ではなくツリー単位で回すため
# MODULES とは別の変数にする。
FMT_DIRS      := shared clients contexts cmd

# golangci-lint の実行ファイル。CI は go install したものを PATH 経由で使い、手元で別パスに
# 置いている場合は `make lint GOLANGCI_LINT=/path/to/golangci-lint` で上書きできる。
# 版は tools/versions.env の GOLANGCI_LINT_VERSION が単一情報源（ここには書かない）。
GOLANGCI_LINT ?= golangci-lint

# 統合テストの接続先。docker compose のデモ用管理者ロール（スキーマ横断の後始末に使う）。
# **デモ専用の資格情報であり、本番では使わない**（docker-compose.yml の既定値と同じ）。
DATABASE_URL  ?= postgres://app:app_admin_demo@localhost:5432/app?sslmode=disable

.DEFAULT_GOAL := help

# ci ターゲットは前提ターゲットを順番に実行することに意味がある（generate が作業ツリーを
# 書き換えたうえで検査する）。並列実行（-j）で順序が崩れないようにする。
.NOTPARALLEL:

# --- ヘルプ ----------------------------------------------------------------

help: ## ターゲット一覧を表示する（引数なし make の既定）
	@echo "使い方: make <target>"
	@echo ""
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z0-9_\/-]+:.*## / {printf "  %-16s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

# --- コード生成 -------------------------------------------------------------

generate: ## 契約 / SQL からコードを生成する（ogen / sqlc / mockgen。生成物はコミットする）
	@set -e; for m in $(GEN_MODULES); do \
		echo "== generate: $$m"; \
		(cd $$m && go generate ./...) || exit 1; \
	done

generate-check: generate ## 生成物が最新であること（生成の冪等性）を検証する
	@if ! git diff --exit-code; then \
		echo "::error::生成物が最新ではありません。'make generate' を実行して差分をコミットしてください。"; \
		exit 1; \
	fi

# --- 整形・静的解析 ---------------------------------------------------------

fmt: ## gofmt と goimports でツリーを整形する
	@gofmt -w $(FMT_DIRS)
	@goimports -w $(FMT_DIRS)

fmt-check: ## 未整形のファイルが無いことを検証する
	@set -e; \
	if [ -n "$$(gofmt -l $(FMT_DIRS))" ]; then \
		echo "::error::gofmt 未整形のファイルがあります:"; gofmt -l $(FMT_DIRS); exit 1; \
	fi; \
	if [ -n "$$(goimports -l $(FMT_DIRS))" ]; then \
		echo "::error::goimports 未整形のファイルがあります:"; goimports -l $(FMT_DIRS); exit 1; \
	fi

vet: ## 全モジュールに go vet をかける
	@set -e; for m in $(MODULES); do \
		echo "== vet: $$m"; \
		(cd $$m && go vet ./...) || exit 1; \
	done

lint: ## 全モジュールに golangci-lint をかける（depguard の層 / seam 境界強制を含む）
	@set -e; for m in $(MODULES); do \
		echo "== lint: $$m"; \
		(cd $$m && $(GOLANGCI_LINT) run ./...) || exit 1; \
	done

# --- ビルド・テスト ---------------------------------------------------------

build: ## 全モジュールをビルドし、統合タグのコンパイル検証も行う
	@set -e; for m in $(MODULES); do \
		echo "== build: $$m"; \
		(cd $$m && go build ./...) || exit 1; \
	done
	@set -e; for m in $(ITEST_MODULES); do \
		echo "== build(tag=integration): $$m"; \
		(cd $$m && go test -tags=integration -run='^$$' ./...) || exit 1; \
	done

test: ## 全モジュールのテストを実行する
	@set -e; for m in $(MODULES); do \
		echo "== test: $$m"; \
		(cd $$m && go test ./...) || exit 1; \
	done

test-race: ## 全モジュールのテストを -race で実行する
	@set -e; for m in $(MODULES); do \
		echo "== test -race: $$m"; \
		(cd $$m && go test -race ./...) || exit 1; \
	done

cover: ## カバレッジゲート（domain + application >= 80% ／ モジュール）
	@bash scripts/coverage-gate.sh

# --- ゲート -----------------------------------------------------------------

contracts: ## 契約の後方互換ゲート（OpenAPI + クロスコンテキストのメッセージスキーマ）
	@bash contracts/check-openapi-compat.sh
	@bash contracts/events/check-compat.sh

vuln: ## 依存関係の既知脆弱性をスキャンする（govulncheck）
	@set -e; for m in $(MODULES); do \
		echo "== govulncheck: $$m"; \
		(cd $$m && govulncheck ./...) || exit 1; \
	done

ci: generate-check fmt-check vet lint build test-race cover ## CI の ci ジョブと同じ検査一式をローカルで再現する
	@echo "ci: OK"

# --- 動かす -----------------------------------------------------------------

dev: ## Docker 不要の開発ハーネスを走らせる（両コンテキストを 1 プロセスで結線）
	@go run ./cmd/dev

up: ## docker compose で分散構成を起動する（DB + init + 2 サービス）
	@set -a && . ./tools/versions.env && set +a && docker compose up -d --build

down: ## docker compose を停止し、ボリュームも削除する
	@docker compose down -v

test-integration: ## PostgreSQL を起動して統合テスト（build tag integration）を実行する
	@set -a && . ./tools/versions.env && set +a && \
		docker compose -f docker-compose.yml -f docker-compose.test.yml up -d --build db migrate
	@set -e; for m in $(ITEST_MODULES); do \
		echo "== integration: $$m"; \
		(cd $$m && DATABASE_URL='$(DATABASE_URL)' go test -tags=integration ./...) || exit 1; \
	done

.PHONY: help generate generate-check fmt fmt-check vet lint build test test-race \
        cover contracts vuln ci dev up down test-integration
