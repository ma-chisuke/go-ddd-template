# 集約ルートを 1 つ足すレシピ

新しい**集約ルート**（整合性の境界）をコンテキストへ足す手順です。ユースケースを 1 つ足す
のとは規模が違います — 集約ルートを足すと、ドメイン・ポート・両アダプタ・スキーマ・契約・
テストの**縦切り**が必要になり、機械検査 3 本（12 / 13 / 14）がその全部を見張ります。

**実物があります。** 注文コンテキストの `Shipment` がこのレシピをそのまま辿って追加された
2 つ目の集約ルートです。各ステップに実ファイルを併記してあるので、迷ったら開いてください。

> 集約ルートではなく**ユースケースを 1 つ足す**だけなら [add-a-use-case.md](./add-a-use-case.md) を
> 参照してください。既存の集約に操作を足すのはそちらの手順です。

## 0. その概念は本当に集約ルートか

足す前に確かめます。**別のライフサイクル**と**別のトランザクション境界**を持たないなら、
それは既存集約の子エンティティか値オブジェクトです。

| 問い | 集約ルート | 子エンティティ / 値オブジェクト |
| --- | --- | --- |
| 単独で生成・変更・削除されるか | はい | いいえ（ルート経由でのみ） |
| 自分のトランザクションでコミットされるか | はい | いいえ（ルートと一緒） |
| 他の集約から参照されるとき | **識別子で**参照される | 参照されない（外へ出ない） |

`Shipment` は注文とは別に作られ、別に発送済みになり、注文を `OrderID` で参照します
（`*Order` を保持しません）。`Reservation` は逆で、`StockItem` の内側にだけ存在します。

**この判断を間違えると検査が止めます。** 子エンティティにストアを与えれば検査 13(a) が、
集約ルートにストアを与え忘れれば検査 13(b) が fail します。

## 1. ドメイン（集約ルートと値オブジェクト）

→ 実物: [contexts/ordering/internal/domain/shipment.go](../contexts/ordering/internal/domain/shipment.go)

- `internal/domain/<集約名>.go` を作り、集約ルートの構造体・コンストラクタ
  （`New<X>(...) (*X, error)`）・復元（`Reconstitute<X>(...) *X`）・状態遷移メソッド・getter を
  置きます。
- **その集約に属する値オブジェクトは同じファイルに同居させます**（B-3 則 1）。
  `ShipmentID` / `TrackingNumber` / `ShipmentStatus` は `shipment.go` にあります。
  `identifiers.go` のような技術的な種類で束ねた容れ物は作りません（B-4・検査 4）。
- **他の集約は識別子で参照します。** `Shipment` が持つのは `OrderID` であって `*Order` では
  ありません。相手の集約の状態を知る必要が出たら、それはユースケースの仕事です（§ 3）。
- `AggregateRoot` の 3 メソッド（`Version` / `MarkPersisted` / `PullEvents`）を実装し、
  **コンパイル時表明を同じファイルに置きます**。

  ```go
  var _ AggregateRoot = (*Shipment)(nil)
  ```

  この表明が「どの型が集約ルートか」の唯一の情報源です（B-9）。書き忘れると、検査 13(b) は
  「ストアが無い」ではなく「そもそも集約ルートでない」と読むので、**何も報告されません**。
- **非ルートのドメイン型へのポインタを公開シグネチャに出さないこと**（B-9・検査 12）。
  子を返すときは値のコピーを返します（`Order.Lines() []OrderLine` /
  `StockItem.Reservations() []Reservation`）。
- ドメインイベントを発行するなら `event.go` に型を足します。**`EventName() string` と
  `OccurredAt() time.Time` の両方**が要ります（片方だけでは `DomainEvent` を満たしません）。

## 2. 番兵と検証規則

→ 実物: [contexts/ordering/internal/domain/errors.go](../contexts/ordering/internal/domain/errors.go)

- 予期される失敗ごとに `Err<Reason>` 番兵を `errors.go` へ足します。
  `Shipment` は 4 つ足しました（`ErrShipmentNotFound` / `ErrShipmentNotPreparing` /
  `ErrInvalidShipmentID` / `ErrInvalidTrackingNumber`）。
- **2 つの集約をまたぐ事前条件の番兵は application 層に置きます。** 「注文が出荷可能な状態か」は
  `Shipment` 集約の不変条件ではないので、`ErrOrderNotConfirmedForShipment` は
  [internal/application/errors.go](../contexts/ordering/internal/application/errors.go) にあります。
- 入力検証（422 になるもの）は `Rule` を足します。**書式を取り違えないでください。**

  | | 書式 | 実例 |
  | --- | --- | --- |
  | `Rule.Field` | lowerCamelCase | `"shipmentId"` |
  | `Rule.Code` | snake_case | `"invalid_shipment_id"` |
  | `httpapi.jsonNames` のキー | UpperCamelCase | `"ShipmentId"` |

  `Rule.Field` と `jsonNames` のキーは**大文字小文字が逆**です。§ 7 の落とし穴 1 も参照。

## 3. ポート（`ports.go`）と `Repos`

→ 実物: [contexts/ordering/internal/application/ports.go](../contexts/ordering/internal/application/ports.go)

- `<集約名>Store` インタフェースを `ports.go` に定義します。**`ports.go` 以外に書けません**
  （検査 10）。**`func` 宣言も書けません**（検査 11）。
- `Repos` にアクセサを 1 つ足します。

  ```go
  type Repos interface {
      Orders() OrderStore
      Shipments() ShipmentStore   // ← 足す
      Outbox() MessagePublisher
  }
  ```

  検査 13 がここと集約ルートの 1 対 1 対応を双方向で見張ります。
- **アクセサを足す理由を取り違えないでください。** `Shipments()` が在るのは「出荷の書き込みと
  アウトボックス投入を同一トランザクションで行うため」であって、「注文と出荷を一緒に書く
  ため」ではありません。トランザクションの内側で触れる集約ルートは 1 つに保ちます。

## 4. 永続化（スキーマとクエリ）

→ 実物: [contexts/ordering/db/schema.sql](../contexts/ordering/db/schema.sql) /
[contexts/ordering/db/queries.sql](../contexts/ordering/db/queries.sql)

- `db/schema.sql` にテーブルを足します。**他の集約のテーブルへ外部キーを張りません** —
  FK を張ると DB が 2 つの集約の整合性を強制することになりますが、集約間の整合性は
  トランザクション境界を越えるものでアプリケーションが担うからです。
- `db/queries.sql` に取得・挿入・楽観的排他つき更新を足します。
- **`make generate` で sqlc を再生成します。生成物は手で編集しません。**
- ロールの `GRANT` は「スキーマ内の全テーブル」に対して与えてあるので、
  `db/roles.sql` は触らなくて構いません。

## 5. アダプタ（インメモリと PostgreSQL の両方）

→ 実物: [memory/shipment_store.go](../contexts/ordering/internal/adapter/outbound/memory/shipment_store.go) /
[postgres/shipment_store.go](../contexts/ordering/internal/adapter/outbound/postgres/shipment_store.go)

- **ファイル名は `<集約名>_store.go` でなければなりません**（B-11・検査 14）。期待名は
  コンパイル時表明が名指すポート名から機械的に決まります（`ShipmentStore` →
  `shipment_store.go`）。
- 実装型に**コンパイル時表明**を置きます。これが検査 14 の判定根拠です。

  ```go
  var _ application.ShipmentStore = (*shipmentStore)(nil)
  ```

- インメモリ側は 1 ファイルに 3 つを束ねます — backing store（`<集約名>Rows`）、
  トランザクション束縛のポート実装（`tx<集約名>Store`）、読み取り専用のポート実装
  （`read<集約名>Store`）。**backing store に `Store` の語を使いません**（`application.<X>Store`
  ポートと同名になり「これはポートの実装か」と誤読されるため）。
- `uow.go` の `repos` 構造体・`Within`・`NewUnitOfWork` に 1 行ずつ足します。
  **`commit()` は触りません** — staging は集約に依存しない `applyGroup` の列なので、
  集約が増えても確定処理は 1 文字も変わりません。
- PostgreSQL 側は `NewUnitOfWork` の closure に 1 行足すだけで同一トランザクションに載ります。
- 復元時の注意: 「まだ設定されていない」ことを空文字で保存している欄
  （`preparing` の追跡番号）は、空を拒否する `New<VO>` に通してはいけません。
  復元が失敗します。

## 6. ユースケース

→ 実物: [prepare_shipment.go](../contexts/ordering/internal/application/prepare_shipment.go) /
[mark_shipped.go](../contexts/ordering/internal/application/mark_shipped.go) /
[get_shipment.go](../contexts/ordering/internal/application/get_shipment.go)

- 1 ファイル 1 ユースケースです。読み取り用 DTO（`<集約名>View`）と射影関数は
  [view.go](../contexts/ordering/internal/application/view.go) に足します。
- **他の集約はトランザクションの外で読みます。** `PrepareShipment` は注文の存在と状態を
  `uow.Run` の**前**に確認し、トランザクションの内側では出荷だけを書きます。既存の
  `PlaceOrder` が在庫予約の ACL 呼び出しを外に出しているのと同じ形です。
- その結果として**集約間は結果整合になります**。確認からコミットまでの間に相手の集約が
  変わる競合は原理的に残ります。このテンプレートはそれを受け入れ、補償処理を持ちません
  （採用者が業務要件に応じて足す拡張点です）。
- 書き込みは `uow.Run` の内側で「読み込み → ドメイン操作 → 保存」を完結させます。
  `uow.Run` は衝突を検知して再試行し、そのたびに `Load` からやり直します —
  **これが集約ルートに `Version` / `MarkPersisted` が要る理由**です。
- 読み取り専用ユースケースは作業単位を使わず、プール直結の読み取りストアを注入します。

## 7. 公開 API（契約とエラー応答）

集約を足すときに**最も落としやすいのがここ**です。落とし穴を 4 系統に整理します。
1 つでも落とすと、ステータスは合っているのに応答の中身が間違う、という形で現れます。

### 落とし穴 1: 新しい `domain.Rule` は 3 箇所

| # | 編集先 | 内容 |
| --- | --- | --- |
| 1 | `internal/domain/errors.go` の `Rule` 一覧 | `V<Name> = Rule{Field: ..., Code: ..., Err: ...}` |
| 2 | `httpapi/problem.go` の `domainReasons` | `code` に対する定型文（受信値も閾値の由来も載せない） |
| 3 | `contracts/<ctx>/openapi.yaml` の `InvalidParam.code` の `enum` | その `code` を 1 行 |

**3 を落とすと CI が落ちます。** `code` は契約で `enum` 化されており、生成型
`InvalidParamCode.Validate()` が enum 外の値を弾きます。追加先は**ドメイン検証語彙群の末尾**
です（その上にある契約検証語彙群は `shared/problem/vocab.go` が唯一の情報源なので、
そちらへ足してはいけません）。

### 落とし穴 2: 新しい type URI 種別は 2 箇所

新しい problem type を作るなら [shared/problem/type_uri.go](../shared/problem/type_uri.go) の
**定数と `titles` マップの両方**に足します。`titles` を落とすと `TitleOf` が HTTP の理由句へ
fallback し、**同じ status の 2 種別が同じ title になって**「title から type を逆引きできる」
不変条件が壊れます（409 の `conflict` と `order-not-confirmed-for-shipment` が実例）。
detail の定型文も同じファイルに足します。

### 落とし穴 3: 新しい番兵のステータス対応は 3 関数

[httpapi/errmap.go](../contexts/ordering/internal/adapter/inbound/httpapi/errmap.go) の

1. `classify()` — ステータスを決める。**型ではなく番兵を `errors.Is` で明示列挙**しています。
   足し忘れると `default` に落ちて **500** になります（契約が 422 と宣言していても）。
2. `problemTypeSuffix()` — 同じ status の中の下位種別を決める。より特殊な種別を先に判定します。
3. `detailOf()` — 種別ごとの定型文。足し忘れると 5xx 用の「予期しないエラーが発生しました」が
   4xx に載ります。

### 落とし穴 4: `invalid-params` は自動では埋まりません

`invalid-params[]` は 3 段のパイプラインを通って初めて埋まります。

```
domain.Rule           … 番兵を FieldViolation（Field / 任意の Index）として生成する
      ↓
application.locate()  … FieldViolation を ValidationError へ包み、パス断片を前置する
      ↓
httpapi.jsonNames     … Go / DTO の識別子を JSON・パラメータ名へ写す上書き表
```

ユースケースで `locate(at, err)` を呼ばないと、ステータスと type URI が正しくても
`invalid-params` は**常に空**です。`jsonNames` へ足すのは機械変換（先頭 1 文字を小文字に）で
正しくならないものだけです — パスパラメータ `/shipments/{id}` は `"ShipmentId": "id"` が要り、
本文の `trackingNumber` は不要です。

> **既知の限界: `jsonNames` は文脈を持ちません。** 同じ DTO 断片が操作によって別の JSON 名へ
> 写る場合（`orderId` は `getOrder` ではパスパラメータ `id`、`prepareShipment` では本文の
> `orderId`）、一方しか表せません。テンプレートはこの場合に**位置の解決自体を行わず**、
> `invalid-params` をキーごと省きます（誤った位置を主張しない）。実装と理由は
> [prepare_shipment.go](../contexts/ordering/internal/application/prepare_shipment.go) の
> コメントにあります。恒久的に解くには「操作ごとの位置」を表現できる仕組みが要ります。

### 契約と生成

- パスは**集約の階層を反映**させます。`Shipment` は `Order` の子リソースではなく独立した
  集約ルートなので `/orders/{id}/shipments` ではなく `/shipments` で、注文への参照は本文の
  `orderId` で表します。状態遷移はサブリソースへの `POST`（`/shipments/{id}/ship`）です。
- オペレーションごとに**実際に返しうる 4xx/5xx を明示**します（`errmap.go` の `classify` と
  対応させます）。
- `make generate` で ogen を再生成し、`httpapi/handler.go` に薄いハンドラを足します。
- `contracts/<ctx>/openapi.baseline.yaml` は**リリース時に**更新するもので、機能追加の PR では
  触りません。操作の追加は破壊的変更ではないので `make contracts` は green のままです。

## 8. 合成ルートとモック

- ファサード（`<ctx>.go`）の `New` / `NewInMemory` / `build` に、backing store・読み取りストア・
  ユースケースを結線します。`cmd/<ctx>/` と `cmd/dev` は公開ファサードだけを見ているので
  通常は変更不要です。
- `internal/mock/generate.go` の `go:generate` 行に新しいポート名を足し、`make generate` で
  `Mock<X>Store` と更新後の `MockRepos` を再生成します。

## 9. テスト

- **ドメイン**: 状態遷移の成功と失敗（前提を満たさない遷移・不正な値オブジェクト）。
- **ユースケース**: happy path と、2 つ以上のエラー経路。
- **アダプタ**: ロールバックとコミットを**1 回の観測で対にします**。
  「ロールバック後に 0 件」だけなら何も書かない実装が、「コミット後に 1 件」だけなら
  常に書く実装が通ってしまいます。実物は
  [memory/uow_test.go](../contexts/ordering/internal/adapter/outbound/memory/uow_test.go)。
- **HTTP**: 422 の `invalid-params`（名前と code）と、新しい type URI の title を固定します。
- **domain + application は行カバレッジ >= 80%** を保ちます（`make cover`）。

## 10. 仕上げ

```sh
make generate    # ogen / sqlc / gomock を再生成する（生成物はコミットする）
make ci          # 生成の冪等性 → 整形 → vet → lint → 規約ゲート → ビルド → テスト → カバレッジ
make contracts   # 契約の後方互換
```

規約ゲートが集約に関して見張るのは 3 本です。

| 検査 | 何を止めるか |
| --- | --- |
| 12 | 集約ルートでないドメイン型が、公開シグネチャからポインタで漏れる |
| 13 | 集約ルートとリポジトリの 1 対 1 が崩れる（子にストア／ルートにストア無し） |
| 14 | 集約ストアの実装が `<集約名>_store.go` 以外のファイルに居る |

**新しい検査を足したときはカナリアで発火を確認してください**（G-1）。違反を注入して当該検査が
報告することを確かめ、**注入した違反がその検査に到達したことまで**出力で見てから revert します。

どのパターンがどのファイルにあるかは [ddd-patterns.md](./ddd-patterns.md)、境界の引き方は
[why-these-boundaries.md](./why-these-boundaries.md)、足した語の用語集は
`contexts/<ctx>/GLOSSARY.md` を参照してください。
