-- 注文コンテキストの宣言的スキーマ定義。
-- これは「あるべき最終状態」を宣言する DDL であり、sqldef などで実際の DB へ適用する。
-- 各境界づけられたコンテキストは自分専用のスキーマ（論理的な DB）を持ち、
-- 他コンテキストのスキーマを直接読み書きしない（schema-per-context）。

CREATE SCHEMA IF NOT EXISTS ordering;

-- 注文テーブル。集約 Order の永続化先。
CREATE TABLE IF NOT EXISTS ordering.orders (
    -- 集約の識別子（アプリケーションが採番する不透明な ID）。
    id              text        NOT NULL PRIMARY KEY,
    -- 注文者（顧客）の識別子。
    customer_id     text        NOT NULL,
    -- 注文状態（confirmed / cancelled）。
    status          text        NOT NULL CHECK (status = 'confirmed' OR status = 'cancelled'),
    -- 合計金額（最小通貨単位）。非負であることを CHECK 制約でも二重に守る。
    total_amount    bigint      NOT NULL CHECK (total_amount >= 0),
    -- 合計金額の通貨コード（ISO-4217）。
    total_currency  text        NOT NULL,
    -- 在庫予約に用いた予約参照（相関 ID）。取消時の解放を駆動する。
    reservation_ref text        NOT NULL,
    -- 楽観的排他制御のためのバージョン番号。永続化済みは 1 以上。
    version         integer     NOT NULL CHECK (version >= 1),
    -- 監査用のタイムスタンプ。
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

-- 注文明細テーブル。集約 Order が保持する OrderLine の永続化先。
-- line_no で行の並び順を保持する（作成後は不変）。
CREATE TABLE IF NOT EXISTS ordering.order_lines (
    -- 明細を保持する注文の ID。
    order_id   text    NOT NULL REFERENCES ordering.orders(id) ON DELETE CASCADE,
    -- 明細の行番号（0 始まり、並び順の保持用）。
    line_no    integer NOT NULL,
    -- 在庫識別子（SKU）。
    sku        text    NOT NULL,
    -- 注文数量（1 以上）。
    quantity   integer NOT NULL CHECK (quantity >= 1),
    -- 単価（最小通貨単位）。非負。
    unit_price bigint  NOT NULL CHECK (unit_price >= 0),
    -- 単価の通貨コード（ISO-4217）。
    currency   text    NOT NULL,
    PRIMARY KEY (order_id, line_no)
);

-- アウトボックステーブル。集約書き込みと同一トランザクションで積まれる送信メッセージ。
-- 送信中継（Runner）が published_at IS NULL の行をポーリングして送出し、published_at を刻む。
CREATE TABLE IF NOT EXISTS ordering.outbox (
    -- メッセージの一意な識別子。
    id           text        NOT NULL PRIMARY KEY,
    -- message_type（送信先の Consumer を選択する中立な種別）。
    message_type text        NOT NULL,
    -- 翻訳済み契約のシリアライズ（共有 Go 型は渡さない）。
    payload      bytea       NOT NULL,
    -- seam を跨ぐ相関 ID。
    trace_id     text        NOT NULL DEFAULT '',
    -- メッセージ発生時刻。
    occurred_at  timestamptz NOT NULL,
    -- 送信済み時刻。NULL なら未送信。
    published_at timestamptz
);

-- 未送信メッセージのポーリング用の部分索引。
CREATE INDEX IF NOT EXISTS idx_ordering_outbox_unpublished
    ON ordering.outbox (occurred_at)
    WHERE published_at IS NULL;
