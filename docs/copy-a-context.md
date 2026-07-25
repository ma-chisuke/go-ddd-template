# 1 つのコンテキストをコピーして出発点にする

このテンプレートの狙いは、**1 つの境界づけられたコンテキストをまるごと切り出して**、自分の
プロジェクトの出発点にできることです。各コンテキストは独立した Go モジュールで、`internal/` に
4 層（domain / application / adapter{inbound,outbound}）を隠し、薄い公開ファサードだけを公開します。

ここでは在庫（Inventory）だけを取り出す例で手順を示します（注文だけを取り出す場合も対称です）。

## 何をコピーするか

1. **コンテキスト本体** — `contexts/inventory/`（モジュールまるごと。`inventory.go` ファサード、
   `cmd/inventory/`、`port/`、`db/`、`internal/**`、`go.mod`、`sqlc.yaml`、`generate.go`、
   そして **`GLOSSARY.md`**）。用語集は境界が所有するので、コンテキストと一緒に付いてきます
   — ユビキタス言語はこの境界の内側でだけ通用するからです（境界を跨いで同名の語の対比は
   [glossary.md](./glossary.md) 側に残り、切り出し先では不要になります）。
2. **そのコンテキストの契約** — `contracts/inventory/`（`openapi.yaml`、内部 API を使うなら
   `internal.openapi.yaml` と ogen 設定、`*.baseline.*`）。
3. **共有モジュール** — `shared/`（`uow` / `event` / `serve` / `outbox` / `id` / `correlation` /
   `logging` / `problem` / `worker` / `testutil`）。ドメイン非依存の機構で、どのコンテキストも
   依存します。**`shared/` はまるごと持ち出す**のが最も手数の少ない方法です（切り出したコンテキストが
   使わないパッケージが混じっていても、Go のビルドやバイナリサイズには影響しません）。

   > **明示的なトレードオフ**: 共通化を進めるほど、この持ち出しに同伴する `shared/` の面積は
   > 増えます。これは設計上の後退ではなく意図した選択です — 重複を各コンテキストに残せば同伴
   > 面積は減りますが、「間違えやすい機構（トランザクションの再試行、アウトボックスの配送順序、
   > HTTP サーバのライフサイクル）が 1 箇所にある」という利点を失います。逆に、生成型を包む
   > アダプタコードのように**共有すると読みにくくなる重複は意図的に各コンテキストへ残して**
   > います（CONVENTIONS.md「共通化しない重複」）。
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
- [ ] module パスを自分のものへ置換する。**丸ごとコピーした直後なら
      `./scripts/rename-module.sh github.com/you/your-repo` が一括で行う**
      （`.golangci.yml` の depguard 指定・`contracts/**` のベースライン・RFC 9457 の
      problem type URI の名前空間まで含む。`--dry-run` で対象を確認できる）。切り出しでは
      ファイル構成が変わるので、各 `go.mod` の `module` パスと `replace` の相対パス
      （`../../shared` など）が新レイアウトで解決することを確認する。
- [ ] 連れて行かないコンテキストへの参照が残っていないか確認する。在庫だけなら、在庫は
      もともと他コンテキストを import しないので追加作業は不要（`clients/` も不要）。
- [ ] `db/` の宣言的 DB 資産（`schema.sql` / `roles.sql` / `seed.sql` / `fixtures.sql` /
      `sqldef.yml`）を確認し、不要な参照データ/フィクスチャを整理する。
- [ ] `docker-compose.yml` / `deploy/` から、連れて行かないサービスと、その DB マウント
      （`- ./contexts/ordering/db:/db/ordering:ro` など）・`depends_on`・ポートを削除する。
      1 コンテキストだけなら `inventory-service` と `migrate`・`db` を残す。
- [ ] `Makefile` の `MODULES` / `GEN_MODULES` / `ITEST_MODULES` / `FMT_DIRS` から、連れて行かない
      モジュールを外す。**直すのはこの変数だけ**で、ターゲットと `.github/workflows/ci.yml` は
      触らなくてよい（CI は Makefile のターゲットを呼ぶだけなので）。
- [ ] `contracts/check-openapi-compat.sh` の対象一覧から、連れて行かない契約を外す。
- [ ] README / AGENTS / CONVENTIONS を自分のプロジェクトに合わせて調整する。
      切り出したコンテキストの `GLOSSARY.md` はそのまま使える（境界が変わっていないため）。

## コピー後の検証

```sh
# ビルド・静的解析・テスト（全モジュール）と生成物の冪等性を一度に
make ci

# 分散構成の起動（DB スキーマ適用 → サービス起動。停止と後片付けは make down）
make up
```

新しいコンテキストの足し方は [add-a-use-case.md](./add-a-use-case.md) と、コンテキスト間の
つなぎ方は [context-map.md](./context-map.md) を参照してください。
