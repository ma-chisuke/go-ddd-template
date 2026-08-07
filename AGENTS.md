# AI 開発ガイド（AGENTS.md）

このリポジトリで AI コーディングアシスタント（および人間の開発者）が、設計を崩さずに
作業するためのガイドです。まず `README.md` と `CONVENTIONS.md` を読んでから着手してください。
語彙は `contexts/<ctx>/GLOSSARY.md`、パターンの実装位置は `docs/ddd-patterns.md`、
境界を割った理由は `docs/why-these-boundaries.md` にあります。
コマンドはすべてルートの `Makefile` が入口です（引数なしの `make` で一覧が出ます）。

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
9. **サブテスト名を 8 語の閉じた語彙で始める。** `t.Run` の名前とテーブル駆動の `name`
   フィールドは `正常系` `異常系` `境界` `冪等` `並行` `契約` `回帰` `性質` のいずれか +
   半角コロン + 半角スペースで始め、`/` を含めません（`go test -run` の階層区切りに
   なるためです）。テーブル駆動のケース名は**必ず `name` フィールド**として持ちます
   （位置指定の第 1 要素にしない — 機械検査から見えなくなります）。
10. **規約は 2 文書に分かれている。** 識別子・ファイル・パッケージ・言語ポリシーは
    [CONVENTIONS.md](CONVENTIONS.md)（「命名」＝ A 群、「パッケージ / ファイルの構成」＝ B 群、
    「言語ポリシー」＝ F 群、「整形・静的解析」の「機械強制の一覧」＝ G 群）、テスト関数名・
    サブテスト名・テストの日本語コメントは
    [docs/testing-conventions.md](docs/testing-conventions.md)（C 群 / D 群 / E 群）にあります。
    どちらも `make lint`（golangci-lint）と `make conventions`
    （`scripts/convention-gate.sh`）が機械的に強制し、両方とも `make ci` に含まれます。
    **規約を足したら強制手段も足す**（規約だけ書いて守られない状態を作らない）。

## どこに何を書くか

パスはコンテキストのモジュール（`contexts/inventory/` または `contexts/ordering/`）からの相対です。
両コンテキストは同じ 4 層構造を持ちます。

| 関心事 | 置き場所 |
| --- | --- |
| 集約・値オブジェクト・不変条件・ドメインイベント・ドメインサービス | `internal/domain/`（`package domain`。1 コンテキストに 1 つで、サブパッケージへは割らない）。集約ルートと不変条件の要約は同ディレクトリの `doc.go`（package コメントは `stylecheck` の ST1000 で必須化されている） |
| **その境界のユビキタス言語**（語 → 業務上の意味 → Go 型 → 定義ファイル） | `contexts/<ctx>/GLOSSARY.md`。ドメインに公開型を足したらここにも 1 行足す（`docs/glossary.md` は索引と、境界を跨いで同名の語の対比だけを持つ） |
| **DDD パターン → 実装位置の索引**（「集約はどこ？ ACL は？」に答える表） | `docs/ddd-patterns.md`（「新しいものをどう足すか」は `docs/add-a-use-case.md`、境界を割った理由は `docs/why-these-boundaries.md`） |
| ユースケース、ポート（interface）、サブスクライバ、Reaper | `internal/application/` |
| ポートの実装（DB・インメモリ）＝出口アダプタ | `internal/adapter/outbound/`（`memory` / `postgres`）。構造化ログは `shared/logging`、no-op Publisher は `shared/outbox/logpub` |
| 公開 HTTP ハンドラ・エラー変換＝入口アダプタ（相関 ID ミドルウェアは共有の `shared/correlation/corrhttp`） | `internal/adapter/inbound/httpapi/` |
| ハンドラ戻り値のエラー → HTTP（E4） | `internal/adapter/inbound/httpapi/errmap.go`（`NewError` / `classify`） |
| デコード失敗・未定義パス・メソッド不許可（E1〜E3）と `type` URI・`code` → `reason` 表 | `internal/adapter/inbound/httpapi/problem.go` |
| ogen 生成の HTTP サーバ | `internal/adapter/inbound/openapi/`（在庫の内部 API は `openapiinternal/`） |
| 依存の結線（合成ルート） | ファサード（`inventory.go` / `ordering.go`）と `cmd/<ctx>/` |
| HTTP サーバの起動・停止（ライフサイクル機構） | `shared/serve`。`cmd/<ctx>/main.go` には配線（env 読取・`signal.NotifyContext`・`defer pool.Close()`・`Deps` 組立・mux + healthz）だけを残す |
| Docker 不要の開発ハーネス（両コンテキストを 1 プロセスで結線） | `cmd/dev/`（公開ファサード + `port` + `shared` + `clients` のみ） |
| DB スキーマ・クエリ | `db/schema.sql`, `db/queries.sql` |
| 最小権限ロール/GRANT・本番参照データ・dev/test フィクスチャ・psqldef スコープ | `db/roles.sql`, `db/seed.sql`, `db/fixtures.sql`, `db/sqldef.yml` |
| bring-up オーケストレーション（schema → roles → seed → fixtures） | `deploy/migrate.Dockerfile`, `deploy/apply.sh`, `docker-compose.yml` |
| **テストの名前・サブテスト名・日本語コメントの規約**（C 群 / D 群 / E 群） | `docs/testing-conventions.md`（識別子・ファイル・言語ポリシーの規約は `CONVENTIONS.md`） |
| **lint で表現できない規約の機械強制**（サブテスト名の 8 語語彙、package 名 = ディレクトリ名、規約系 Markdown の半角スペース境界など） | `scripts/convention-gate.sh`（`make conventions` で実行。`make ci` と CI の step にも入っている） |
| 契約ガバナンスゲート（後方互換・カバレッジ） | `contracts/check-openapi-compat.sh`, `contracts/events/check-compat.sh`, `scripts/coverage-gate.sh` |
| **腐敗防止層（ACL）ポート** `StockReserver`（注文） | `contexts/ordering/internal/application/ports.go`（全 outbound ポートの唯一の置き場。B-5） |
| **ACL の番兵** `ErrReservationRejected` / `ErrReservationUnavailable`（注文） | `contexts/ordering/internal/application/errors.go` |
| **ACL の HTTP 実装**（生成クライアントで在庫を予約・解放 + trace 伝播）（注文） | `contexts/ordering/internal/adapter/outbound/aclhttp/` |
| **アウトボックス送信トランスポート**（在庫の `/events` へ HTTP push）（注文） | `contexts/ordering/internal/adapter/outbound/eventhttp/` |
| **公開の翻訳済み DTO**（境界を跨ぐ型） | `contexts/<ctx>/port/` |
| 公開 HTTP 契約 / 在庫の内部 HTTP 契約（= ACL サーフェス） | `contracts/inventory/{openapi,internal.openapi}.yaml`, `contracts/ordering/openapi.yaml` |
| **クロスコンテキストのメッセージ契約**（コマンド / イベント） | `contracts/events/*.schema.json` |
| **共有の生成クライアント**（消費側が import・手編集しない） | `clients/inventory/invclient/` |
| コンテキスト横断の汎用機構（純インフラ） | `shared/`（`uow`〔+`pgxuow`〕 / `event` / `serve` / `outbox`〔+`memory`,+`logpub`〕 / `id` / `correlation`〔+`corrhttp`〕 / `logging` / `problem`〔+`ogenproblem`〕 / `worker` / `clock`） |

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
3. `internal/adapter/inbound/httpapi/handler.go` の薄いハンドラを、生成された型に合わせて
   更新する。エラーの HTTP 変換は `internal/adapter/inbound/httpapi/errmap.go` の
   `NewError` を更新する。
4. **新しいサーバを組み立てるなら `NewServer(h, h.ServerOptions()...)` と書く。**
   オプションを渡し忘れると ogen の既定エラーハンドラが使われ、内部文字列
   （`{"error_message": "operation ...: decode request: ..."}`）が外部へ漏れる。
   本番の合成ルートもテストも同じヘルパー経由で組み立てる。
5. **公開契約のオペレーションには、返しうる 4xx/5xx を明示ステータスとして宣言し、
   加えて `default` を宣言する。** 明示コードは `errmap.go` の `classify` から導く
   （実際に到達するものだけ。羅列しない）。`default` は未処理エラーの受け皿（= 500）。
   明示コードを宣言すると ogen はハンドラ戻り型を `<Op>Res` union にするので、`handler.go`
   の戻り型を union へ変える（成功応答型はマーカーで union を満たすので本体は変えない。エラーは
   `return nil, err` のまま `NewError` へ委譲）。**内部契約
   `contracts/inventory/internal.openapi.yaml` は `default` のみに保つ**（この契約は生成
   クライアント経由で ACL に消費され、ACL はステータス区分だけを見る＝規則 R-16。明示コードを
   足すと宣言済みコードが union の値としてクライアントに返り ACL の非 2xx 翻訳が壊れる）。
   詳細は `CONVENTIONS.md` の「契約に宣言するステータスコード」。

### 新しい値オブジェクト・検証規則を追加する（エラー応答の規約）

ドメインの検証規則を足すコストは **3 箇所の編集**である。規約の全体像は `CONVENTIONS.md` の
「HTTP エラー応答（RFC 9457 / Problem Details）」にある。

1. **ドメイン層** `internal/domain/errors.go` の `Rule` 一覧に 1 行足す。

   ```go
   VQuantity = Rule{Field: "quantity", Code: "invalid_quantity", Err: ErrInvalidQuantity}
   ```

   `Rule` はフィールド名・`code`・番兵を 1 箇所に束ねた検証規則である。**番兵の定義は
   変えない**（`errors.Is` の判定単位であり既存の公開 API）。新しい番兵が要るなら、
   上の `var` ブロックにも 1 行足してから `Rule` から指す。

2. **インターフェース層** `internal/adapter/inbound/httpapi/problem.go` の `domainReasons` に
   「規則 → 定型文」を 1 行足す。

   ```go
   domain.VQuantity.Code: "1 以上の値を指定してください",
   ```

   受信値も閾値も書かない（FR-2.3 / FR-2.4）。キーを `Rule` から引いているので、
   `code` の綴りがドメイン側とずれることは構造的に起こらない。

3. **契約（OpenAPI）** `contracts/<ctx>/openapi.yaml`（在庫コンテキストは内部 API の
   `contracts/inventory/internal.openapi.yaml` も）の `InvalidParam.code` の `enum` に、その
   `code` を 1 行足す。

   ```yaml
   - invalid_quantity
   ```

   `code` は契約で `enum` 化されており、ogen が `InvalidParamCode`（`string` の別名）を生成する。
   契約は機械可読な語彙台帳を兼ねる（契約を読むだけで取りうる `code` が分かる）。足し忘れると、
   その `code` を載せた応答が生成型 `ProblemDetails.Validate()` で弾かれ CI が落ちる
   （`problem_test.go` の網羅テストと `readProblem` の Validate が二重に守る）。`go generate`
   の再生成も忘れずに。

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
`validation_path_test.go`（`Path`）、`problem_test.go`（JSON パスと `code` / `reason`、および
新 `code` が契約の `enum` に含まれることを固定する `TestProblem_InvalidParamCodeEnumCoversVocabulary`）。

### 永続化のクエリ／スキーマを変更する

1. `db/schema.sql` または `queries.sql` を編集する。
2. `go generate ./...` で sqlc を再生成する。
3. `internal/adapter/outbound/postgres/<集約名>_store.go` を、生成された型・関数に合わせて更新する。

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
- インメモリ構成では配送キューと恒久ログを `shared/outbox/memory.Stores` が 1 つに束ねる:
  `memory.NewUnitOfWork(rows, memory.NewStores())` の 2 引数で結線し、コミット時に
  `Stores.CommitStaged` が両方へ**同時に**確定させる（片方だけ書く公開 API は無い＝
  「キューに積んだがログに無い」状態が型として起こり得ない）。events を検証したい構成ルート／
  テストは `stores.Events()`、配送キューは `stores.Queued()` を読む（`application.Repos` に
  読み取り面は増やさない）。送信中継へは配送キュービュー `stores.Outbox()` を渡す。
- 時刻に依存する処理（TTL / Reaper）は、実時間を直接呼ばず `application.Clock` ポートを注入して
  テスト可能にする（本番は `shared/clock` の `clock.System`、テストは手で進める `clock.NewManual`）。

## `shared/`（共有インフラ）を扱うときの要点

- **置いてよいのはドメイン非依存かつ外部依存ゼロの建材だけ**（`id` / `correlation` / `uow` /
  `outbox` / `logging` / `worker` / `event` / `serve` / `problem` / `clock`）。ドメイン値
  オブジェクト（`SKU` / `Quantity` / `Money`）や DB ドライバ・HTTP フレームワーク・特定
  コンテキストの型は `shared/` に置かない。判断は上から順に 4 つ:
  ① ドメイン語彙を名前にも構造にも含まないか →
  ② 型パラメータで受けるだけで済み、コンテキストの package を import せずに書けるか →
  ③ `shared/go.mod` に `require` を足さずに書けるか →
  ④ 呼び出し元が 2 つ以上あるか（1 つなら置かない = 先回りの共通化をしない）。
  1 つでも「いいえ」なら `shared/` へは置かず、コンテキスト側に残す。
- **依存は「コンテキスト → `shared`」の一方向**で、depguard の `shared-purity` rule が
  `shared/` からの `contexts/`・`clients/` import を機械的に禁じている（`_test.go` も対象）。
  この rule に引っかかったら回避策を探すのではなく、**それは `shared/` に置くべきものではない**
  というシグナルとして扱う。
- **`shared/event`**: `event.go` が型なしコア（`InProcess`）、`typed.go` が型付きファサード
  （`Typed[E Occurred]`）。合成ルートは `event.NewTyped[domain.DomainEvent](log)` のように型引数を
  綴って直接生成する。per-context の委譲コンストラクタは作らない（「機構は共有・型はコンテキスト
  固有」が呼び出し側から見えている状態を保つ）。ドメイン層は `shared/event` を import せず、
  `DomainEvent` が `EventName()` + `OccurredAt()` を持つことで `event.Occurred` を構造的に満たす。
  `Dispatch` はエラーを返さない（永続化成功後の後処理という契約 — 署名を変えない）。
  `EventDispatcher` ポート宣言そのものは**消さない**（mockgen の生成元であり、ポートは
  コンテキストのドメイン型で宣言されるべきものだから）。置き場所は他の outbound ポートと同じ
  `contexts/*/internal/application/ports.go`。かつては専用ファイルに単独で置かれていたが、
  そこへ集約された — 移ったのは置き場所であって、ポートが消えたわけではない。
- **`shared/serve`**: `serve.Run(ctx, log, servers ...serve.Server)` が HTTP サーバ群の起動・
  停止待ち・全サーバのグレースフルシャットダウンを担う（本数非依存）。サーバを足すときは
  `serve.Server{Name: …, Addr: …, Handler: …}` を 1 つ増やすだけでよく、`*http.Server` の
  組み立て・起動 goroutine・`ErrServerClosed` の除外・`Shutdown` を `main.go` に書き戻さない。
  逆に**ランナーへ移してはいけないもの**が 3 つある: シグナル受信（`signal.NotifyContext`。
  プールとワーカーがランナー起動前に同じ ctx を要る）、資源解放（`defer pool.Close()`。
  ランナー到達前の早期 return で漏れる）、ヘルスチェック（運用契約なので各 mux に載せる）。
- **重複を見つけても機械的に `shared/` へ寄せない**。判断は「抽出後にテンプレートが読みやすく
  なるか」で、差の量ではなく性質で決める。生成型（ogen `openapi.*` / sqlc `sqlcgen.*`）を包む
  アダプタコードの重複（`problem.go` / `postgres/outbox.go`）と、ドメイン番兵を名指しする写像
  （`errmap.go`）は**意図的に重複させたまま**にしている（理由は CONVENTIONS.md「共通化しない
  重複」）。これらを共通化する変更は提案しない。
- **ポート本体は純粋に、実装はサブパッケージへ隔離**する。`shared/outbox`・`shared/correlation`・
  `shared/uow` の本体は依存を増やさず、`net/http` や DB ドライバを持ち込まない。実装は
  `shared/uow/pgxuow`（pgx）、`shared/outbox/memory`（インメモリ `Stores`）/ `shared/outbox/logpub`
  （no-op Publisher）、`shared/correlation/corrhttp`（`net/http` ミドルウェア）へ分ける。
- **コンテキストを単独モジュールとして切り出すときは `shared/` も併せて持ち出す**。各コンテキストは
  `shared` に `replace`（相対パス）で依存しており、`shared` を残置するとビルドできない。
  共通化を進めるほど同伴面積は増えるが、これは設計上の後退ではなく明示的なトレードオフである
  （重複を各コンテキストに残せば同伴面積は減るが、間違えやすい機構が 1 箇所にある利点を失う）。
- **相関 ID ミドルウェアは現存する 3 つのサーバ（注文の公開・在庫の公開・在庫の内部）が同じ
  `shared/correlation/corrhttp.Middleware` を使う**。新しい HTTP サーバを足すときもこれを結線し、
  取り込み優先順位（traceparent → X-Correlation-ID → 新規採番）を一箇所に保つ。

## コマンド

**すべての操作はリポジトリルートの `Makefile` が単一入口**である。モジュールを個別に `cd` して
回さない（モジュール一覧は `MODULES` 変数 1 箇所にあり、CI もこれと同じターゲットを呼ぶ）。
ターゲットの一覧は引数なしの `make` で出る。

```sh
# 生成（ogen + sqlc + mockgen）。クライアント → 各コンテキスト → shared の順に回る。
# shared はエラー抽出テスト用のフィクスチャ契約を生成する。
make generate
make generate-check   # 再生成しても差分が出ないこと（冪等性）

# 整形・静的解析・ビルド・テスト（全モジュール）
make fmt              # gofmt + goimports で整形する
make fmt-check        # 未整形が無いことを検証する
make vet
make lint             # golangci-lint（depguard の層 / seam 境界強制を含む）
make conventions      # 規約ゲート（lint で表現できない規約。scripts/convention-gate.sh）
make build            # 統合タグのコンパイル検証を含む
make test
make test-race
make fuzz             # 任意実行の fuzz 探索（時間制限つき。make ci には含まれない）

# 契約ガバナンス・カバレッジのゲート（CI と同じものをローカルで再現）
make contracts        # OpenAPI + メッセージ契約の後方互換
make cover            # domain + application >= 80%
make vuln             # govulncheck
make ci               # CI の ci ジョブ相当（generate-check → … → cover）を丸ごと

# Docker 不要の開発ハーネス（両コンテキストを 1 プロセスで結線して一気に動かす）
make dev

# 分散構成（docker compose）と、DB ありの統合テスト
make up               # 起動（バックグラウンド）
make down             # 停止 + ボリューム削除
make test-integration # テスト用オーバーレイで Postgres を公開してから統合テストを回す
```

**新しいコマンドが要るときは Makefile にターゲットを足す。** README・AGENTS.md・CI に生の
コマンド列を書き戻さない（3 箇所に散った手順が drift するのを構造的に防いでいる）。

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
