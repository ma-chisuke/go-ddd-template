-- 在庫コンテキストの「本番参照データ」（production reference data）。冪等な upsert。
--
-- ここには、本番を含む全環境に投入してよい参照データ（既知の商品カタログなど）だけを置く。
-- dev/test 専用のデモデータは fixtures.sql に分離し、本番経路には決して混ぜない
-- （seed は常に適用、fixtures は APPLY_FIXTURES=true のときだけ適用）。
--
-- 冪等性: ON CONFLICT DO NOTHING により、再適用しても重複や上書きが起きない。
-- 実行時の在庫数（available）は運用（補充）で決まる値なので、参照データとしては 0 とする
-- （「カタログに存在する SKU」を宣言するだけ）。

-- 既知の商品カタログ（参照データ）。version は永続化済みを表す 1。
INSERT INTO inventory.stock_items (id, sku, available, version)
VALUES
    ('seed-widget-001', 'WIDGET-001', 0, 1),
    ('seed-gadget-001', 'GADGET-001', 0, 1)
ON CONFLICT (sku) DO NOTHING;
