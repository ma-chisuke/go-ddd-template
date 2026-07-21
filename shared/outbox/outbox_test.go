package outbox_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
	require.NoError(t, err, "想定外のエラー")
	assert.Equal(t, 2, sent, "送信件数")
	assert.Len(t, pub.sent, 2, "Publisher へ渡った件数")

	// 2 回目は全て published 済みなので 0 件。
	sent, err = runner.RunOnce(ctx)
	require.NoError(t, err, "想定外のエラー")
	assert.Equal(t, 0, sent, "2 回目の送信件数(既に送信済み)")
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
	require.Error(t, err, "送出失敗時はエラーを返すべき")
	assert.Equal(t, 1, sent, "失敗前に送信できた件数")

	// 失敗した bad は未送信のまま残り、次回に再送されうる(at-least-once)。
	remaining, err := store.Unpublished(ctx, 10)
	require.NoError(t, err, "Unpublished 失敗")
	require.Len(t, remaining, 1, "未送信の残り件数")
	assert.Equal(t, "bad", remaining[0].ID, "未送信の残りは bad であるべき")
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
	require.NoError(t, err, "想定外のエラー")
	assert.Equal(t, "hello", delivered, "配送されたペイロード")
}

func TestRouter_DeliverUnknownTypeReturnsErrNoRoute(t *testing.T) {
	ctx := context.Background()
	router := outbox.NewRouter()
	// 未登録種別は ErrNoRoute。黙って捨てない。
	err := router.Deliver(ctx, outbox.Message{Type: "unknown"})
	require.ErrorIs(t, err, outbox.ErrNoRoute, "エラーは ErrNoRoute であるべき")
}
