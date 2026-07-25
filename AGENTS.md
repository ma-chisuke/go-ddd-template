# AI 開発ガイド（AGENTS.md）

このリポジトリで AI コーディングアシスタント（および人間の開発者）が、設計を崩さずに
作業するためのガイドです。まず `README.md` と `CONVENTIONS.md` を読んでから着手してください。

このリポジトリは、構造・規約・機械可読な契約によって「AI 支援開発が DDD 設計に
忠実であり続ける」ことを狙っています。契約はコード生成の入力であると同時に、
あなた（AI）が意図を読み取るためのガイドでもあります。

## 破ってはいけない規則

1. **生成コードを手で編集しない。** ogen（`internal/adapter/inbound/openapi/`）と
   sqlc（`internal/adapter/outbound/postgres/sqlcgen/`）の出力は生成物です。挙動を変えたい
   ときは、元の契約（`contracts/inventory/openapi.yaml`）や SQL（`contexts/inventory/db/`）を
   編集して `go generate ./...` で再生成し、生成物をコミットします。
2. **業務ルールはドメイン層に置く。** adapter（inbound / outbound）層には業務ロジックを
   置きません。オーケストレーションはアプリケーション層です。
3. **ドメイン層を純粋に保つ。** `internal/domain/**` から永続化・HTTP・IO・フレームワーク・
   アダプタを import しません（`net/http`, `database/sql`, `github.com/jackc/pgx`,
   `github.com/ogen-go/ogen`、`internal/adapter/**` など）。この規則は depguard で
   機械的に強制されており、違反するとビルドが止まります。さらに、入口（inbound）と
   出口（outbound）が互いを直接 import することも depguard で禁止しています。
4. **トランザクションを `context.Context` に載せない。** 書き込みは必ず
   `UnitOfWork.Within` の内側で、コールバック引数のリポジトリ束から取得したリポジトリを
   使って行います。`context.Context` には相関 ID などの付帯情報だけを載せます。
5. **サービスとして本番デプロイしない。** これはテンプレートです。リリースは
   git タグと GitHub Releases（SemVer）で行います。唯一の実行時依存（PostgreSQL）は
   デモ・テスト用に docker compose でローカル起動するだけです。
6. **コンテキストの seam（縫い目）を跨がない。** 注文コンテキストは在庫コンテキストの
   Go パッケージ（`contexts/inventory/**`）を決して import しません。在庫へは生成クライアント
   `clients/inventory`（在庫の内部 OpenAPI から生成）を介して HTTP 越しにのみ到達します。
   この規則は depguard で機械的に強制されています。コンテキスト間で受け渡すのは翻訳済みの
   公開型（`port` パッケージの DTO や、`contracts/events/` のメッセージ契約）だけで、内部の
   ドメイン値オブジェクトは渡しません。相手の番兵エラーも自コンテキストの番兵へ翻訳します。
   開発ハーネス `cmd/dev` も同様に、公開ファサード + 公開 `port` + `shared` + `clients` だけを
   使い、各コンテキストの `internal/` には到達しません（Go の `internal/` 規則 + depguard）。
7. **エラー応答から内部実装を漏らさない。** 応答本文に `err.Error()` をそのまま載せず、
   ogen / Go 由来の文言（`operation ...`、`decode request`、`unexpected byte`）・Go の型名・
   スタック・**問題となった受信値**（SKU・数量・在庫数など）を含めません。`detail` と
   `invalid-params[].reason` は本プロジェクトが定義した定型文だけを使います。排除した情報は
   ログに残し、相関 ID で追跡できるようにします。詳細は `CONVENTIONS.md` の
   「HTTP エラー応答（RFC 9457 / Problem Details）」。
8. **秘密情報をハードコードしない。** DB 接続文字列・パスワード・トークンをコードや
   イメージに焼き込みません。実行時に環境変数 / シークレットマネージャから注入します
   （`docker-compose.yml` の認証情報はすべて**デモ専用**であることを明記済み）。入力は
   境界（HTTP ハンドラ・契約）で検証します。

## どこに何を書くか

パスはコンテキストのモジュール（`contexts/inventory/` または `contexts/ordering/`）からの相対です。
両コンテキストは同じ 4 層構造を持ちます。

| 関心事 | 置き場所 |
| --- | --- |
| 集約・値オブジェクト・不変条件・ドメインイベント・ドメインサービス | `internal/domain/<ctx>/`（在庫は `inventory`、注文は `order`） |
| ユースケース、ポート（interface）、サブスクライバ、Reaper | `internal/application/` |
| ポートの実装（DB・インメモリ・ログ）＝出口アダプタ | `internal/adapter/outbound/`（`memory` / `postgres` / `logging`） |
| 公開 HTTP ハンドラ・ミドルウェア＝入口アダプタ | `internal/adapter/inbound/http/`（パッケージ `httpapi`） |
| ハンドラ戻り値のエラー → HTTP（E4） | `internal/adapter/inbound/http/errmap.go`（`NewError` / `classify`） |
| デコード失敗・未定義パス・メソッド不許可（E1〜E3）と `type` URI・`code` → `reason` 表 | `internal/adapter/inbound/http/problem.go` |
| ogen 生成の HTTP サーバ | `internal/adapter/inbound/openapi/`（在庫の内部 API は `openapiinternal/`） |
| 依存の結線（合成ルート） | ファサード（`inventory.go` / `ordering.go`）と `cmd/<ctx>/` |
| Docker 不要の開発ハーネス（両コンテキストを 1 プロセスで結線） | `cmd/dev/`（公開ファサード + `port` + `shared` + `clients` のみ） |
| DB スキーマ・クエリ | `db/schema.sql`, `db/queries.sql` |
| 最小権限ロール/GRANT・本番参照データ・dev/test フィクスチャ・psqldef スコープ | `db/roles.sql`, `db/seed.sql`, `db/fixtures.sql`, `db/sqldef.yml` |
| bring-up オーケストレーション（schema → roles → seed → fixtures） | `deploy/migrate.Dockerfile`, `deploy/apply.sh`, `docker-compose.yml` |
| 契約ガバナンスゲート（後方互換・カバレッジ） | `contracts/check-openapi-compat.sh`, `contracts/events/check-compat.sh`, `scripts/coverage-gate.sh` |
| **腐敗防止層（ACL）ポート** `StockReserver` と番兵 `ErrReservationRejected` / `ErrReservationUnavailable`（注文） | `contexts/ordering/internal/application/acl.go` |
| **ACL の HTTP 実装**（生成クライアントで在庫を予約・解放 + trace 伝播）（注文） | `contexts/ordering/internal/adapter/outbound/aclhttp/` |
| **アウトボックス送信トランスポート**（在庫の `/events` へ HTTP push）（注文） | `contexts/ordering/internal/adapter/outbound/eventhttp/` |
| **公開の翻訳済み DTO**（境界を跨ぐ型） | `contexts/<ctx>/port/` |
| 公開 HTTP 契約 / 在庫の内部 HTTP 契約（= ACL サーフェス） | `contracts/inventory/{openapi,internal.openapi}.yaml`, `contracts/ordering/openapi.yaml` |
| **クロスコンテキストのメッセージ契約**（コマンド / イベント） | `contracts/events/*.schema.json` |
| **共有の生成クライアント**（消費側が import・手編集しない） | `clients/inventory/invclient/` |
| コンテキスト横断の汎用機構 | `shared/`（`uow` / `event` / `outbox` / `id` / `correlation` / `problem` / `testutil`） |

## よくある作業のレシピ

### ユースケースを追加する

1. `internal/application/` に入力 DTO・出力 DTO と、ユースケース型を追加する。
2. 書き込みなら `uow.Run(ctx, exec, work, func(ctx, repos) error { ... })` を使い、
   「読み込み → ドメイン操作 → 保存」をクロージャ内で完結させる。ドメインイベントは
   外側の変数に退避し、`Run` が成功したあとにのみ配信する。
3. 読み取り専用なら、プール直結の読み取り用 `StockStore` を注入し、作業単位は使わない。
4. ドメインの不変条件はドメイン層のメソッドに実装する（ユースケースには書かない）。
5. テストを書く。アサーションは testify（`require`/`assert`）。application ポートの
   相互作用（use case がポートを正しい順序・回数で呼ぶか）は `internal/mock` の gomock
   モックで、統合的な振る舞いはインメモリアダプタ（本物のアダプタ）で検証する。詳細は
   `CONVENTIONS.md` の「テスト」を参照。

### 公開 API を変更する

1. `contracts/inventory/openapi.yaml` を編集する。
2. `cd contexts/inventory && go generate ./...` で ogen を再生成する。
3. `internal/adapter/inbound/http/handler.go` の薄いハンドラを、生成された型に合わせて
   更新する。エラーの HTTP 変換は `internal/adapter/inbound/http/errmap.go` の
   `NewError` を更新する。
4. **新しいサーバを組み立てるなら `NewServer(h, h.ServerOptions()...)` と書く。**
   オプションを渡し忘れると ogen の既定エラーハンドラが使われ、内部文字列
   （`{"error_message": "operation ...: decode request: ..."}`）が外部へ漏れる。
   本番の合成ルートもテストも同じヘルパー経由で組み立てる。

### 新しい値オブジェクト・検証規則を追加する（エラー応答の規約）

ドメインの検証規則を足すコストは **2 箇所の編集**である。規約の全体像は `CONVENTIONS.md` の
「HTTP エラー応答（RFC 9457 / Problem Details）」にある。

1. **ドメイン層** `internal/domain/<ctx>/errors.go` の `Rule` 一覧に 1 行足す。

   ```go
   VQuantity = Rule{Field: "quantity", Code: "invalid_quantity", Err: ErrInvalidQuantity}
   ```

   `Rule` はフィールド名・`code`・番兵を 1 箇所に束ねた検証規則である。**番兵の定義は
   変えない**（`errors.Is` の判定単位であり既存の公開 API）。新しい番兵が要るなら、
   上の `var` ブロックにも 1 行足してから `Rule` から指す。

2. **インターフェース層** `internal/adapter/inbound/http/problem.go` の `domainReasons` に
   「規則 → 定型文」を 1 行足す。

   ```go
   order.VQuantity.Code: "1 以上の値を指定してください",
   ```

   受信値も閾値も書かない（FR-2.3 / FR-2.4）。キーを `Rule` から引いているので、
   `code` の綴りがドメイン側とずれることは構造的に起こらない。

そして呼び出し側は 1 行で書く。

```go
if n < 1 {
    return Quantity{}, VQuantity.Violated("注文行の数量は 1 以上でなければなりません（指定値: %d）", n)
}
```

番兵の文言は自動で後ろに連結されるので、`format` には状況の説明だけを書く。集約や
ドメインサービスが**自分でコレクションを走査していて**、何番目で失敗したかを知っている
場合は `VQuantity.ViolatedAt(i, ...)` を使う（位置はアプリケーション層が `Lines[i]` という
パスへ組み立てる）。

アプリケーション層とインターフェース層の残り 2 つの表（`dtoPaths` / `jsonNames`）は
**上書き表**であり、機械的な変換（大文字化 / 小文字化）で正しくならないときだけ
1 行足す。通常は触らなくてよい。

呼び出し側では `locate(at, err)` を必ず通す。**検証以外のエラーを検証エラーに化けさせない。**
`locate` はドメインの違反でなければ透過するので通常は安全だが、リポジトリ失敗や版衝突が
「入力検証エラー」として返ると利用者に嘘をつくことになる。

**単一の入力フィールドに帰着しない規則は `Rule` にしない。** 通貨不一致（`Money.Add`）や
状態の矛盾（409）は素の番兵のまま返し、`invalid-params` を省略する。

テストは 3 層それぞれに足す。`field_violation_test.go`（違反が名乗る `Rule` と `errors.Is`）、
`validation_path_test.go`（`Path`）、`problem_test.go`（JSON パスと `code` / `reason`）。

### 永続化のクエリ／スキーマを変更する

1. `db/schema.sql` または `queries.sql` を編集する。
2. `go generate ./...` で sqlc を再生成する。
3. `internal/adapter/outbound/postgres/store.go` を、生成された型・関数に合わせて更新する。

### コンテキストを跨ぐ呼び出し（ACL / イベント）を扱う

- **同期の在庫予約（ACL）**: 注文のユースケースは `application.StockReserver` ポート越しにのみ
  在庫を呼ぶ。呼び出しは **作業単位の外**（HTTP がトランザクションを跨いで保持されるのを避ける）。
  実装は `aclhttp` が生成クライアント `clients/inventory` で行い、`port.ReserveLine` をクライアントの
  request 型へ写像し、在庫の 409 / 5xx / タイムアウトを注文側の `ErrReservationRejected` /
  `ErrReservationUnavailable` へ翻訳する（在庫の番兵は漏らさない）。
- **確定コマンド（`ConfirmReservation`）**: アプリケーション層が組み立ててアウトボックスへ **直接** 積む
  コマンド。ドメインイベントの `PullEvents` 経路は通らない。作成の成功時に `Save` と同一 `uow.Run`
  クロージャ内で `repos.Outbox().Enqueue(...)` する。
- **クロスコンテキストイベント（`OrderCancelled`）**: ドメインが append したイベントを取消の
  `uow.Run` 内で `PullEvents()` して収集し、`contracts/events/` の契約へ翻訳してアウトボックスへ積む
  （保存と同一トランザクション）。在庫側が購読して非同期に解放する。
- **メッセージ契約を変える**: `contracts/events/*.schema.json`（`type` 文字列 = 契約識別子）を編集し、
  送信側（注文の `messages.go`）と受信側（在庫の `subscriber.go`）の双方を整合させる。破壊的変更は
  `type` を新設してバージョン移行する。
- **trace 相関**: 入口ミドルウェアが W3C traceparent / X-Correlation-ID を受理（無ければ採番）し、
  相関 ID を context に載せる。ACL / イベント送出はそれをヘッダとメッセージの `TraceID` に伝播する。
  遅延／消失した確定の整合はコード分岐で解かず、両サービスのログを `trace_id` で相関して運用で行う。

## 機械可読な契約（真実の源）

- 在庫の公開 HTTP 契約: `contracts/inventory/openapi.yaml`（RFC 9457 の ProblemDetails を含む）
- 在庫の内部 HTTP 契約（= ACL サーフェス）: `contracts/inventory/internal.openapi.yaml`
- 注文の公開 HTTP 契約: `contracts/ordering/openapi.yaml`（作成・照会・取消）
- クロスコンテキストのメッセージ契約: `contracts/events/{confirm_reservation,order_cancelled}.schema.json`
- エラー応答の共通部品: `shared/problem/`（`type` URI 台帳・契約検証の `code` 語彙・
  パス表記）と `shared/problem/ogenproblem/`（ogen のエラーからフィールドを抽出する）。
  後者はテスト専用のフィクスチャ契約
  `shared/problem/ogenproblem/internal/fixture/openapi.yaml` を持ち、ogen の実挙動に対して
  抽出をテストする（版上げでエラー形式が変われば CI が落ちる）
- DB スキーマ / クエリ: `contexts/<ctx>/db/schema.sql`, `queries.sql`
  （在庫: stock_items / stock_reservations / outbox / events、
  注文: orders / order_lines / outbox / events）

## 予約・アウトボックスを扱うときの要点

- **予約は二相**（reserve → confirm）。予約・確定・解放はいずれも冪等に実装する
  （自動リトライと at-least-once 配送のもとで安全にするため）。
- **確定・解放は予約参照（ref）単位**で、`StockStore.LoadByReservation` を使って ref を持つ
  全ての在庫項目を 1 つの作業単位で原子的に遷移させる。単一項目への部分適用は禁止
  （残り SKU の pending が孤児化し、Reaper に誤解放されて二重割当を招く）。
- **Reaper は期限切れの pending のみ**を解放する。confirmed には決して触れない。
- **アウトボックス**へは `repos.Outbox().Enqueue(...)` で、集約の `Save` と同一の
  `uow.Run` クロージャ内から積む（二重書き込みを避ける）。送出は `outbox.Runner` が
  at-least-once で行い、受信は `outbox.Router` が message_type で `Consumer` へ振り分ける。
- **`outbox` は一時的な配送キュー、`events` は恒久イベントログ**。`Runner` は
  `Unpublished` → `Publish` → `MarkPublished` の順で動き、`MarkPublished` は
  送信済みフラグを立てるのではなく**行を削除**する（delete-after-publish）。
  「何を発行したか」の記録は `Enqueue` が**同一トランザクションで書く** `events` 表に残るため、
  outbox から消えても履歴は失われない。`Runner` は `events` を読まない。
  ユースケースの呼び出し面は変わらない（`repos.Outbox().Enqueue(...)` のまま）。
  この順序（送出成功後にのみ削除）は at-least-once の要なので**変えないこと**。
- **`events` は保持ジョブを持たず増え続ける**。採用時はアーカイブ／パーティション／
  保持ジョブを足す（テンプレートは単純さを優先して意図的に持たない）。
- インメモリ構成では配送キューと恒久ログが別ストアになる:
  `memory.NewUnitOfWork(store, outboxStore, eventsStore)` の 3 引数で結線し、
  コミット時に両方へ確定させる。events を検証したい構成ルート／テストは
  `EventStore` を直接保持する（`application.Repos` に読み取り面は増やさない）。
- 時刻に依存する処理（TTL / Reaper）は、実時間を直接呼ばず `application.Clock` を注入して
  テスト可能にする（`shared/testutil` の擬似時計を使う）。

## コマンド

```sh
# 生成（ogen + sqlc）。クライアント → 各コンテキスト → shared の順に。
# shared はエラー抽出テスト用のフィクスチャ契約を生成する。
cd clients/inventory && go generate ./...
cd ../../contexts/inventory && go generate ./...
cd ../ordering && go generate ./...
cd ../../shared && go generate ./...

# ビルド・静的解析・テスト（各モジュールで）
go build ./...
go vet ./...
golangci-lint run ./...
go test ./...

# Docker 不要の開発ハーネス（両コンテキストを 1 プロセスで結線して一気に動かす）
go run ./cmd/dev

# 契約ガバナンス・カバレッジのゲート（CI と同じものをローカルで再現）
bash contracts/check-openapi-compat.sh   # OpenAPI 後方互換（oasdiff）
bash contracts/events/check-compat.sh    # メッセージ契約の後方互換
bash scripts/coverage-gate.sh            # domain + application >= 80%

# DB ありの統合テスト（テスト用オーバーレイで Postgres を公開してから）
# migrate をビルドするため tools/versions.env を export してから compose を呼ぶ。
set -a && . ./tools/versions.env && set +a && \
  docker compose -f docker-compose.yml -f docker-compose.test.yml up -d db migrate
DATABASE_URL=postgres://app:app_admin_demo@localhost:5432/app?sslmode=disable \
  go test -tags=integration ./...
```

## 設計上の要点（変更時に守ること）

- **楽観的排他制御**: 集約はバージョンを保持するだけで、比較はリポジトリが行う。
  版が食い違えば `uow.ErrConcurrencyConflict` を返し、`uow.Run` が再試行する。
- **RFC 9457**: エラーは `application/problem+json` で返す。ドメインのセンチネルを
  HTTP ステータスへ翻訳する（未検出 → 404、入力検証 → 422、排他衝突 → 409）。
- **境界を跨ぐ型**: コンテキストを跨ぐときは、内部のドメイン値オブジェクトをそのまま渡さず、
  翻訳した公開型（`port` の DTO、生成クライアントの wire 型、`contracts/events/` のメッセージ）を使う。
  値オブジェクトはコンテキストごとに独立所有する（例: 注文の `Quantity` は n ≥ 1、在庫は n ≥ 0）。
- **番兵エラーの翻訳**: 相手コンテキスト由来の失敗は自コンテキストの番兵へ翻訳する（`%w` で原因を
  保持しつつ `errors.Join` で自番兵に一致させる）。相手の番兵名をそのまま公開・alias しない。
