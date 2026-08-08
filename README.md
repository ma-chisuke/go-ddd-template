# go-ddd-template

Go でドメイン駆動設計（DDD）とヘキサゴナルアーキテクチャ（ports and adapters）を
実践するためのテンプレートリポジトリです。**コントラクトファースト + コード生成** を軸に、
「契約（OpenAPI / SQL）から型安全なコードを生成し、ドメインとアプリケーションは手書きで
守る」構成を示します。

このリポジトリは DDD の**パターンそのもの**を製品として提供します。題材のドメインは
あえて小さく保っています（在庫の補充と照会だけ）。狙いはドメインの深さではなく、
層の分離・境界・生成物との付き合い方を、そのまま自分のプロジェクトの出発点として
コピーできる形で示すことです。

## Getting Started（5 分）

前提は Go 1.26 以上と `make` だけです（Docker はまだ要りません）。

```sh
# 1) 自分の module path にする（リポジトリ全体を一括置換。--dry-run で対象と件数を確認できる）
./scripts/rename-module.sh github.com/you/your-repo

# 2) Docker 無しで端から端まで動かす（両コンテキストを 1 プロセスで結線して一気に実行）
make dev

# 3) 全モジュールのテストを回す（CI と同じ検査一式は make ci）
make test
```

1 の置換は Go の module path だけでなく、**RFC 9457 の problem type URI の名前空間**
（エラー応答の `type` に載る公開契約の値）も同時に書き換えます。`.golangci.yml` の depguard
設定や `contracts/**` のベースラインなど、手作業では取りこぼしやすい箇所まで含みます。

打てるコマンドの一覧は引数なしの `make` で出ます（すべての操作の単一入口です）。

**4) 次に読む** — [docs/why-these-boundaries.md](docs/why-these-boundaries.md)（なぜこの境界か）
→ [docs/ddd-patterns.md](docs/ddd-patterns.md)（パターンがどのファイルにあるか）
→ [docs/glossary.md](docs/glossary.md)（境界ごとの語彙と、境界を跨いで同名の語）。

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
- **配送キューと恒久イベントログの分離**: `outbox` 表は**一時的な配送キュー**で、送出に
  成功した行は削除されます（delete-after-publish）。つまり `outbox` に残るのは常に未送信分
  だけです。「何を発行したか」の**恒久的な記録**は `events` 表（追記専用のイベントログ）が
  担い、`Enqueue` が outbox 行と events 行を**同一トランザクションで両方**書きます
  （集約の保存も含めて原子的にコミットされ、片方だけ残ることがありません）。配送は
  `events` を参照しません。
  なお `events` は保持ジョブを持たず**無制限に増え続ける**ため、本番採用時はアーカイブ・
  パーティション・保持ジョブのいずれかを足してください（テンプレートは単純さを優先して
  意図的に持ちません）。
- **プロセス内イベント配信**（`shared/event`）: 型なしコア `InProcess` と、コンテキストの
  ドメインイベント型で使う generic な型付きファサード `Typed[E]`。ドメイン層は `shared/event` を
  import せず、`shared/event` もコンテキストを import しない — 双方向に import が無いまま、
  Go の構造的型付けだけで型が噛み合う。時刻の供給は `shared/clock`（実時計 `clock.System` と、
  決定的テスト用に手で進める `clock.NewManual`）。
- **HTTP サーバ群のランナー**（`shared/serve`）: 起動・停止待ち・全サーバのグレースフル
  シャットダウンをサーバ本数に依存せず担う（注文は公開 1 本、在庫は公開 + 内部の 2 本）。
  シグナル受信・資源解放・ヘルスチェックは意図的に持たず、各 `main.go` に残す。

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
  契約は `contracts/api/inventory/internal.openapi.yaml` に集中管理します。サーバはコンテキストごとに、
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
  この層の `ports.go` にまとめて定義し、実装は adapter/outbound に委ねます（依存性逆転）。
  `ports.go` を開けば、そのコンテキストが外部に要求している依存が一望できます。
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

- **domain（`internal/domain`）** — 純粋なドメイン層。`context.Context`・
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
- **RFC 9457（Problem Details）** — エラーは**すべての経路**で
  `application/problem+json` として返します。デコード失敗（400）・未定義パス（404）・
  メソッド不許可（405）・サポート外 Content-Type（415）も、ドメインのセンチネルエラーの
  翻訳（404 / 409 / 422 / 503）も同じ形式です。`type` は種別ごとの安定した URI で、
  同じ status でも原因が違えば別の URI を与えます（経路が無い `not-found` と、対象が無い
  `resource-not-found`）。違反したフィールドは拡張メンバー `invalid-params` で
  機械可読に伝えます（`lines[0].unitPrice.amount` のようなパスと固定語彙の `code`）。
  応答本文に ogen / Go 由来の文言や受信値は決して載せません。詳細は `CONVENTIONS.md`。

## 読み進め方（段階的読解パス）

このリポジトリは、まず 2 つの層で DDD のコアを理解し、そのうえで本番形状のインフラを
読む、という順序で読み進められるよう構成しています。「最小」なのは**ドメインの題材**で
あって、リポジトリ全体ではありません。インフラや横断的関心事が本番形状で厚いのは意図した
特徴であり、この読解パスがその順序を制御します。

0. **なぜこの境界か** — [docs/why-these-boundaries.md](docs/why-these-boundaries.md)。
   在庫と受注の 2 つに割った導出過程（語の衝突・トランザクション境界・変更理由の違い）と、
   却下した代替案。コードを読む前に、境界がどう決まったかを掴みます。
1. **ドメイン** — `contexts/<ctx>/internal/domain/`。純粋なドメイン（集約・値オブジェクト・
   不変条件・ドメインイベント・ドメインサービス）。外側を一切知りません。語彙は
   [contexts/inventory/GLOSSARY.md](contexts/inventory/GLOSSARY.md) /
   [contexts/ordering/GLOSSARY.md](contexts/ordering/GLOSSARY.md)、どのパターンがどのファイルかは
   [docs/ddd-patterns.md](docs/ddd-patterns.md) § 戦術パターン。
2. **アプリケーション（ユースケース）** — `contexts/<ctx>/internal/application/`。
   「読み込み → ドメイン操作 → 保存」のオーケストレーションと、依存を逆転させるポート。
   リポジトリ／作業単位の位置は [docs/ddd-patterns.md](docs/ddd-patterns.md)。
3. **戦略的シーム（ACL + イベント）** — コンテキスト間の縫い目。配置時の同期予約（腐敗防止層
   `aclhttp`）、確定コマンドと取消イベントのメッセージ契約（`contracts/events/`）。
   全体像は [docs/context-map.md](docs/context-map.md)、パターンとしての位置づけは
   [docs/ddd-patterns.md](docs/ddd-patterns.md) § 戦略パターン。
4. **インフラの堅牢化** — 作業単位（`shared/uow`）・トランザクショナルアウトボックス
   （`shared/outbox`）・楽観的排他制御・宣言的 DB（`contexts/<ctx>/db/`）・契約ガバナンス
   （`contracts/` + CI ゲート）。ここは本番形状で厚く、後から読みます。索引は
   [docs/ddd-patterns.md](docs/ddd-patterns.md) § 支援機構。

### ドキュメント

- [CONVENTIONS.md](CONVENTIONS.md) — Go / SQL / DDD の規約（命名・層分離・`UnitOfWork[R]` など）。
- [docs/testing-conventions.md](docs/testing-conventions.md) — テストの規約（テスト関数名の 2 形、
  サブテスト名の 8 語語彙、`t.Parallel()` の適用範囲、テストの日本語コメント）。
  どちらの文書も `make lint` と `make conventions` が機械的に強制します。
- [AGENTS.md](AGENTS.md) / [CLAUDE.md](CLAUDE.md) — AI エージェント向けガイド（機械可読契約への案内・禁止事項）。
- [docs/why-these-boundaries.md](docs/why-these-boundaries.md) — なぜ在庫と受注の 2 つに割ったのか（導出過程と却下した代替案）。
- [docs/ddd-patterns.md](docs/ddd-patterns.md) — DDD パターン → このリポジトリでの実装位置の索引。
- [docs/glossary.md](docs/glossary.md) — 用語集の索引と、境界を跨いで同名の語の対比
  （定義本体は [contexts/inventory/GLOSSARY.md](contexts/inventory/GLOSSARY.md) と
  [contexts/ordering/GLOSSARY.md](contexts/ordering/GLOSSARY.md)）。
- [docs/context-map.md](docs/context-map.md) — seam の 3 フロー（同期予約 / 確定コマンド / 取消イベント）。
- [docs/copy-a-context.md](docs/copy-a-context.md) — 1 コンテキストを切り出して自分のプロジェクトの出発点にする手順。
- [docs/add-a-use-case.md](docs/add-a-use-case.md) — 新しいユースケースを足すレシピ。
- [clients/README.md](clients/README.md) — 生成クライアントがどの契約から来て、なぜ
  `contexts/` の外にいるのか（同じ契約からサーバとクライアントが別の場所へ生成される）。

## ディレクトリ構成

```
.
├── go.work                     … 複数モジュールのワークスペース
├── Makefile                    … すべてのコマンドの単一入口（モジュール一覧は MODULES 変数 1 箇所）
├── docker-compose.yml          … 分散サービスのローカル起動（DB + init + 2 サービス）
├── docker-compose.test.yml     … 統合テスト時のみ Postgres をホスト公開するオーバーレイ
├── deploy/                     … bring-up 用の使い捨て init コンテナ（psqldef + psql）
│   ├── migrate.Dockerfile        … スキーマ適用・ロール/seed 適用イメージ
│   └── apply.sh                  … 適用オーケストレーション（schema → roles → seed → fixtures）
├── docs/                       … 追加ドキュメント（why-these-boundaries / ddd-patterns / glossary /
│                                 context-map / copy-a-context / add-a-use-case）
├── scripts/
│   ├── coverage-gate.sh          … カバレッジゲート（domain + application >= 80%）
│   └── rename-module.sh          … module path と problem type URI の名前空間を一括置換
├── contracts/                  … コード生成の入力（契約 = 真実の源。集中管理）
│                                 第 1 階層は**契約の種別**、第 2 階層は**境界づけられた
│                                 コンテキスト**。直下の子は api/ と events/ の 2 つだけ
│   ├── api/                    … 種別: 同期 HTTP 契約（OpenAPI）
│   │   ├── check-compat.sh       … 後方互換ゲート（oasdiff）+ 宣言と実体の突き合わせ
│   │   ├── protected.txt         … 守ると宣言した契約の一覧（ゲートが実体と突き合わせる）
│   │   ├── inventory/
│   │   │   ├── openapi.yaml          … 公開 OpenAPI（補充・照会）
│   │   │   ├── internal.openapi.yaml … 内部 OpenAPI（予約・確定・解放・取り込み = ACL サーフェス）
│   │   │   ├── *.baseline.yaml       … リリース済み契約のベースライン（互換ゲートの基準）
│   │   │   ├── openapi.ogen.yaml     … 公開 API から「サーバのみ」を生成する設定
│   │   │   ├── internal.ogen.yaml    … 内部 API から「サーバのみ」を生成する設定
│   │   │   └── client.ogen.yaml      … 上記内部 API から「クライアントのみ」を生成する設定
│   │   └── ordering/
│   │       ├── openapi.yaml          … 公開 OpenAPI（作成・照会・取消）
│   │       ├── openapi.baseline.yaml … ベースライン（互換ゲートの基準）
│   │       └── openapi.ogen.yaml     … 「サーバのみ」を生成する設定
│   └── events/                 … 種別: 非同期メッセージ契約（JSON スキーマ）
│       ├── check-compat.sh       … 後方互換ゲート + 配置の検査（発行元と type / $id の一致）
│       ├── ordering/            … 第 2 階層 = **発行元**コンテキスト
│       │   ├── confirm_reservation.schema.json … 予約確定コマンド
│       │   ├── order_cancelled.schema.json     … 注文取消イベント
│       │   └── *.baseline.schema.json          … ベースライン（互換ゲートの基準）
│       └── inventory/           … 在庫は現在メッセージを発行しない（意図された空。README のみ）
├── clients/                    … 共有の生成クライアント（消費側が import・コミット・手編集しない）
│   ├── README.md               … どの契約から生成され、なぜ contexts/ の外にいるのか
│   └── inventory/              … 在庫の内部 API から生成した Go クライアント（invclient）
├── cmd/dev/                    … Docker 不要の開発ハーネス（両コンテキストを 1 プロセスで結線）
├── shared/                     … ドメイン非依存の共有モジュール（uow / event / serve / outbox / id / correlation / problem / clock）
└── contexts/
    ├── inventory/              … 「在庫」境界づけられたコンテキスト（1 モジュール）
    │   ├── GLOSSARY.md          … この境界のユビキタス言語（コンテキストと一緒にコピーされる）
    │   ├── inventory.go         … 公開ファサード（Module, New, NewInMemory, HTTPHandler, InternalHTTPHandler,
    │   │                          Reserve/Confirm/Release/Deliver/Sweep のシーム, StartWorkers）
    │   ├── cmd/inventory/       … サービスの合成ルート（main）
    │   ├── port/                … 公開の翻訳済み DTO（SKUQty）
    │   ├── db/ · sqlc.yaml      … schema.sql / queries.sql（stock_items / stock_reservations / outbox / events）/ roles.sql / seed.sql / fixtures.sql / sqldef.yml
    │   └── internal/{domain, application, adapter/{inbound, outbound}}
    └── ordering/               … 「注文」境界づけられたコンテキスト（1 モジュール）
        ├── GLOSSARY.md          … この境界のユビキタス言語（コンテキストと一緒にコピーされる）
        ├── ordering.go          … 公開ファサード（Module, New, NewInMemory, HTTPHandler, StartWorkers）
        ├── cmd/ordering/        … サービスの合成ルート（main。ACL / イベント送出クライアントを結線）
        ├── port/                … 公開の翻訳済み DTO（ReserveLine）と ACL の番兵（ErrReservationRejected など）
        ├── db/ · sqlc.yaml      … schema.sql / queries.sql（orders / order_lines / outbox / events）/ roles.sql / seed.sql / fixtures.sql / sqldef.yml
        └── internal/
            ├── domain/          … 純粋なドメイン（Order / OrderLine / VO / イベント）
            ├── application/     … ユースケース（PlaceOrder / GetOrder / CancelOrder）/ ポート / ACL ポート
            └── adapter/
                ├── inbound/{http, openapi}     … 公開 API の薄いハンドラ + ogen 生成サーバ
                └── outbound/
                    ├── memory/    … インメモリ実装（注文 + アウトボックス + イベントログ）
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

2 つの実行モードがあります。**まず動かす**なら Docker 不要の `make dev`、
**分散構成を体験する**なら `make up`（docker compose）です。

> **タイミングに関する注意（誤解しないために）**: `cmd/dev` は同期 in-process publisher で
> クロスコンテキストメッセージを即時配送します。これは「注文が在庫のドメイン型を知らずに
> 契約だけで到達する」という **decoupling** を示しますが、実運用の**遅延ある eventual
> consistency（結果整合）のタイミング**は示しません。遅延を伴う本物の結果整合は、PostgreSQL
> のアウトボックス + 送信中継（`docker compose` 経路）で観察できます。

### 1) Docker なしで動かす（`make dev` と `make test`）

DB もコンテナも要らずに、両コンテキストを 1 プロセスで結線して「まず動く」様子を確認できます。
インメモリ実装はモックではなく、擬似トランザクションと楽観的排他制御を備えた**本物のアダプタ**です。

```sh
# 開発ハーネス: 補充 → 注文（予約 + 確定）→ 照会 → 取消（解放）→ 在庫不足で拒否 を一気に実行
make dev

# 全モジュールのテスト（相関 ID・作業単位・排他衝突・RFC 9457・seam の翻訳まで通しで検証。
# 開発ハーネスの端から端までのスモークテストも含む）
make test
```

`cmd/dev` は各コンテキストの**公開ファサード**（`inventory.Module` / `ordering.Module`）と
公開 `port` だけを結線します。注文には在庫を直接呼ぶ in-process ACL と、コミット時にピアへ
同期配送する publisher を注入します（Go の `internal/` 規則により、ハーネスは各コンテキストの
内部実装へは到達できません）。

> 注文コンテキストのテストには、腐敗防止層（`aclhttp`）が在庫の内部 API 契約どおりに要求を
> 組み立て、在庫のエラー（409 / 5xx / タイムアウト）を注文側の番兵へ翻訳し、`trace_id` が
> 作成 → 予約でサービスを跨いで伝播することの検証（httptest スタブ）が含まれます。

### 2) docker compose で動かす（PostgreSQL・分散サービス）

下の 1 コマンド（`tools/versions.env` を export してから compose を呼ぶ）で、手作業なしに次までを
立ち上げます:
PostgreSQL 起動 →（init コンテナで）**宣言的スキーマ適用（psqldef）→ 最小権限ロール/GRANT →
本番参照データ → dev/test フィクスチャ** → 在庫サービス・注文サービスの起動。分散構成
（サービスごとに独立コンテナ、1 つの物理 DB を schema-per-context で論理分割）を体験できます。

- 適用順は専用の init コンテナ（`migrate`）が担い、`depends_on`（`service_healthy` /
  `service_completed_successfully`）で決定的に強制します。
- サービスは superuser ではなく、**自スキーマだけにスコープした最小権限ロール**で接続します。
- **公開 API ポートだけをホストに publish** します（在庫 8080 / 注文 8082）。在庫の内部 API
  （8081）と PostgreSQL（5432）は compose ネットワーク内に留めます。

```sh
# バックグラウンドで起動する（停止と後片付けは make down）。
# Makefile が tools/versions.env を export してから compose を呼ぶ（migrate の psqldef 版を
# build.args で渡すため）。export 方式なので compose 既定の root .env 自動読込
# （デモ資格情報の上書き）も維持される。
make up
```

起動後、次を試せます：

```sh
# 在庫を補充する（未登録 SKU は新規作成される）
curl -X POST localhost:8080/stock/WIDGET-001/replenish \
  -H 'Content-Type: application/json' -d '{"quantity":10}'

# 在庫を照会する
curl localhost:8080/stock/WIDGET-001

# 存在しない SKU（RFC 9457 の problem+json が 404 で返る。type は resource-not-found）
curl -i localhost:8080/stock/UNKNOWN

# 未定義のパス（同じ 404 でも type は not-found。クライアントは type で区別できる）
curl -i localhost:8080/no-such-endpoint

# 契約違反（必須プロパティの欠落）。400 + invalid-params に違反フィールドが並ぶ
curl -i -X POST localhost:8080/stock/WIDGET-001/replenish \
  -H 'Content-Type: application/json' -d '{}'

# ドメインの検証違反（補充数量 0）。422 + invalid-params
curl -i -X POST localhost:8080/stock/WIDGET-001/replenish \
  -H 'Content-Type: application/json' -d '{"quantity":0}'
```

在庫の**内部 API**（予約・確定・解放）は既定ではホストに publish しません（compose ネットワーク
内でのみ到達可能）。注文サービスはこの内部 API へ `http://inventory-service:8081` で到達します。
内部 API を手元から直接叩いて確認したいときは、開発時に限り compose の該当 `ports` を
一時的に開けてください（本番の分散構成では内部 API はネットワーク隔離が前提です）。

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

> `docker-compose.yml` に書かれた認証情報（管理者ロール・各サービスロールのパスワード）は
> すべて**デモ専用**です。本番では使わないでください。秘密情報はイメージに焼き込まず（すべて
> 実行時の環境変数）、環境変数やシークレットマネージャから注入します。`.env` は gitignore 済みです。

### 統合テスト（PostgreSQL アダプタ）

`postgres` アダプタの統合テストは build tag `integration` を付けたときだけ実行され、
`DATABASE_URL` が指す稼働中の PostgreSQL に接続します（未設定ならスキップ）。既定の compose は
Postgres をホストに publish しないため、テスト時のみオーバーレイで 5432 を公開します。

```sh
# DB（+ init コンテナ）をテスト用に起動して 5432 をホストへ公開し、そのまま統合テストを回す。
# 接続は後始末でスキーマ横断するため管理者ロール（デモ専用）を使う。別の DB を指すなら
# make test-integration DATABASE_URL=postgres://... で上書きする。
make test-integration
```

## 契約ガバナンス（CI ゲート）

契約（OpenAPI / メッセージスキーマ）は真実の源であり、後方互換を CI ゲートで守ります。
ローカルでも同じスクリプトで再現できます。

```sh
make contracts   # OpenAPI 後方互換（oasdiff）+ メッセージ契約の後方互換（type/required の不変性）
make cover       # domain + application のカバレッジ >= 80%
make ci          # 上記を含む CI の ci ジョブ相当を丸ごと再現する
```

`make fuzz`（任意実行の fuzz 探索）は**ゲートではありません**。打ち切り時刻でしか止まらず
結果が実行ごとに変わるため、`make ci` には含めていません。ゲートに載るのは fuzz の
seed corpus（`testdata/fuzz/` にコミット済み）だけで、これは `make test` で毎回走ります。

破壊的変更が必要なときは、**既存の契約を「その場で」変えず**、メジャーバージョンを上げて
ベースライン（`*.baseline.*`）を更新するか、メッセージなら新しい `type`（新スキーマファイル）を
追加してバージョン移行します。

### リリース時にベースラインを更新する

`*.baseline.*` は「**最後にリリースした契約**」のスナップショットです。互換ゲートは
「前回リリース以降の破壊的変更」を検出するので、**リリース（git タグ + GitHub Release）のたびに**
その時点の契約でベースラインを更新し、同じコミットに含めてください。

```sh
# リリース直前に実行する。契約を変えていないリリースなら差分ゼロになる。
cp contracts/api/inventory/openapi.yaml          contracts/api/inventory/openapi.baseline.yaml
cp contracts/api/inventory/internal.openapi.yaml contracts/api/inventory/internal.openapi.baseline.yaml
cp contracts/api/ordering/openapi.yaml           contracts/api/ordering/openapi.baseline.yaml
cp contracts/events/ordering/confirm_reservation.schema.json \
   contracts/events/ordering/confirm_reservation.baseline.schema.json
cp contracts/events/ordering/order_cancelled.schema.json \
   contracts/events/ordering/order_cancelled.baseline.schema.json
make contracts
```

**この手順を飛ばしてもゲートは緑のままです。** 契約への「追加」は非破壊なので
`oasdiff --fail-on ERR` を通り、ベースラインが古いことは何も報告しません。気づかないまま
何リリースも進みえます（実際 v0.9.0 以降の 3 リリースで、注文の公開契約のベースラインが
`/shipments` を欠いたままでした）。ずれている間は、その API に対する**将来の破壊的変更が
検出されません** — ベースラインにその API が存在しないからです。

## コード生成

生成ツール（ogen / sqlc / mockgen）は各モジュールの go.mod `tool` ディレクティブで版を固定して
いるため、手元へ別途インストールする必要はありません。`go generate ./...` が `go tool` 経由で
ピン留めした版を解決します（開発者のローカル環境に依存せず同一の生成物になります）。

```sh
# 契約 / SQL を編集したら再生成する（生成物はコミットする）。
# クライアント → 各コンテキスト → shared の順で回る（順序は Makefile の GEN_MODULES が持つ）。
make generate

# 再生成しても差分が出ないこと（冪等性）を検証する。CI が回すのと同じ検査。
make generate-check
```

サーバはコンテキストごとに、クライアントは共有の `clients/inventory` に、同じ内部 OpenAPI から
鏡像の生成設定（サーバ側は `paths/client` を無効化、クライアント側は `paths/server` を無効化）で
生成します。CI では再生成後に差分が出ないこと（冪等性）を検証します。

## 前提ツール

いずれも **dev/CI 専用**で、サービスのランタイム依存には持ち込みません。版の固定は 2 段構えです
— コード生成ツールは go.mod の `tool` ディレクティブ、横断／Docker ツールは
[`tools/versions.env`](tools/versions.env) を単一情報源とし、版番号を他所へハードコードしません。

- Go 1.26 以上（最新安定版。go.work と各 go.mod は `go 1.26.0` を要求）
- `make` — すべてのコマンドの単一入口（[`Makefile`](Makefile)）。macOS / Linux は標準で入っています。
  Windows は WSL または Git Bash を使うか、`make` を別途インストールしてください
- Docker / Docker Compose（PostgreSQL で動かす場合）
- ogen, sqlc, mockgen（コード生成）— 版は各モジュールの go.mod `tool` ディレクティブで固定。
  手動インストールは不要で、`go generate ./...` が `go tool` で解決する
- golangci-lint, goimports（静的解析・整形）— 版は `tools/versions.env`
- oasdiff（OpenAPI の後方互換ゲート）— 版は `tools/versions.env`（`OASDIFF_VERSION`）
- jq（メッセージスキーマの互換ゲート。多くの環境でプリインストール済み）
- psqldef（宣言的スキーマ適用。docker compose の init コンテナ内で使用）— 版は
  `tools/versions.env`（`PSQLDEF_VERSION`）で、Dockerfile へ `ARG` で注入する

## ライセンス

MIT License（`LICENSE` を参照）。依存はすべて寛容ライセンス（copyleft のランタイム依存なし）の
方針です。`golangci-lint`（GPL-3.0）や `sqldef` などは dev/CI 専用ツールであり、サービスの
ランタイムには載りません。
