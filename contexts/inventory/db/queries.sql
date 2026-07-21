-- sqlc の入力となる SQL 定義。ここから型安全な Go コードを生成する。
-- 生成物はコミットし、手で編集しない。クエリを変えたいときはこの SQL を編集して再生成する。

-- name: GetStockItemBySKU :one
-- SKU で在庫項目を 1 件取得する。存在しなければ pgx.ErrNoRows が返る。
SELECT id, sku, available, version
FROM inventory.stock_items
WHERE sku = $1;

-- name: InsertStockItem :exec
-- 在庫項目を新規挿入する（version は 1 から始まる）。
INSERT INTO inventory.stock_items (id, sku, available, version)
VALUES ($1, $2, $3, $4);

-- name: UpdateStockItem :execrows
-- 楽観的排他制御つきの更新。期待バージョンが一致する行だけを更新し、
-- 影響行数を返す。0 行なら版が食い違っている（＝衝突）ことを意味する。
UPDATE inventory.stock_items
SET available = $1, version = $2, updated_at = now()
WHERE sku = $3 AND version = $4;
