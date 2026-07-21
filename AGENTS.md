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

## どこに何を書くか

| 関心事 | 置き場所 |
| --- | --- |
| 集約・値オブジェクト・不変条件・ドメインイベント・ドメインサービス | `internal/domain/inventory/` |
| ユースケース、ポート（interface）、サブスクライバ、Reaper | `internal/application/` |
| ポートの実装（DB・インメモリ・ログ）＝出口アダプタ | `internal/adapter/outbound/`（`memory` / `postgres` / `logging`） |
| 公開 HTTP ハンドラ・エラー変換・ミドルウェア＝入口アダプタ | `internal/adapter/inbound/http/`（パッケージ `httpapi`） |
| 内部 HTTP ハンドラ（予約・確定・解放・取り込み）＝入口アダプタ | `internal/adapter/inbound/internalhttp/` |
| ogen 生成の HTTP サーバ（公開 / 内部） | `internal/adapter/inbound/openapi/` / `openapiinternal/` |
| 依存の結線（合成ルート） | `inventory.go`（ファサード）と `cmd/inventory/` |
| 公開 HTTP 契約 / 内部 HTTP 契約 | `contracts/inventory/openapi.yaml` / `internal.openapi.yaml` |
| DB スキーマ・クエリ | `contexts/inventory/db/schema.sql`, `queries.sql` |
| コンテキスト横断の汎用機構 | `shared/`（`uow` / `event` / `outbox` / `id` / `correlation` / `testutil`） |

## よくある作業のレシピ

### ユースケースを追加する

1. `internal/application/` に入力 DTO・出力 DTO と、ユースケース型を追加する。
2. 書き込みなら `uow.Run(ctx, exec, work, func(ctx, repos) error { ... })` を使い、
   「読み込み → ドメイン操作 → 保存」をクロージャ内で完結させる。ドメインイベントは
   外側の変数に退避し、`Run` が成功したあとにのみ配信する。
3. 読み取り専用なら、プール直結の読み取り用 `StockStore` を注入し、作業単位は使わない。
4. ドメインの不変条件はドメイン層のメソッドに実装する（ユースケースには書かない）。
5. インメモリアダプタを使った統合テストを書く。

### 公開 API を変更する

1. `contracts/inventory/openapi.yaml` を編集する。
2. `cd contexts/inventory && go generate ./...` で ogen を再生成する。
3. `internal/adapter/inbound/http/handler.go` の薄いハンドラを、生成された型に合わせて
   更新する。エラーの HTTP 変換は `internal/adapter/inbound/http/errmap.go` の
   `NewError` を更新する。

### 永続化のクエリ／スキーマを変更する

1. `contexts/inventory/db/schema.sql` または `queries.sql` を編集する。
2. `go generate ./...` で sqlc を再生成する。
3. `internal/adapter/outbound/postgres/store.go` を、生成された型・関数に合わせて更新する。

## 機械可読な契約（真実の源）

- 公開 HTTP 契約: `contracts/inventory/openapi.yaml`（RFC 9457 の ProblemDetails を含む）
- 内部 HTTP 契約: `contracts/inventory/internal.openapi.yaml`（予約・確定・解放・取り込み）
- DB スキーマ: `contexts/inventory/db/schema.sql`（stock_items / stock_reservations / outbox）
- DB クエリ: `contexts/inventory/db/queries.sql`

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
- 時刻に依存する処理（TTL / Reaper）は、実時間を直接呼ばず `application.Clock` を注入して
  テスト可能にする（`shared/testutil` の擬似時計を使う）。

## コマンド

```sh
# 生成（ogen + sqlc）
cd contexts/inventory && go generate ./...

# ビルド・静的解析・テスト（各モジュールで）
go build ./...
go vet ./...
golangci-lint run ./...
go test ./...

# DB ありの統合テスト（docker compose で DB を起動してから）
DATABASE_URL=postgres://inventory:inventory@localhost:5432/inventory?sslmode=disable \
  go test -tags=integration ./...
```

## 設計上の要点（変更時に守ること）

- **楽観的排他制御**: 集約はバージョンを保持するだけで、比較はリポジトリが行う。
  版が食い違えば `uow.ErrConcurrencyConflict` を返し、`uow.Run` が再試行する。
- **RFC 9457**: エラーは `application/problem+json` で返す。ドメインのセンチネルを
  HTTP ステータスへ翻訳する（未検出 → 404、入力検証 → 422、排他衝突 → 409）。
- **境界を跨ぐ型**: 将来コンテキストを跨ぐときは、内部のドメイン値オブジェクトをそのまま
  渡さず、翻訳した公開型（文字列や素の DTO）を使う。
