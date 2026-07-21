# go-ddd-template

Go でドメイン駆動設計（DDD）とヘキサゴナルアーキテクチャ（ports and adapters）を
実践するためのテンプレートリポジトリです。**コントラクトファースト + コード生成** を軸に、
「契約（OpenAPI / SQL）から型安全なコードを生成し、ドメインとアプリケーションは手書きで
守る」構成を示します。

このリポジトリは DDD の**パターンそのもの**を製品として提供します。題材のドメインは
あえて小さく保っています（在庫の補充と照会だけ）。狙いはドメインの深さではなく、
層の分離・境界・生成物との付き合い方を、そのまま自分のプロジェクトの出発点として
コピーできる形で示すことです。

## このスライスで実装済みのもの

ひとつの境界づけられたコンテキスト **Inventory（在庫）** を、最小の縦切り
（walking skeleton）で通してあります。

- **在庫の補充** `POST /stock/{sku}/replenish`
- **在庫の照会** `GET /stock/{sku}`

これにより「OpenAPI → 生成サーバ → アプリケーションのユースケース → 純粋なドメイン →
リポジトリ（インメモリ実装と PostgreSQL 実装の両方）」という背骨が端から端まで
組み上がることを、テストで保証しています。

> 引当・予約、期限切れの掃除処理、アウトボックス、2 つ目のコンテキスト、
> コンテキスト間の腐敗防止層（anti-corruption layer）などは、この最小スライスの
> 対象外です（後続で追加していきます）。

## アーキテクチャ（4 つの層）

**なぜこの 4 分割か** — ヘキサゴナル（ports and adapters）の考え方で「外の世界」と
「中の核」を切り離すためです。アダプタは**方向で対称に 2 種類**に分けます。

- **adapter/inbound（入口）** — 外から中を「駆動する」側。HTTP リクエストのような
  外部からの呼び出しを受け取り、アプリケーションのユースケース呼び出しへ翻訳します。
- **adapter/outbound（出口）** — 中から外へ「書き出す」側。アプリケーションが定義した
  ポートを実装し、DB・メモリ・ログといった外部資源へアクセスします。
- **application** — ユースケースと**ポート（Go の interface）**を置く層。ポートは
  ここに定義し、実装は adapter/outbound に委ねます（依存性逆転）。
- **domain** — 純粋なドメイン。外側を一切知りません。

依存の向きは**常に内側**へ向きます。外側は内側を知ってよいが、内側は外側を知りません。
入口（inbound）と出口（outbound）は互いを直接知らず、両者の結線は合成ルート
（ファサード / cmd）だけで行います。

```
  外の世界                    ┌───────────────────────┐                   外の世界
 （HTTP）                     │  application          │       （PostgreSQL / メモリ / ログ）
    │                         │  ユースケース + ポート   │                     ▲
    ▼                         │  Replenisher          │                     │ ポートを実装
 [adapter/inbound]  ───呼ぶ──▶ │  StockViewer          │ ◀──ポートを実装── [adapter/outbound]
  入口（駆動側）               │  StockStore(port)     │                    出口（被駆動側）
  ogen サーバ +               │        │              │                    pgx+sqlc 実装 /
  薄いハンドラ +               │        ▼              │                    インメモリ実装 /
  RFC 9457 変換               │  [domain] 純粋なドメイン  │                    構造化ログ
                             │  StockItem / SKU /    │
                             │  Quantity / Event     │
                             └───────────────────────┘

依存の向き:  inbound ──▶ application ◀── outbound、  application ──▶ domain
```

- **domain（`internal/domain/inventory`）** — 純粋なドメイン層。`context.Context`・
  リポジトリ・永続化・IO・フレームワーク・アダプタのいずれにも依存しません。不変条件は
  集約自身が守ります。この純粋性は静的解析（depguard）でも機械的に強制しています。
- **application（`internal/application`）** — ユースケースとポート（`StockStore` など）。
  ドメインのオーケストレーションを担いますが、業務ルール自体はドメインに置きます。
  この層は**ポートを定義するだけ**でアダプタには依存しません。
- **adapter/outbound（`internal/adapter/outbound`）** — 出口（被駆動側）アダプタ。
  ポートの実装であり、インメモリ実装・pgx + sqlc による PostgreSQL 実装・構造化ログを
  含みます（`memory` / `postgres` / `logging`）。
- **adapter/inbound（`internal/adapter/inbound`）** — 入口（駆動側）アダプタ。ogen が
  生成したサーバ（`openapi`）を薄いハンドラ（ディレクトリ `http`、パッケージ `httpapi`）で
  実装し、HTTP とアプリケーション層を相互変換します。
- **公開ファサード（コンテキストルート `inventory.Module`）** — 外部はこの薄い
  ファサードだけに依存し、`internal/` には触れません。Go の internal パッケージ規則で
  境界がコンパイラに強制されます。

### 中心となる設計判断

- **明示的なトランザクション境界** — トランザクションを `context.Context` に隠して
  引き回しません。`shared/uow` の `UnitOfWork.Within` がトランザクションを所有し、
  そのトランザクションに束ねたリポジトリの束をコールバックへ渡します。書き込みは
  必ず作業単位の内側でしか行えません。`context.Context` にはリクエストスコープの
  付帯情報（相関 ID）だけを載せます。
- **楽観的排他制御（optimistic concurrency control）** — 集約はバージョン番号を
  「保持」するだけで、比較（compare-and-set）はリポジトリが担います。版が食い違えば
  `ErrConcurrencyConflict` を返し、`uow.Run` が指数バックオフで再試行します。
- **コントラクトファースト + コード生成** — OpenAPI から ogen がサーバを、SQL から
  sqlc が型安全なアクセサを生成します。**生成物はコミットし、手で編集しません**。
  契約を変えたいときは元の YAML / SQL を編集して再生成します。
- **RFC 9457（Problem Details）** — エラーは `application/problem+json` として返します。
  ドメインのセンチネルエラーを HTTP ステータスへ翻訳します（見つからない → 404、
  入力検証 → 422、排他衝突 → 409）。

## ディレクトリ構成

```
.
├── go.work                     … 複数モジュールのワークスペース
├── contracts/                  … コード生成の入力（契約 = 真実の源）
│   └── inventory/openapi.yaml   … 在庫コンテキストの公開 OpenAPI
├── shared/                     … ドメイン非依存の共有モジュール
│   ├── uow/                     … 作業単位（明示的トランザクション + 楽観的再試行）
│   ├── id/                      … ID 生成（crypto/rand）
│   └── correlation/             … 相関 ID の context ヘルパー
└── contexts/
    └── inventory/              … 「在庫」境界づけられたコンテキスト（1 モジュール）
        ├── inventory.go         … 公開ファサード（Module, New, HTTPHandler）
        ├── cmd/inventory/       … サービスの合成ルート（main）
        ├── db/                  … schema.sql / queries.sql（sqlc の入力）
        ├── sqlc.yaml            … sqlc の設定
        └── internal/
            ├── domain/          … 純粋なドメイン
            ├── application/     … ユースケース / ポート
            └── adapter/         … アダプタ（入口 / 出口で対称）
                ├── inbound/     … 入口（駆動側）
                │   ├── http/     … 薄いハンドラ + RFC 9457 変換（パッケージ httpapi）
                │   └── openapi/  … ogen 生成サーバ
                └── outbound/    … 出口（被駆動側）
                    ├── memory/   … インメモリ実装
                    ├── postgres/ … pgx + sqlc 実装（sqlcgen/ を含む）
                    └── logging/  … 構造化ログ
```

## 動かし方

### 1) Docker なしで動かす（インメモリ / テスト）

DB を用意せずに、ドメインとアプリケーションの縦切りを検証できます。インメモリ実装は
モックではなく、擬似トランザクションと楽観的排他制御を備えた**本物のアダプタ**です。

```sh
# 全モジュールのテスト（相関 ID・作業単位・排他衝突・RFC 9457 まで通しで検証）
cd shared && go test ./...
cd ../contexts/inventory && go test ./...
```

### 2) docker compose で動かす（PostgreSQL）

`docker compose up` で、PostgreSQL の起動 → スキーマ適用 → 在庫サービス起動までを
手作業なしで行います。

```sh
docker compose up --build
```

起動後、別ターミナルから：

```sh
# 在庫を補充する（未登録 SKU は新規作成される）
curl -X POST localhost:8080/stock/WIDGET-001/replenish \
  -H 'Content-Type: application/json' -d '{"quantity":10}'

# 在庫を照会する
curl localhost:8080/stock/WIDGET-001

# 存在しない SKU（RFC 9457 の problem+json が 404 で返る）
curl -i localhost:8080/stock/UNKNOWN
```

> `docker-compose.yml` に書かれた認証情報はすべて**デモ専用**です。本番では
> 使わないでください。秘密情報はイメージに焼き込まず、実行時の環境変数で渡します。

## コード生成

生成にはコマンドラインツールが必要です。

```sh
go install github.com/ogen-go/ogen/cmd/ogen@latest
go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest

# 契約 / SQL を編集したら再生成する（生成物はコミットする）
cd contexts/inventory && go generate ./...
```

CI では再生成後に差分が出ないこと（冪等性）を検証します。

## 前提ツール

- Go（最新安定版）
- Docker / Docker Compose（PostgreSQL で動かす場合）
- ogen, sqlc（コード生成する場合）
- golangci-lint, goimports（静的解析・整形）

## ライセンス

MIT License（`LICENSE` を参照）。
