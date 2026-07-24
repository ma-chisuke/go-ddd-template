-- sqlc の入力となる SQL 定義。ここから型安全な Go コードを生成する。
-- 生成物はコミットし、手で編集しない。クエリを変えたいときはこの SQL を編集して再生成する。

-- name: GetStockItemBySKU :one
-- SKU で在庫項目を 1 件取得する。存在しなければ pgx.ErrNoRows が返る。
SELECT id, sku, available, version
FROM inventory.stock_items
WHERE sku = $1;

-- name: GetStockItemByID :one
-- ID で在庫項目を 1 件取得する（予約参照や期限切れ検索で得た ID から復元する用）。
SELECT id, sku, available, version
FROM inventory.stock_items
WHERE id = $1;

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

-- name: ListReservationsByStockItem :many
-- 在庫項目が保持する予約の一覧を取得する。
SELECT stock_item_id, ref, quantity, status, expires_at
FROM inventory.stock_reservations
WHERE stock_item_id = $1;

-- name: ListStockItemIDsByReservationRef :many
-- 指定の予約参照を持つ在庫項目の ID 一覧を取得する（Confirm / Release のマルチ SKU ロード）。
SELECT DISTINCT stock_item_id
FROM inventory.stock_reservations
WHERE ref = $1;

-- name: ListExpiredPendingStockItemIDs :many
-- before 時点で期限切れの pending 予約を持つ在庫項目の ID 一覧を取得する（Reaper）。
SELECT DISTINCT stock_item_id
FROM inventory.stock_reservations
WHERE status = 'pending' AND expires_at IS NOT NULL AND expires_at < $1
LIMIT $2;

-- name: DeleteReservationsByStockItem :exec
-- 在庫項目の予約を全削除する（集約の予約状態を「削除して入れ直す」スナップショット保存の前段）。
DELETE FROM inventory.stock_reservations
WHERE stock_item_id = $1;

-- name: InsertReservation :exec
-- 予約を 1 件挿入する。
INSERT INTO inventory.stock_reservations (stock_item_id, ref, quantity, status, expires_at)
VALUES ($1, $2, $3, $4, $5);

-- name: InsertOutboxMessage :exec
-- アウトボックス（一時的な配送キュー）へメッセージを積む。
-- 集約書き込み・InsertEvent と同一トランザクションで実行する。
INSERT INTO inventory.outbox (id, message_type, payload, trace_id, occurred_at)
VALUES ($1, $2, $3, $4, $5);

-- name: InsertEvent :exec
-- 恒久イベントログへ発行メッセージを 1 件記録する（recorded_at は既定値 now()）。
-- InsertOutboxMessage と同一トランザクションで実行し、原子的に確定させる。
INSERT INTO inventory.events (id, message_type, payload, trace_id, occurred_at)
VALUES ($1, $2, $3, $4, $5);

-- name: ListUnpublishedOutbox :many
-- 未送信のメッセージを occurred_at 昇順で最大 $1 件取得する。
-- 送信済みの行は削除されるため、この表に残っている行はすべて未送信である。
SELECT id, message_type, payload, trace_id, occurred_at
FROM inventory.outbox
ORDER BY occurred_at ASC
LIMIT $1;

-- name: MarkOutboxPublished :exec
-- 送信に成功した ID の行を配送キューから削除する（delete-after-publish）。
-- 発行履歴は events テーブルに残るため、ここで削除しても記録は失われない。
DELETE FROM inventory.outbox
WHERE id = $1;
