-- 在庫コンテキストの宣言的スキーマ定義。
-- これは「あるべき最終状態」を宣言する DDL であり、sqldef によって実際の DB へ適用する。
-- 各境界づけられたコンテキストは自分専用のスキーマ（論理的な DB）を持ち、
-- 他コンテキストのスキーマを直接読み書きしない（schema-per-context）。

CREATE SCHEMA IF NOT EXISTS inventory;

-- 在庫項目テーブル。集約 StockItem の永続化先。
-- available は「自由に予約できる」利用可能在庫（予約分は差し引き済み）。
-- 予約分（reserved）は stock_reservations から導出するため、ここには持たない。
CREATE TABLE IF NOT EXISTS inventory.stock_items (
    -- 集約の識別子（アプリケーションが採番する不透明な ID）
    id         text        NOT NULL PRIMARY KEY,
    -- 在庫識別子（SKU）。一意。
    sku        text        NOT NULL UNIQUE,
    -- 利用可能在庫数。非負であることを CHECK 制約でも二重に守る。
    available  integer     NOT NULL CHECK (available >= 0),
    -- 楽観的排他制御のためのバージョン番号。永続化済みは 1 以上。
    version    integer     NOT NULL CHECK (version >= 1),
    -- 監査用のタイムスタンプ。
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- 予約テーブル。集約 StockItem が保持する Reservation の永続化先。
-- 1 つの在庫項目につき、1 つの予約参照（ref）は高々 1 つの有効な予約に対応する
-- （主キーが (stock_item_id, ref)）。マルチ SKU 予約では同一 ref が複数の在庫項目に
-- 跨るため、ref を跨いだ検索（LoadByReservation）用の索引を張る。
CREATE TABLE IF NOT EXISTS inventory.stock_reservations (
    -- 予約を保持する在庫項目の ID。
    stock_item_id text        NOT NULL REFERENCES inventory.stock_items(id) ON DELETE CASCADE,
    -- 予約参照（呼び出し側供給の不透明な相関 ID）。
    ref           text        NOT NULL,
    -- 予約数量（1 以上）。
    quantity      integer     NOT NULL CHECK (quantity >= 1),
    -- 予約状態（pending / confirmed）。
    status        text        NOT NULL CHECK (status = 'pending' OR status = 'confirmed'),
    -- 失効時刻。pending のときのみ有効（confirmed では NULL）。
    expires_at    timestamptz,
    PRIMARY KEY (stock_item_id, ref)
);

-- 予約参照での横断検索（Confirm / Release のマルチ SKU ロード）用の索引。
CREATE INDEX IF NOT EXISTS idx_stock_reservations_ref
    ON inventory.stock_reservations (ref);

-- 期限切れ pending の掃除（Reaper）用の部分索引。
CREATE INDEX IF NOT EXISTS idx_stock_reservations_pending_expiry
    ON inventory.stock_reservations (expires_at)
    WHERE status = 'pending';

-- アウトボックステーブル。集約書き込みと同一トランザクションで積まれる送信メッセージ。
-- 送信中継（Runner）が published_at IS NULL の行をポーリングして送出し、published_at を刻む。
CREATE TABLE IF NOT EXISTS inventory.outbox (
    -- メッセージの一意な識別子。
    id           text        NOT NULL PRIMARY KEY,
    -- message_type（送信先スキーマと受信側 Consumer を選択する中立な種別）。
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
CREATE INDEX IF NOT EXISTS idx_outbox_unpublished
    ON inventory.outbox (occurred_at)
    WHERE published_at IS NULL;
