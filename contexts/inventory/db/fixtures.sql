-- 在庫コンテキストの dev/test 専用フィクスチャ（fixtures）。
--
-- 重要: このファイルは開発・デモ・テスト環境でのみ適用する（APPLY_FIXTURES=true のとき）。
-- 本番の適用経路には決して含めない（本番参照データは seed.sql に分離）。ここには、
-- 「デモをすぐ試せるようにするための都合の良い初期状態」を置く。
--
-- 冪等性: ON CONFLICT DO UPDATE で、再適用してもデモ用の在庫数へ収束する。

-- デモ用に、カタログ SKU の在庫を補充した状態にしておく（すぐ注文を試せる）。
INSERT INTO inventory.stock_items (id, sku, available, version)
VALUES ('fixture-widget-001', 'WIDGET-001', 100, 1)
ON CONFLICT (sku) DO UPDATE SET available = EXCLUDED.available;

-- デモ専用の追加 SKU（本番カタログには無い）。
INSERT INTO inventory.stock_items (id, sku, available, version)
VALUES ('fixture-demo-sku', 'DEMO-SKU-001', 25, 1)
ON CONFLICT (sku) DO UPDATE SET available = EXCLUDED.available;
