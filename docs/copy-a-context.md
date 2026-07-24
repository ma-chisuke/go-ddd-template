# 1 つのコンテキストをコピーして出発点にする

このテンプレートの狙いは、**1 つの境界づけられたコンテキストをまるごと切り出して**、自分の
プロジェクトの出発点にできることです。各コンテキストは独立した Go モジュールで、`internal/` に
4 層（domain / application / adapter{inbound,outbound}）を隠し、薄い公開ファサードだけを公開します。

ここでは在庫（Inventory）だけを取り出す例で手順を示します（注文だけを取り出す場合も対称です）。

## 何をコピーするか

1. **コンテキスト本体** — `contexts/inventory/`（モジュールまるごと。`inventory.go` ファサード、
   `cmd/inventory/`、`port/`、`db/`、`internal/**`、`go.mod`、`sqlc.yaml`、`generate.go`）。
2. **そのコンテキストの契約** — `contracts/inventory/`（`openapi.yaml`、内部 API を使うなら
   `internal.openapi.yaml` と ogen 設定、`*.baseline.*`）。
3. **共有モジュール** — `shared/`（`uow` / `event` / `outbox` / `id` / `correlation` / `testutil`）。
   ドメイン非依存の機構で、どのコンテキストも依存します。
4. **消費する場合のみ**: 別コンテキストの内部 API を呼ぶなら、その生成クライアント
   （`clients/<peer>/`）と、送出するメッセージ契約（`contracts/events/`）。在庫だけを取り出す
   場合、在庫は他コンテキストを呼ばないので `clients/` は不要です。

> **契約は中央レジストリを優先**します。厳密な「1 ディレクトリ完全自己完結」より、`contracts/` に
> 契約を集中管理する構成を採っています。コピー時は、そのコンテキストが**使う契約**（自分の
> OpenAPI と、消費するなら相手のイベント契約・生成クライアント）だけを持っていきます。

## 手順（チェックリスト）

- [ ] 上記 1〜3（消費するなら 4 も）を新しいリポジトリへコピーする。
- [ ] `go.work` の `use` から、連れて行かないモジュール（例: `./contexts/ordering`、
      使わない `./clients/*`、`./cmd/dev`）の行を削除する。残すのは
      `./contexts/inventory`・`./shared`（+ 消費するなら `./clients/<peer>`）。
- [ ] 各 `go.mod` の `module` パス（`github.com/example/go-ddd-template/...`）を自分の
      モジュールパスへ置換する。`replace` の相対パス（`../../shared` など）が新レイアウトで
      解決することを確認する。
- [ ] 連れて行かないコンテキストへの参照が残っていないか確認する。在庫だけなら、在庫は
      もともと他コンテキストを import しないので追加作業は不要（`clients/` も不要）。
- [ ] `db/` の宣言的 DB 資産（`schema.sql` / `roles.sql` / `seed.sql` / `fixtures.sql` /
      `sqldef.yml`）を確認し、不要な参照データ/フィクスチャを整理する。
- [ ] `docker-compose.yml` / `deploy/` から、連れて行かないサービスと、その DB マウント
      （`- ./contexts/ordering/db:/db/ordering:ro` など）・`depends_on`・ポートを削除する。
      1 コンテキストだけなら `inventory-service` と `migrate`・`db` を残す。
- [ ] `.github/workflows/ci.yml` と `contracts/check-openapi-compat.sh` の対象一覧から、
      連れて行かないモジュール/契約を外す。
- [ ] README / AGENTS / CONVENTIONS を自分のプロジェクトに合わせて調整する。

## コピー後の検証

```sh
# 各モジュールでビルド・静的解析・テスト
go build ./... && go vet ./... && golangci-lint run ./... && go test ./...

# 生成物の冪等性（契約/SQL を変えたら再生成してコミット）
go generate ./...

# 分散構成の起動（DB スキーマ適用 → サービス起動）
# migrate の psqldef 版を渡すため tools/versions.env を export してから compose を呼ぶ。
set -a && . ./tools/versions.env && set +a && docker compose up --build
```

新しいコンテキストの足し方は [add-a-use-case.md](./add-a-use-case.md) と、コンテキスト間の
つなぎ方は [context-map.md](./context-map.md) を参照してください。
