package application_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/example/go-ddd-template/contexts/ordering/internal/adapter/outbound/memory"
	"github.com/example/go-ddd-template/contexts/ordering/internal/application"
	"github.com/example/go-ddd-template/contexts/ordering/internal/domain/order"
	"github.com/example/go-ddd-template/contexts/ordering/port"
	"github.com/example/go-ddd-template/shared/outbox"
	"github.com/example/go-ddd-template/shared/uow"
)

func testLogger() *slog.Logger { return slog.New(slog.NewJSONHandler(io.Discard, nil)) }

// fakeReserver は StockReserver の差し替え。呼び出し回数と最後の引数を記録し、
// 注入したエラーを返す（ACL の HTTP 実装は aclhttp のテストで別途検証する）。
type fakeReserver struct {
	reserveErr   error
	releaseErr   error
	reserveCalls int
	releaseCalls int
	lastRef      string
	lastLines    []port.ReserveLine
}

func (f *fakeReserver) Reserve(_ context.Context, ref string, lines []port.ReserveLine) error {
	f.reserveCalls++
	f.lastRef = ref
	f.lastLines = lines
	return f.reserveErr
}

func (f *fakeReserver) Release(_ context.Context, _ string) error {
	f.releaseCalls++
	return f.releaseErr
}

// failingUoW は常に指定エラーを返す作業単位（保存失敗の再現用）。
type failingUoW struct{ err error }

func (u *failingUoW) Within(_ context.Context, _ func(ctx context.Context, r application.Repos) error) error {
	return u.err
}

// flakyUoW は最初の failsLeft 回だけ ErrConcurrencyConflict を注入する UoW デコレータ。
type flakyUoW struct {
	inner     application.UnitOfWork
	failsLeft int
}

func (f *flakyUoW) Within(ctx context.Context, fn func(ctx context.Context, r application.Repos) error) error {
	return f.inner.Within(ctx, func(ctx context.Context, r application.Repos) error {
		if f.failsLeft > 0 {
			f.failsLeft--
			return uow.ErrConcurrencyConflict
		}
		return fn(ctx, r)
	})
}

// fixture は注文ユースケース一式をインメモリアダプタで組み立てる。
type fixture struct {
	place    *application.PlaceOrder
	get      *application.GetOrder
	cancel   *application.CancelOrder
	store    *memory.Store
	obx      *memory.OutboxStore
	captured *[]order.DomainEvent
}

func newFixture(t *testing.T, reserver application.StockReserver, work application.UnitOfWork, store *memory.Store, obx *memory.OutboxStore) fixture {
	t.Helper()
	exec := uow.NewExecutor(uow.WithBaseBackoff(0))
	log := testLogger()
	captured := &[]order.DomainEvent{}
	dispatcher := application.NewInProcessDispatcher(log, func(_ context.Context, e order.DomainEvent) {
		*captured = append(*captured, e)
	})
	return fixture{
		place:    application.NewPlaceOrder(exec, work, reserver, dispatcher, log),
		get:      application.NewGetOrder(memory.NewReadOrderStore(store), log),
		cancel:   application.NewCancelOrder(exec, work, log),
		store:    store,
		obx:      obx,
		captured: captured,
	}
}

// newMemoryFixture はインメモリの UoW で fixture を組み立てる（最も一般的な構成）。
func newMemoryFixture(t *testing.T, reserver application.StockReserver) fixture {
	t.Helper()
	store := memory.NewStore()
	obx := memory.NewOutboxStore()
	return newFixture(t, reserver, memory.NewUnitOfWork(store, obx), store, obx)
}

func sampleInput() application.PlaceOrderInput {
	return application.PlaceOrderInput{
		CustomerID: "CUST-1",
		Lines: []application.PlaceOrderLine{
			{SKU: "SKU-A", Quantity: 3, UnitPriceAmount: 1200, Currency: "JPY"},
		},
	}
}

func filterByType(msgs []outbox.Message, msgType string) []outbox.Message {
	var out []outbox.Message
	for _, m := range msgs {
		if m.Type == msgType {
			out = append(out, m)
		}
	}
	return out
}

// decodeReservationRef は在庫側の購読ポリシと同一の構造体で payload をデコードする。
// これにより「注文側が生む payload が、在庫側がデコードできる契約に一致する」ことを検証する。
func decodeReservationRef(t *testing.T, payload []byte) string {
	t.Helper()
	var p struct {
		ReservationRef string `json:"reservation_ref"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		t.Fatalf("payload のデコードに失敗しました: %v", err)
	}
	return p.ReservationRef
}

func countEvents(events []order.DomainEvent, name string) int {
	n := 0
	for _, e := range events {
		if e.EventName() == name {
			n++
		}
	}
	return n
}

func TestPlaceOrder_Happy(t *testing.T) {
	ctx := context.Background()
	reserver := &fakeReserver{}
	f := newMemoryFixture(t, reserver)

	id, err := f.place.Handle(ctx, sampleInput())
	if err != nil {
		t.Fatalf("想定外のエラー: %v", err)
	}

	// フェーズ 1: 予約が 1 回、翻訳済み DTO で呼ばれている。
	if reserver.reserveCalls != 1 {
		t.Fatalf("Reserve 呼び出し回数 = %d, want 1", reserver.reserveCalls)
	}
	if reserver.lastRef != id.String() {
		t.Fatalf("予約参照 = %q, want %q", reserver.lastRef, id.String())
	}
	if len(reserver.lastLines) != 1 || reserver.lastLines[0].SKU != "SKU-A" || reserver.lastLines[0].Qty != 3 {
		t.Fatalf("翻訳された予約行が不正: %+v", reserver.lastLines)
	}

	// フェーズ 2: 注文が Confirmed で保存されている。
	view, err := f.get.Handle(ctx, id.String())
	if err != nil {
		t.Fatalf("照会失敗: %v", err)
	}
	if view.Status != "confirmed" || view.Version != 1 {
		t.Fatalf("保存後の注文が不正: %+v", view)
	}
	if view.TotalAmount != 3600 || view.TotalCurrency != "JPY" {
		t.Fatalf("合計が不正: %d %s", view.TotalAmount, view.TotalCurrency)
	}

	// 同一 tx で ConfirmReservation コマンドが outbox に積まれている。
	confirms := filterByType(f.obx.Messages(), application.MessageTypeConfirmReservation)
	if len(confirms) != 1 {
		t.Fatalf("ConfirmReservation 件数 = %d, want 1", len(confirms))
	}
	if ref := decodeReservationRef(t, confirms[0].Payload); ref != id.String() {
		t.Fatalf("ConfirmReservation の reservation_ref = %q, want %q", ref, id.String())
	}

	// OrderPlaced はコミット後にプロセス内配信されている。
	if got := countEvents(*f.captured, "ordering.order_placed"); got != 1 {
		t.Fatalf("OrderPlaced 配信件数 = %d, want 1", got)
	}
}

func TestPlaceOrder_InsufficientStockRejected(t *testing.T) {
	ctx := context.Background()
	reserver := &fakeReserver{reserveErr: application.ErrReservationRejected}
	f := newMemoryFixture(t, reserver)

	_, err := f.place.Handle(ctx, sampleInput())
	if !errors.Is(err, application.ErrReservationRejected) {
		t.Fatalf("エラー = %v, want ErrReservationRejected", err)
	}
	// 注文は保存されず、コマンドも積まれていない（予約失敗は tx の前）。
	if len(f.obx.Messages()) != 0 {
		t.Fatalf("予約拒否時にメッセージが積まれた: %d 件", len(f.obx.Messages()))
	}
	// 予約失敗時は補償解放を呼ばない（そもそも予約が成立していない）。
	if reserver.releaseCalls != 0 {
		t.Fatalf("予約拒否時に解放が呼ばれた: %d 回", reserver.releaseCalls)
	}
}

func TestPlaceOrder_ReserveUnavailable(t *testing.T) {
	ctx := context.Background()
	// aclhttp が不達（timeout / 5xx）を翻訳したときの形（両番兵に一致）を再現する。
	reserver := &fakeReserver{
		reserveErr: errors.Join(application.ErrReservationRejected, application.ErrReservationUnavailable),
	}
	f := newMemoryFixture(t, reserver)

	_, err := f.place.Handle(ctx, sampleInput())
	if !errors.Is(err, application.ErrReservationUnavailable) {
		t.Fatalf("エラー = %v, want ErrReservationUnavailable", err)
	}
	if len(f.obx.Messages()) != 0 {
		t.Fatalf("不達時にメッセージが積まれた: %d 件", len(f.obx.Messages()))
	}
}

func TestPlaceOrder_SaveFailureReleasesCompensating(t *testing.T) {
	ctx := context.Background()
	reserver := &fakeReserver{} // 予約は成功する
	store := memory.NewStore()
	obx := memory.NewOutboxStore()
	// 保存（フェーズ 2）が必ず失敗する UoW。
	f := newFixture(t, reserver, &failingUoW{err: errors.New("DB 書き込み失敗")}, store, obx)

	_, err := f.place.Handle(ctx, sampleInput())
	if err == nil {
		t.Fatalf("保存失敗が伝播していない")
	}
	if reserver.reserveCalls != 1 {
		t.Fatalf("Reserve 呼び出し回数 = %d, want 1", reserver.reserveCalls)
	}
	// 保存失敗時は best-effort な補償解放を試みる。
	if reserver.releaseCalls != 1 {
		t.Fatalf("補償解放の呼び出し回数 = %d, want 1", reserver.releaseCalls)
	}
}

func TestPlaceOrder_RetriesOnConflictReserveOnce(t *testing.T) {
	ctx := context.Background()
	reserver := &fakeReserver{}
	store := memory.NewStore()
	obx := memory.NewOutboxStore()
	flaky := &flakyUoW{inner: memory.NewUnitOfWork(store, obx), failsLeft: 1}
	f := newFixture(t, reserver, flaky, store, obx)

	id, err := f.place.Handle(ctx, sampleInput())
	if err != nil {
		t.Fatalf("再試行後も失敗: %v", err)
	}
	// UoW は再試行されたが、ACL の予約は tx の外なので 1 回だけ呼ばれる。
	if reserver.reserveCalls != 1 {
		t.Fatalf("Reserve が再試行された: %d 回, want 1", reserver.reserveCalls)
	}
	if flaky.failsLeft != 0 {
		t.Fatalf("衝突注入が消費されていない: failsLeft=%d", flaky.failsLeft)
	}
	// ロールバック分は破棄され、最終的に ConfirmReservation は 1 件だけ。
	if got := len(filterByType(obx.Messages(), application.MessageTypeConfirmReservation)); got != 1 {
		t.Fatalf("ConfirmReservation 件数 = %d, want 1", got)
	}
	view, _ := f.get.Handle(ctx, id.String())
	if view.Version != 1 {
		t.Fatalf("保存後の version = %d, want 1", view.Version)
	}
}
