# AI 開発ガイド（AGENTS.md）

このリポジトリで AI コーディングアシスタント（および人間の開発者）が、設計を崩さずに
作業するためのガイドです。まず `README.md` と `CONVENTIONS.md` を読んでから着手してください。

このリポジトリは、構造・規約・機械可読な契約によって「AI 支援開発が DDD 設計に
忠実であり続ける」ことを狙っています。契約はコード生成の入力であると同時に、
あなた（AI）が意図を読み取るためのガイドでもあります。

## 破ってはいけない規則

1. **生成コードを手で編集しない。** ogen（`internal/interfaces/openapi/`）と
   sqlc（`internal/infrastructure/postgres/sqlcgen/`）の出力は生成物です。挙動を変えたい
   ときは、元の契約（`contracts/inventory/openapi.yaml`）や SQL（`contexts/inventory/db/`）を
   編集して `go generate ./...` で再生成し、生成物をコミットします。
2. **業務ルールはドメイン層に置く。** infrastructure / interfaces 層には業務ロジックを
   置きません。オーケストレーションはアプリケーション層です。
3. **ドメイン層を純粋に保つ。** `internal/domain/**` から永続化・HTTP・IO・フレームワークを
   import しません（`net/http`, `database/sql`, `github.com/jackc/pgx`,
   `github.com/ogen-go/ogen` など）。この規則は depguard で機械的に強制されており、
   違反するとビルドが止まります。
4. **トランザクションを `context.Context` に載せない。** 書き込みは必ず
   `UnitOfWork.Within` の内側で、コールバック引数のリポジトリ束から取得したリポジトリを
   使って行います。`context.Context` には相関 ID などの付帯情報だけを載せます。
5. **サービスとして本番デプロイしない。** これはテンプレートです。リリースは
   git タグと GitHub Releases（SemVer）で行います。唯一の実行時依存（PostgreSQL）は
   デモ・テスト用に docker compose でローカル起動するだけです。

## どこに何を書くか

| 関心事 | 置き場所 |
| --- | --- |
| 集約・値オブジェクト・不変条件・ドメインイベント | `internal/domain/inventory/` |
| ユースケース、ポート（interface） | `internal/application/` |
| ポートの実装（DB・インメモリ・ログ） | `internal/infrastructure/` |
| HTTP ハンドラ・エラー変換・ミドルウェア | `internal/interfaces/` |
| 依存の結線（合成ルート） | `inventory.go`（ファサード）と `cmd/inventory/` |
| 公開 HTTP 契約 | `contracts/inventory/openapi.yaml` |
| DB スキーマ・クエリ | `contexts/inventory/db/schema.sql`, `queries.sql` |

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
3. `internal/interfaces/handler.go` の薄いハンドラを、生成された型に合わせて更新する。
   エラーの HTTP 変換は `internal/interfaces/errmap.go` の `NewError` を更新する。

### 永続化のクエリ／スキーマを変更する

1. `contexts/inventory/db/schema.sql` または `queries.sql` を編集する。
2. `go generate ./...` で sqlc を再生成する。
3. `internal/infrastructure/postgres/store.go` を、生成された型・関数に合わせて更新する。

## 機械可読な契約（真実の源）

- 公開 HTTP 契約: `contracts/inventory/openapi.yaml`（RFC 9457 の ProblemDetails を含む）
- DB スキーマ: `contexts/inventory/db/schema.sql`
- DB クエリ: `contexts/inventory/db/queries.sql`

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
