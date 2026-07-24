# 規約（CONVENTIONS）

このテンプレートで一貫して守る Go とドメイン駆動設計の規約をまとめます。
新しいコンテキストやユースケースを足すときは、この規約に沿ってください。

## パッケージ / ファイルの構成

- **1 ディレクトリ 1 パッケージ**。
- パッケージ名は**短く・小文字・単数形**。冗長な繰り返し（stutter）を避けます。
  例: `inventory.SKU` とし、`inventory.InventorySKU` とはしません。
- **1 ファイル 1 主要型**を基本とし、ファイル名は型名の snake_case にします。
  例: `StockItem` → `stock_item.go`、`SKU` → `sku.go`。
- **生成ファイルは隔離**します。ogen は `internal/adapter/inbound/openapi/` に、
  sqlc は `internal/adapter/outbound/postgres/sqlcgen/` に出力し、**手で編集しません**。

## 命名

- 型・エクスポートされる識別子は **PascalCase**、非公開は **camelCase**。
- **頭字語は全て大文字**にします。例: `OrderID`, `SKU`, `HTTP`, `URL`。
  `OrderId` や `Sku` とはしません（ただし外部の生成コードの命名はそのツールに従います）。
- コンストラクタは **`New<Type>(...) (Type, error)`** の形にします。
  検証を伴う値オブジェクトや集約は、不正値を弾いてから返します。

## エラー

- 予期される失敗は **`Err<Reason>` センチネルエラー**として定義し、値で返します
  （`panic` しません）。例: `ErrStockItemNotFound`, `ErrInvalidQuantity`。
- ラップするときは **`%w`** を用いてセンチネルを保持し、`errors.Is` による判定を
  壊さないようにします。
- 回復不能・想定外の異常（暗号乱数源の故障など）に限り `panic` を許容します。

## context.Context

- IO を伴うメソッドは **`ctx context.Context` を第 1 引数**に取ります。
- **ドメイン層は `context.Context` を受け取りません**（純粋に保つため）。
- `context.Context` に載せてよいのは**リクエストスコープの付帯情報**（相関 ID など）
  だけです。**トランザクションハンドルのような制御関心を context に隠して運びません**。

## 層の分離（ヘキサゴナル / DDD）

コードは 4 層に分けます。アダプタは方向で対称に **adapter/inbound（入口＝駆動側）** と
**adapter/outbound（出口＝被駆動側）** に分けます。ポートは application 層に置きます。

- **純粋なドメイン**: ドメイン層はリポジトリ（そのポート interface も含む）や
  永続化・IO・フレームワーク・アダプタを import しません。この純粋性は静的解析（depguard）
  で機械的に強制しています。
- **ポートはアプリケーション層に定義**し、**アダプタは adapter/outbound 層で実装**します。
  リポジトリのポートも、翻訳（腐敗防止）のポートも、application 層に置きます。
  application 層はアダプタに依存しません（依存はポート経由で逆転させます）。
- **入口（inbound）と出口（outbound）は互いを直接 import しません**。両者の結線は
  合成ルート（ファサード / cmd）だけで行います。方向ルールは depguard で強制しています。
- ユースケースは **「読み込み → ドメイン操作 → 保存」** を作業単位の内側で行います。
  ドメインサービスはユースケースから受け取ったデータで動き、自分でリポジトリを
  参照しません。
- **業務ルールはドメイン層**（オーケストレーションはアプリケーション層）に置きます。
  adapter（inbound / outbound）層に業務ロジックを置きません。

## 値オブジェクト

- 値オブジェクト（`SKU`, `Quantity` など）は**境界づけられたコンテキストごとに独立して
  所有**します。他コンテキストと安易に共有しません。共有は「意図的で正当化された例外」で
  あって、既定ではありません。
- `shared` モジュールにはドメイン非依存の建材（ID 生成、相関 ID、作業単位など）だけを
  置き、ドメインの値オブジェクトは置きません。

## モジュール境界

- 各コンテキストは 1 つの Go モジュールとして独立します。
- コンテキストは 4 層を `internal/` 配下に隠し、**薄い公開ファサード**
  （ルートパッケージ: `New(Deps) (*Module, error)`, `HTTPHandler()` など）だけを公開します。
- 他コンテキストや合成ルートは、この公開ファサード（および公開 `port` パッケージ）だけに
  依存し、他モジュールの `internal/` には触れません（Go のコンパイラが強制します）。
- コンテキスト間で受け渡すのは**翻訳済みの公開型**（`port` の DTO、`contracts/events/` の
  メッセージ契約、生成クライアントの wire 型）だけです。内部のドメイン値オブジェクトは
  渡しません。相手コンテキストの番兵エラーも自コンテキストの番兵へ翻訳します（`port` に置いた
  `ErrReservationRejected` など）。

## 作業単位（UnitOfWork）とトランザクション境界

- **トランザクション境界は明示的に所有**します。トランザクションハンドルを
  `context.Context` に隠して引き回しません。書き込みは必ず `UnitOfWork.Within` の内側で、
  コールバック引数のリポジトリ束から取得したリポジトリを使って行います。
- 作業単位は Go のジェネリクスで **`uow.UnitOfWork[R]`** として型付けし、各コンテキストは
  自分のリポジトリ束 `R`（例: `Repos { Stock() ...; Outbox() ... }`）で特殊化します
  （`type UnitOfWork = uow.UnitOfWork[Repos]`）。ユースケースはこの束からしかリポジトリを
  取得できないため、「トランザクション外の書き込み」が構造的に起こり得ません。
- 楽観的排他制御の衝突（`uow.ErrConcurrencyConflict`）は `uow.Run` が指数バックオフで
  再試行します。集約はバージョンを**保持するだけ**で、比較（compare-and-set）はリポジトリが
  担います。ユースケースは「読み込み → ドメイン操作 → 保存」をクロージャ内で完結させ、
  再試行時に最新状態を読み直せるようにします。
- クロスコンテキスト送信は**同一トランザクション**でアウトボックスへ積みます
  （`repos.Outbox().Enqueue(...)`）。集約の保存とメッセージ Enqueue を原子的にコミットして
  二重書き込みを避けます。一方、在庫予約の同期 ACL 呼び出しは**トランザクションの外**で
  行います（HTTP 呼び出しが DB トランザクションを跨いで保持されるのを避けるため）。

## SQL と宣言的 DB

- 各コンテキストは自分の `db/` を所有します。**1 つの物理 DB を schema-per-context で論理
  分割**し、他コンテキストのスキーマを直接読み書きしません。
- `db/schema.sql` は「あるべき最終状態」を宣言する DDL で、**psqldef（sqldef）で incremental・
  非破壊に適用**します（drop-and-recreate をしない。破壊的差分は `--dry-run` でプレビュー）。
  `target_tables` で当該スキーマに限定し、psqldef が他コンテキストのオブジェクトに触れない
  ようにします（`db/sqldef.yml`）。
- **psqldef の DDL パーサ制約**: 列 CHECK 制約に `IN (...)` は使えません（パースエラーになる）。
  列挙は `CHECK (status = 'a' OR status = 'b')` で書きます。これは PostgreSQL が保持する形と
  一致するため psqldef の適用も冪等になります（`IN (...)` は内部的に `= ANY(ARRAY[...])` へ
  正規化される）。
- `db/queries.sql` は sqlc の入力で、そこから型安全な Go を生成します。**生成物はコミットし、
  手で編集しません**。
- 権限は宣言的な**冪等ロール/GRANT SQL**（`db/roles.sql`）で与え、各サービスが**自スキーマ
  だけにスコープしたロール**で接続します（実行時 superuser なし・no-cross-schema-reads）。
- **本番参照データ**（`db/seed.sql`、冪等 upsert）と **dev/test フィクスチャ**
  （`db/fixtures.sql`）は別経路にし、fixtures を本番の適用経路に混ぜません。
- 適用順は **schema → roles/GRANT → seed →（dev のみ）fixtures**（GRANT/seed はテーブルの
  存在を前提とするため schema の後）。この順序は bring-up の init コンテナが担います。

## テスト

- テストランナーは標準の `testing`。アサーションは **testify**（`require` は前提が崩れたら
  即中断する致命的検証、`assert` は独立した検証を続行）で書きます。
- **ポート相互作用の検証には uber-go/mock（gomock）** を使います。application 層のポート
  （interface）から `go generate`（`go tool mockgen`）でモックを生成し、`internal/mock`
  パッケージに**コミット**します（手編集しない・再生成で冪等）。「use case がポートを正しい
  順序・回数で呼ぶか」を `EXPECT()` で表明する用途です。カバレッジ計測（domain + application）
  を汚さないよう、生成モックは計測グロブの外（`internal/mock`）へ置きます。
- **インメモリ実装（`adapter/outbound/memory`）はモックではなく本物のアダプタ**です。擬似
  トランザクションや楽観的排他制御まで含めた統合的な振る舞いを、DB 非依存で高速に検証する
  ために使います（gomock とは役割が別で、置き換えではなく併用）。
- コンテキストを跨ぐ **seam（腐敗防止層）は `httptest`** でピア契約どおりのスタブを立てて
  検証します。
- **PostgreSQL アダプタの統合テスト**は build tag `integration` を付けたときだけ実行します
  （ローカル DB / docker-compose 前提）。
- ドメイン層とアプリケーション層は**行カバレッジ 80% 以上**を維持します
  （`scripts/coverage-gate.sh`、CI のマージ前ゲート）。生成コード（ogen / sqlc / mockgen）と
  アダプタ結線はこの閾値の対象にしません。
- テスト用の外部依存（testify / gomock）は**本番コードに持ち込みません**。テストファイルと
  `internal/mock` だけが import してよく、とりわけドメイン層の純粋性を保ちます。

## 整形・静的解析

- `gofmt` と `goimports` で整形し、`go vet` と `golangci-lint` を通します（CI ゲート）。
  層／seam の境界は `depguard` で機械的に強制します。
- 生成コード以外は原則コメント付きで、意図が読み取れるようにします。
