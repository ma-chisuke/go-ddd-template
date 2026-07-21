package outbox_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/example/go-ddd-template/shared/outbox"
)

func testLogger() *slog.Logger { return slog.New(slog.NewJSONHandler(io.Discard, nil)) }

// fakeStore は MessageStore のインメモリ実装(テスト用)。
type fakeStore struct {
	msgs      []outbox.Message
	published map[string]bool
}

func newFakeStore(msgs ...outbox.Message) *fakeStore {
	return &fakeStore{msgs: msgs, published: make(map[string]bool)}
}

func (s *fakeStore) Enqueue(_ context.Context, m outbox.Message) error {
	s.msgs = append(s.msgs, m)
	return nil
}

func (s *fakeStore) Unpublished(_ context.Context, limit int) ([]outbox.Message, error) {
	var out []outbox.Message
	for _, m := range s.msgs {
		if s.published[m.ID] {
			continue
		}
		out = append(out, m)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (s *fakeStore) MarkPublished(_ context.Context, ids ...string) error {
	for _, id := range ids {
		s.published[id] = true
	}
	return nil
}

// fakePublisher は送出したメッセージを記録する。failOn に一致する ID で失敗する。
type fakePublisher struct {
	sent   []outbox.Message
	failOn string
}

func (p *fakePublisher) Publish(_ context.Context, m outbox.Message) error {
	if m.ID == p.failOn {
		return errors.New("送出失敗")
	}
	p.sent = append(p.sent, m)
	return nil
}

func TestRunner_RunOncePublishesAndMarks(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore(
		outbox.Message{ID: "m1", Type: "t", OccurredAt: time.Now().UTC()},
		outbox.Message{ID: "m2", Type: "t", OccurredAt: time.Now().UTC()},
	)
	pub := &fakePublisher{}
	runner := outbox.NewRunner(store, pub, testLogger(), outbox.WithBatch(10))

	sent, err := runner.RunOnce(ctx)
	if err != nil {
		t.Fatalf("想定外のエラー: %v", err)
	}
	if sent != 2 {
		t.Fatalf("送信件数 = %d, want 2", sent)
	}
	if len(pub.sent) != 2 {
		t.Fatalf("Publisher へ渡った件数 = %d, want 2", len(pub.sent))
	}

	// 2 回目は全て published 済みなので 0 件。
	sent, err = runner.RunOnce(ctx)
	if err != nil {
		t.Fatalf("想定外のエラー: %v", err)
	}
	if sent != 0 {
		t.Fatalf("2 回目の送信件数 = %d, want 0(既に送信済み)", sent)
	}
}

func TestRunner_PublishFailureLeavesUnpublished(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore(
		outbox.Message{ID: "ok", Type: "t"},
		outbox.Message{ID: "bad", Type: "t"},
	)
	pub := &fakePublisher{failOn: "bad"}
	runner := outbox.NewRunner(store, pub, testLogger(), outbox.WithBatch(10))

	// 1 件目は成功、2 件目で失敗して中断する。
	sent, err := runner.RunOnce(ctx)
	if err == nil {
		t.Fatal("送出失敗時はエラーを返すべき")
	}
	if sent != 1 {
		t.Fatalf("失敗前に送信できた件数 = %d, want 1", sent)
	}

	// 失敗した bad は未送信のまま残り、次回に再送されうる(at-least-once)。
	remaining, err := store.Unpublished(ctx, 10)
	if err != nil {
		t.Fatalf("Unpublished 失敗: %v", err)
	}
	if len(remaining) != 1 || remaining[0].ID != "bad" {
		t.Fatalf("未送信の残りが不正: %+v", remaining)
	}
}

func TestRouter_DeliverRoutesByType(t *testing.T) {
	ctx := context.Background()
	router := outbox.NewRouter()

	var delivered string
	router.Register("greeting", func(_ context.Context, m outbox.Message) error {
		delivered = string(m.Payload)
		return nil
	})

	err := router.Deliver(ctx, outbox.Message{Type: "greeting", Payload: []byte("hello")})
	if err != nil {
		t.Fatalf("想定外のエラー: %v", err)
	}
	if delivered != "hello" {
		t.Fatalf("配送されたペイロード = %q, want hello", delivered)
	}
}

func TestRouter_DeliverUnknownTypeReturnsErrNoRoute(t *testing.T) {
	ctx := context.Background()
	router := outbox.NewRouter()
	// 未登録種別は ErrNoRoute。黙って捨てない。
	err := router.Deliver(ctx, outbox.Message{Type: "unknown"})
	if !errors.Is(err, outbox.ErrNoRoute) {
		t.Fatalf("エラー = %v, want ErrNoRoute", err)
	}
}
