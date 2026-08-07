-- sqlc の入力となる SQL 定義。ここから型安全な Go コードを生成する。
-- 生成物はコミットし、手で編集しない。クエリを変えたいときはこの SQL を編集して再生成する。

-- name: GetOrderByID :one
-- ID で注文を 1 件取得する。存在しなければ pgx.ErrNoRows が返る。
SELECT id, customer_id, status, total_amount, total_currency, reservation_ref, version
FROM ordering.orders
WHERE id = $1;

-- name: InsertOrder :exec
-- 注文を新規挿入する（version は 1 から始まる）。
INSERT INTO ordering.orders (id, customer_id, status, total_amount, total_currency, reservation_ref, version)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: UpdateOrder :execrows
-- 楽観的排他制御つきの更新。期待バージョンが一致する行だけを更新し、影響行数を返す。
-- 0 行なら版が食い違っている（＝衝突）ことを意味する。注文の可変部分は状態のみ。
UPDATE ordering.orders
SET status = $1, version = $2, updated_at = now()
WHERE id = $3 AND version = $4;

-- name: InsertOrderLine :exec
-- 注文明細を 1 件挿入する。
INSERT INTO ordering.order_lines (order_id, line_no, sku, quantity, unit_price, currency)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: ListOrderLines :many
-- 注文が保持する明細を行番号順に取得する。
SELECT order_id, line_no, sku, quantity, unit_price, currency
FROM ordering.order_lines
WHERE order_id = $1
ORDER BY line_no ASC;

-- name: GetShipmentByID :one
-- ID で出荷を 1 件取得する。存在しなければ pgx.ErrNoRows が返る。
SELECT id, order_id, status, tracking_number, version
FROM ordering.shipments
WHERE id = $1;

-- name: InsertShipment :exec
-- 出荷を新規挿入する（version は 1 から始まる）。
INSERT INTO ordering.shipments (id, order_id, status, tracking_number, version)
VALUES ($1, $2, $3, $4, $5);

-- name: UpdateShipment :execrows
-- 楽観的排他制御つきの更新。期待バージョンが一致する行だけを更新し、影響行数を返す。
-- 0 行なら版が食い違っている（＝衝突）ことを意味する。出荷の可変部分は状態と追跡番号のみ。
UPDATE ordering.shipments
SET status = $1, tracking_number = $2, version = $3, updated_at = now()
WHERE id = $4 AND version = $5;

-- name: InsertOutboxMessage :exec
-- アウトボックス（一時的な配送キュー）へメッセージを積む。
-- 集約書き込み・InsertEvent と同一トランザクションで実行する。
INSERT INTO ordering.outbox (id, message_type, payload, trace_id, occurred_at)
VALUES ($1, $2, $3, $4, $5);

-- name: InsertEvent :exec
-- 恒久イベントログへ発行メッセージを 1 件記録する（recorded_at は既定値 now()）。
-- InsertOutboxMessage と同一トランザクションで実行し、原子的に確定させる。
INSERT INTO ordering.events (id, message_type, payload, trace_id, occurred_at)
VALUES ($1, $2, $3, $4, $5);

-- name: ListUnpublishedOutbox :many
-- 未送信のメッセージを occurred_at 昇順で最大 $1 件取得する。
-- 送信済みの行は削除されるため、この表に残っている行はすべて未送信である。
SELECT id, message_type, payload, trace_id, occurred_at
FROM ordering.outbox
ORDER BY occurred_at ASC
LIMIT $1;

-- name: MarkOutboxPublished :exec
-- 送信に成功した ID の行を配送キューから削除する（delete-after-publish）。
-- 発行履歴は events テーブルに残るため、ここで削除しても記録は失われない。
DELETE FROM ordering.outbox
WHERE id = $1;
