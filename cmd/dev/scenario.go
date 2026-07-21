package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
)

// scenarioResult はデモシナリオの観測結果（テストの検証にも使う）。
type scenarioResult struct {
	orderID             string
	placedStatus        string // 作成後の注文状態（confirmed を期待）
	reservedAfterPlace  int    // 作成後の在庫の引当済み数（予約 + 確定 = 3 を期待）
	cancelledStatus     string // 取消後の注文状態（cancelled を期待）
	reservedAfterCancel int    // 取消後の在庫の引当済み数（解放されて 0 を期待）
	rejectedStatusCode  int    // 在庫不足の注文作成が返す HTTP ステータス（409 を期待）
}

// 各シナリオで用いる SKU と数量（デモ用の固定値）。
const (
	demoSKU          = "WIDGET-001"
	demoReplenishQty = 10
	demoOrderQty     = 3

	scarceSKU          = "GADGET-001"
	scarceReplenishQty = 2  // 在庫不足を起こすため少なく補充する
	scarceOrderQty     = 10 // 補充量を上回る注文（拒否される）
)

// runScenario は両コンテキストの公開 API を、実際の HTTP（httptest サーバ）越しに駆動して
// seam を端から端まで通す。ネットワークは in-process の httptest ループバックであり、
// 予約・確定・取消・解放はすべて同期的・決定的に完結する。
//
// 流れ:
//  1. SKU を補充する（在庫）。
//  2. 注文を作成する（在庫を同期予約 → 成功で Confirmed → ConfirmReservation が在庫へ届き pending → confirmed）。
//  3. 注文を照会する。
//  4. 注文を取り消す（OrderCancelled が在庫へ届き、予約が解放される）。
//  5. 在庫不足の SKU へ注文する（拒否される = 409）。
func runScenario(ctx context.Context, log *slog.Logger, h *harness) (scenarioResult, error) {
	invSrv := httptest.NewServer(h.inventoryHandler())
	defer invSrv.Close()
	ordSrv := httptest.NewServer(h.orderingHandler())
	defer ordSrv.Close()

	c := &apiClient{http: invSrv.Client(), invBase: invSrv.URL, ordBase: ordSrv.URL}
	var res scenarioResult

	// 1) 在庫を補充する。
	stock, err := c.replenish(ctx, demoSKU, demoReplenishQty)
	if err != nil {
		return res, err
	}
	log.InfoContext(ctx, "在庫を補充しました", slog.String("sku", stock.SKU),
		slog.Int("available", stock.Available), slog.Int("reserved", stock.Reserved))

	// 2) 注文を作成する（在庫を同期予約 → Confirmed。ConfirmReservation が在庫へ同期配送される）。
	order, status, err := c.placeOrder(ctx, demoSKU, demoOrderQty)
	if err != nil {
		return res, err
	}
	if status != http.StatusCreated {
		return res, fmt.Errorf("注文作成が想定外のステータスを返しました: %d", status)
	}
	res.orderID = order.ID
	res.placedStatus = order.Status
	log.InfoContext(ctx, "注文を作成しました（在庫を予約し確定しました）",
		slog.String("order_id", order.ID), slog.String("status", order.Status),
		slog.String("reservation_ref", order.ReservationRef))

	// 作成後の在庫を照会する（予約 + 確定で reserved が増えている）。
	stock, err = c.getStock(ctx, demoSKU)
	if err != nil {
		return res, err
	}
	res.reservedAfterPlace = stock.Reserved
	log.InfoContext(ctx, "在庫を照会しました（作成後）", slog.String("sku", stock.SKU),
		slog.Int("available", stock.Available), slog.Int("reserved", stock.Reserved))

	// 3) 注文を照会する。
	got, err := c.getOrder(ctx, order.ID)
	if err != nil {
		return res, err
	}
	log.InfoContext(ctx, "注文を照会しました", slog.String("order_id", got.ID), slog.String("status", got.Status))

	// 4) 注文を取り消す（OrderCancelled が在庫へ同期配送され、予約が解放される）。
	cancelled, err := c.cancelOrder(ctx, order.ID)
	if err != nil {
		return res, err
	}
	res.cancelledStatus = cancelled.Status
	log.InfoContext(ctx, "注文を取り消しました（在庫の予約が解放されます）",
		slog.String("order_id", cancelled.ID), slog.String("status", cancelled.Status))

	// 取消後の在庫を照会する（解放されて reserved が 0 に戻る）。
	stock, err = c.getStock(ctx, demoSKU)
	if err != nil {
		return res, err
	}
	res.reservedAfterCancel = stock.Reserved
	log.InfoContext(ctx, "在庫を照会しました（取消後）", slog.String("sku", stock.SKU),
		slog.Int("available", stock.Available), slog.Int("reserved", stock.Reserved))

	// 5) 在庫不足の SKU へ注文する（拒否される = 409）。
	if _, err := c.replenish(ctx, scarceSKU, scarceReplenishQty); err != nil {
		return res, err
	}
	_, rejectStatus, err := c.placeOrder(ctx, scarceSKU, scarceOrderQty)
	if err != nil {
		return res, err
	}
	res.rejectedStatusCode = rejectStatus
	log.InfoContext(ctx, "在庫不足の注文は拒否されました",
		slog.String("sku", scarceSKU), slog.Int("status", rejectStatus))

	return res, nil
}

// apiClient は両コンテキストの公開 API を叩く薄い HTTP クライアント。
type apiClient struct {
	http    *http.Client
	invBase string
	ordBase string
}

// --- 契約の JSON 表現（公開 OpenAPI に対応する最小の型） ---

type stockView struct {
	SKU       string `json:"sku"`
	Available int    `json:"available"`
	Reserved  int    `json:"reserved"`
	Version   int    `json:"version"`
}

type money struct {
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
}

type placeOrderLine struct {
	Sku       string `json:"sku"`
	Quantity  int    `json:"quantity"`
	UnitPrice money  `json:"unitPrice"`
}

type placeOrderRequest struct {
	CustomerID string           `json:"customerId"`
	Lines      []placeOrderLine `json:"lines"`
}

type orderView struct {
	ID             string `json:"id"`
	CustomerID     string `json:"customerId"`
	Status         string `json:"status"`
	ReservationRef string `json:"reservationRef"`
	Version        int    `json:"version"`
}

func (c *apiClient) replenish(ctx context.Context, sku string, qty int) (stockView, error) {
	var out stockView
	body := map[string]int{"quantity": qty}
	status, err := c.do(ctx, http.MethodPost, c.invBase+"/stock/"+sku+"/replenish", body, &out)
	if err != nil {
		return out, err
	}
	if status != http.StatusOK {
		return out, fmt.Errorf("在庫補充が想定外のステータスを返しました: %d", status)
	}
	return out, nil
}

func (c *apiClient) getStock(ctx context.Context, sku string) (stockView, error) {
	var out stockView
	status, err := c.do(ctx, http.MethodGet, c.invBase+"/stock/"+sku, nil, &out)
	if err != nil {
		return out, err
	}
	if status != http.StatusOK {
		return out, fmt.Errorf("在庫照会が想定外のステータスを返しました: %d", status)
	}
	return out, nil
}

// placeOrder は注文を作成し、注文ビューと HTTP ステータスを返す。ステータスは呼び出し側が
// 検証する（成功 201 / 在庫不足 409 のどちらも正常系として扱いたいため）。
func (c *apiClient) placeOrder(ctx context.Context, sku string, qty int) (orderView, int, error) {
	var out orderView
	req := placeOrderRequest{
		CustomerID: "CUST-1",
		Lines: []placeOrderLine{{
			Sku:       sku,
			Quantity:  qty,
			UnitPrice: money{Amount: 1200, Currency: "JPY"},
		}},
	}
	status, err := c.do(ctx, http.MethodPost, c.ordBase+"/orders", req, &out)
	if err != nil {
		return out, status, err
	}
	return out, status, nil
}

func (c *apiClient) getOrder(ctx context.Context, id string) (orderView, error) {
	var out orderView
	status, err := c.do(ctx, http.MethodGet, c.ordBase+"/orders/"+id, nil, &out)
	if err != nil {
		return out, err
	}
	if status != http.StatusOK {
		return out, fmt.Errorf("注文照会が想定外のステータスを返しました: %d", status)
	}
	return out, nil
}

func (c *apiClient) cancelOrder(ctx context.Context, id string) (orderView, error) {
	var out orderView
	status, err := c.do(ctx, http.MethodPost, c.ordBase+"/orders/"+id+"/cancel", nil, &out)
	if err != nil {
		return out, err
	}
	if status != http.StatusOK {
		return out, fmt.Errorf("注文取消が想定外のステータスを返しました: %d", status)
	}
	return out, nil
}

// do は 1 回の HTTP 呼び出しを行い、2xx のときだけ out へ JSON をデコードする。戻り値の
// ステータスは呼び出し側が検証できるよう常に返す（エラー応答 = problem+json はデコードしない）。
func (c *apiClient) do(ctx context.Context, method, url string, in, out any) (int, error) {
	var reqBody io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return 0, fmt.Errorf("リクエストの JSON 変換に失敗しました: %w", err)
		}
		reqBody = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return 0, fmt.Errorf("リクエストの生成に失敗しました: %w", err)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("HTTP 呼び出しに失敗しました: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, fmt.Errorf("応答本文の読み取りに失敗しました: %w", err)
	}
	if out != nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if err := json.Unmarshal(payload, out); err != nil {
			return resp.StatusCode, fmt.Errorf("応答の JSON 解釈に失敗しました: %w", err)
		}
	}
	return resp.StatusCode, nil
}
