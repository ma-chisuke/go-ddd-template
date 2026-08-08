# 共有の生成クライアント

このディレクトリは、**他コンテキストの内部 HTTP 契約から生成した Go クライアント**を置きます。
消費側（呼び出す方のコンテキスト）が import します。生成物はコミットし、**手で編集しません**
（契約か ogen 設定を直して `make generate` で再生成します）。

現在あるのは 1 つです。

| モジュール | 生成元の契約 | 生成される Go パッケージ | 消費者 |
|-----------|-------------|------------------------|-------|
| `clients/inventory` | `contracts/api/inventory/internal.openapi.yaml` | `invclient` | 注文コンテキストの腐敗防止層（`contexts/ordering/internal/adapter/outbound/aclhttp/`） |

## 同じ契約から、サーバとクライアントが別の場所へ生成される

`contracts/api/inventory/internal.openapi.yaml` は **1 本の契約から 2 つの生成先**を持ちます。
この矢印がこのディレクトリの存在理由です。

```
contracts/api/inventory/internal.openapi.yaml   … 契約（真実の源）
    │
    ├─[internal.ogen.yaml]→ contexts/inventory/internal/adapter/inbound/openapiinternal/
    │                        （サーバ。在庫が実装する側）
    │
    └─[client.ogen.yaml]──→ clients/inventory/invclient/
                             （クライアント。注文が呼ぶ側）
```

<!-- Text fallback: contracts/api/inventory/internal.openapi.yaml という 1 本の契約から、
     internal.ogen.yaml でサーバが contexts/inventory/internal/adapter/inbound/openapiinternal/ へ、
     client.ogen.yaml でクライアントが clients/inventory/invclient/ へ生成される。 -->

生成の向きは ogen 設定で決まります。サーバ側の設定は `paths/client` を、クライアント側の設定は
`paths/server` を、それぞれ `generator.features.disable` で無効化します。設定を契約ごとに
分けているのは、この 2 方向を独立に制御するためです。

「仕様は `contracts/`、束縛は使う側」という規則の実例です —— 契約は 1 箇所で集中管理し、
そこから生成した束縛（サーバのスケルトンとクライアント）は、それを使う側の近くに置きます。

## なぜ `contexts/` の外にいるのか

**偶然ではなく、`.golangci.yml` の depguard が要求する配置です。**

注文コンテキストは在庫コンテキストの Go パッケージを import してはいけません
（`contexts/ordering/**` から `contexts/inventory` への依存を depguard が deny しています）。
これは、コンテキスト間の結合を「相手の Go の型」ではなく「翻訳された契約」に限るための、
最も鋭い一行の規則です。

もし生成クライアントを `contexts/inventory/client/` に置くと、注文がそれを import した瞬間に
この規則の**例外**が要る（あるいは規則そのものが緩む）ことになります。`contexts/` の外に
出しておけば、例外は 1 つも要りません。注文が import するのは
`clients/inventory/invclient`（在庫の**契約**から生成した wire 型）であって、在庫の
ドメイン型ではないからです。

同じ理由で、このディレクトリを `shared/` の下へ移すこともできません。`.golangci.yml` の
`shared-purity` rule が `.../clients` を名指しで deny しています（`shared/` は
ドメイン非依存の汎用機構だけを置く場所であり、特定コンテキストの契約から生成した
クライアントはそこに属しません）。

## 新しいクライアントを足すとき

1. 相手コンテキストの内部 OpenAPI 契約（`contracts/api/<peer>/internal.openapi.yaml`）と、
   クライアント生成用の ogen 設定（`contracts/api/<peer>/client.ogen.yaml`）を用意します。
2. `clients/<peer>/` に Go モジュールを作り、`generate.go` に `go:generate` の 1 行を書きます。
3. `go.work` の `use` と、`Makefile` の `MODULES` / `GEN_MODULES` に追加します。
4. `make generate` で生成し、生成物をコミットします。
