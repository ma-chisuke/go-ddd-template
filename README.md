# go-ddd-template

Go でドメイン駆動設計（DDD）とヘキサゴナルアーキテクチャ（ports and adapters）を
実践するためのテンプレートリポジトリです。**コントラクトファースト + コード生成** を軸に、
「契約（OpenAPI / SQL）から型安全なコードを生成し、ドメインとアプリケーションは手書きで
守る」構成を示します。

このリポジトリは DDD の**パターンそのもの**を製品として提供します。題材のドメインは
あえて小さく保っています（在庫の補充と照会だけ）。狙いはドメインの深さではなく、
層の分離・境界・生成物との付き合い方を、そのまま自分のプロジェクトの出発点として
コピーできる形で示すことです。

## 実装済みのもの

2 つの境界づけられたコンテキスト **Inventory（在庫）** と **Ordering（注文）** を、DDD の
主要パターンでひととおり実装し、両者を **分散サービス** として結ぶ **腐敗防止層（ACL）**と
**クロスコンテキストのイベント／コマンド契約** まで通しています。まず在庫側で最小の縦切り
（walking skeleton）から予約のライフサイクルと汎用機構を整え、そのうえに注文コンテキストと
コンテキスト間の seam（縫い目）を載せています。

このテンプレートの中心的な差別化点は、この **seam の実装** です。注文コンテキストは在庫の
ドメイン型を一切知らず、生成クライアント（在庫の内部 OpenAPI から生成）越しに HTTP でのみ
到達し、在庫のエラーは注文側自身の番兵へ翻訳されます。

**公開 API（補充・照会）**

- **在庫の補充** `POST /stock/{sku}/replenish`
- **在庫の照会** `GET /stock/{sku}`

**内部 API（サービス間連携）** — 公開 API とは別のサーバ／ポートで動く

- **予約** `POST /reservations`（マルチ SKU・全か無か）
- **確定** `POST /reservations/{ref}/confirm`（二相予約の第 2 相）
- **解放** `POST /reservations/{ref}/release`
- **メッセージ取り込み** `POST /events`（`outbox.Router` へ委譲）

**ドメインの要点**

- **二相予約**（reserve → confirm）と、期限切れ仮予約を掃除する **Reaper**
  （confirmed は決して解放しない）。予約・確定・解放はいずれも **冪等**。
- **導出値としての `reserved`**（有効な予約の合計）と、非負の `available`。
- **マルチ SKU 予約の全か無か**（`ReservationService`）。同一予約参照が複数の在庫項目に
  跨るため、確定・解放は対象の全項目を **1 つの作業単位で原子的に** 遷移させる。

**共有機構（`shared/`）**

- **トランザクショナルアウトボックス**（`shared/outbox`）: 集約書き込みと同一トランザクションで
  メッセージを積み（Enqueue）、送信中継（`Runner`）が at-least-once で送出、受信側は
  `Router` が種別ごとに `Consumer` へ振り分ける（未登録種別は `ErrNoRoute`）。
- **プロセス内イベント配信**（`shared/event`）と、決定的テスト用の擬似時計（`shared/testutil`）。

これらは「OpenAPI / SQL → 生成コード → アプリケーションのユースケース → 純粋なドメイン →
リポジトリ（インメモリ実装と PostgreSQL 実装の両方）」という構成で、端から端まで
テストで保証しています。

**公開 API（作成・照会・取消）— Ordering（注文）**

- **注文の作成** `POST /orders`（在庫を同期予約できたときのみ Confirmed で確定）
- **注文の照会** `GET /orders/{id}`
- **注文の取消** `POST /orders/{id}/cancel`（在庫の解放は非同期）

**コンテキスト間の seam（この段階の主眼）**

- **二相予約（分散）** — 作成は、注文 ID から決定的に導出した予約参照で在庫を **同期予約**
  （腐敗防止層 `aclhttp` → 生成クライアント `clients/inventory` → 在庫の内部 API）し、
  成功したときのみ、同一の作業単位で注文を Confirmed 保存し `ConfirmReservation` コマンドを
  アウトボックスへ積みます。確定はアウトボックス経由で at-least-once に在庫へ届きます。
- **腐敗防止層（ACL）とエラー翻訳** — 注文は在庫の Go パッケージを import せず、翻訳済み
  DTO（`port.ReserveLine`）だけを渡します。在庫不足（409）は注文側の `ErrReservationRejected`
  （→ HTTP 409）、不達・タイムアウト・5xx は `ErrReservationUnavailable`（→ HTTP 503）へ
  翻訳し、在庫側の番兵はそのまま漏らしません。
- **非同期の在庫解放** — 取消は `OrderCancelled` を（保存と同一トランザクションで）アウトボックスへ
  積み、在庫側が購読して非同期に解放します。下流から上流への同期呼び出しはしません。
- **契約の正本** — クロスコンテキストのメッセージ契約は `contracts/events/` に、在庫の内部 API
  契約は `contracts/inventory/internal.openapi.yaml` に集中管理します。サーバはコンテキストごとに、
  クライアントは共有モジュール `clients/inventory` に生成します（生成物はコミット）。
- **遅延／消失した確定の整合** — 分散トポロジでは、注文側に照会用のコード分岐や `Failed` 状態は
  持たせません。整合は運用レベルで、両サービスのログを共有 `trace_id`（W3C traceparent）で
  相関して行います（在庫側 `OnConfirmReservation` は該当予約が無ければ良性の警告ログを残します）。

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
├── contracts/                  … コード生成の入力（契約 = 真実の源。集中管理）
│   ├── inventory/
│   │   ├── openapi.yaml          … 公開 OpenAPI（補充・照会）
│   │   ├── internal.openapi.yaml … 内部 OpenAPI（予約・確定・解放・取り込み = ACL サーフェス）
│   │   └── client.ogen.yaml      … 上記内部 API から「クライアントのみ」を生成する設定
│   ├── ordering/
│   │   └── openapi.yaml          … 公開 OpenAPI（作成・照会・取消）
│   └── events/                  … クロスコンテキストのメッセージ契約（JSON スキーマ）
│       ├── confirm_reservation.schema.json … 予約確定コマンド
│       └── order_cancelled.schema.json     … 注文取消イベント
├── clients/                    … 共有の生成クライアント（消費側が import・コミット・手編集しない）
│   └── inventory/              … 在庫の内部 API から生成した Go クライアント（invclient）
├── shared/                     … ドメイン非依存の共有モジュール（uow / event / outbox / id / correlation / testutil）
└── contexts/
    ├── inventory/              … 「在庫」境界づけられたコンテキスト（1 モジュール）
    │   ├── inventory.go         … 公開ファサード（Module, New, HTTPHandler, InternalHTTPHandler, StartWorkers）
    │   ├── cmd/inventory/       … サービスの合成ルート（main）
    │   ├── db/ · sqlc.yaml      … schema.sql / queries.sql（sqlc の入力）
    │   └── internal/{domain, application, adapter/{inbound, outbound}}
    └── ordering/               … 「注文」境界づけられたコンテキスト（1 モジュール）
        ├── ordering.go          … 公開ファサード（Module, New, HTTPHandler, StartWorkers）
        ├── cmd/ordering/        … サービスの合成ルート（main。ACL / イベント送出クライアントを結線）
        ├── port/                … 公開の翻訳済み DTO（ReserveLine）
        ├── db/ · sqlc.yaml      … schema.sql / queries.sql（orders / order_lines / outbox）
        └── internal/
            ├── domain/order/    … 純粋なドメイン（Order / OrderLine / VO / イベント）
            ├── application/     … ユースケース（PlaceOrder / GetOrder / CancelOrder）/ ポート / ACL ポート
            └── adapter/
                ├── inbound/{http, openapi}     … 公開 API の薄いハンドラ + ogen 生成サーバ
                └── outbound/
                    ├── memory/    … インメモリ実装（注文 + アウトボックス）
                    ├── postgres/  … pgx + sqlc 実装（sqlcgen/ を含む）
                    ├── aclhttp/   … 腐敗防止層（生成クライアントで StockReserver を実装 + trace 伝播）
                    ├── eventhttp/ … アウトボックス送信トランスポート（在庫の /events へ HTTP push）
                    └── logging/   … 構造化ログ + 開発用パブリッシャ
```

> **境界規則（depguard で強制）**: 注文コンテキストは `contexts/inventory` の Go パッケージを
> 決して import しません。在庫へは `clients/inventory` を介して HTTP 越しにのみ到達します。
> 各コンテキストのドメインは純粋（永続化・IO・生成クライアントに非依存）で、値オブジェクトは
> コンテキストごとに独立所有します（例: 注文の `Quantity` は n ≥ 1、在庫の `Quantity` は n ≥ 0）。

## 動かし方

### 1) Docker なしで動かす（インメモリ / テスト）

DB を用意せずに、ドメインとアプリケーションの縦切りを検証できます。インメモリ実装は
モックではなく、擬似トランザクションと楽観的排他制御を備えた**本物のアダプタ**です。

```sh
# 全モジュールのテスト（相関 ID・作業単位・排他衝突・RFC 9457・seam の翻訳まで通しで検証）
cd shared && go test ./...
cd ../contexts/inventory && go test ./...
cd ../ordering && go test ./...
```

> 注文コンテキストのテストには、腐敗防止層（`aclhttp`）が在庫の内部 API 契約どおりに要求を
> 組み立て、在庫のエラー（409 / 5xx / タイムアウト）を注文側の番兵へ翻訳し、`trace_id` が
> 作成 → 予約でサービスを跨いで伝播することの検証（httptest スタブ）が含まれます。

### 2) docker compose で動かす（PostgreSQL）

`docker compose up` で、PostgreSQL の起動 → 両コンテキストのスキーマ適用 →
在庫サービス・注文サービスの起動までを手作業なしで行います（分散構成: サービスごとに独立
コンテナ、1 つの物理 DB をスキーマで論理分割）。

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

内部 API（別ポート 8081）で予約のライフサイクルを試せます。

```sh
# 予約（マルチ SKU・全か無か）
curl -X POST localhost:8081/reservations \
  -H 'Content-Type: application/json' \
  -d '{"ref":"ORDER-1","lines":[{"sku":"WIDGET-001","quantity":3}]}'

# 確定（pending → confirmed）
curl -X POST localhost:8081/reservations/ORDER-1/confirm

# 解放（在庫を戻す）
curl -X POST localhost:8081/reservations/ORDER-1/release

# 照会すると reserved が反映されている（公開 API 側）
curl localhost:8080/stock/WIDGET-001
```

注文サービス（ポート 8082）で、seam を跨ぐ二相予約を端から端まで試せます。

```sh
# まず在庫を補充しておく
curl -X POST localhost:8080/stock/WIDGET-001/replenish \
  -H 'Content-Type: application/json' -d '{"quantity":10}'

# 注文を作成する（在庫を同期予約 → 成功で Confirmed。ConfirmReservation が在庫へ届く）
curl -X POST localhost:8082/orders \
  -H 'Content-Type: application/json' \
  -d '{"customerId":"CUST-1","lines":[{"sku":"WIDGET-001","quantity":3,"unitPrice":{"amount":1200,"currency":"JPY"}}]}'

# 注文を照会する（作成レスポンスの id を使う）
curl localhost:8082/orders/<ORDER_ID>

# 注文を取り消す（OrderCancelled を発行 → 在庫側が非同期に解放）
curl -X POST localhost:8082/orders/<ORDER_ID>/cancel

# 在庫が不足していれば作成は 409（problem+json）、在庫サービスが不達なら 503 を返す
```

> `docker-compose.yml` に書かれた認証情報はすべて**デモ専用**です。本番では
> 使わないでください。秘密情報はイメージに焼き込まず、実行時の環境変数で渡します。

## コード生成

生成にはコマンドラインツールが必要です。

```sh
go install github.com/ogen-go/ogen/cmd/ogen@latest
go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest

# 契約 / SQL を編集したら各モジュールで再生成する（生成物はコミットする）
cd clients/inventory && go generate ./...   # 在庫内部 API → 共有クライアント（invclient）
cd ../../contexts/inventory && go generate ./...
cd ../ordering && go generate ./...
```

サーバはコンテキストごとに、クライアントは共有の `clients/inventory` に、同じ内部 OpenAPI から
鏡像の生成設定（サーバ側は `paths/client` を無効化、クライアント側は `paths/server` を無効化）で
生成します。CI では再生成後に差分が出ないこと（冪等性）を検証します。

## 前提ツール

- Go（最新安定版）
- Docker / Docker Compose（PostgreSQL で動かす場合）
- ogen, sqlc（コード生成する場合）
- golangci-lint, goimports（静的解析・整形）

## ライセンス

MIT License（`LICENSE` を参照）。
