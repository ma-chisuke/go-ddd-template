-- 在庫コンテキストの宣言的スキーマ定義。
-- これは「あるべき最終状態」を宣言する DDL であり、sqldef によって実際の DB へ適用する。
-- 各境界づけられたコンテキストは自分専用のスキーマ（論理的な DB）を持ち、
-- 他コンテキストのスキーマを直接読み書きしない（schema-per-context）。

CREATE SCHEMA IF NOT EXISTS inventory;

-- 在庫項目テーブル。集約 StockItem の永続化先。
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
