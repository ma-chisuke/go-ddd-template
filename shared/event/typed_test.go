package event_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/go-ddd-template/shared/event"
)

// testDomainEvent はテスト用の「コンテキスト固有ドメインイベント型」。
//
// 実際の各コンテキストの domain.DomainEvent と同じ形（EventName + OccurredAt）を、
// shared/event を import しない独立した interface として定義する。これがそのまま
// event.NewTyped の型引数へ渡せることが、構造的型付けによる境界設計の実証である。
type testDomainEvent interface {
	EventName() string
	OccurredAt() time.Time
}

// placed / cancelled はテスト用の 2 種のドメインイベント（種別名で振り分けられる）。
type placed struct {
	orderID string
	at      time.Time
}

func (placed) EventName() string       { return "test.placed" }
func (e placed) OccurredAt() time.Time { return e.at }

type cancelled struct {
	orderID string
	at      time.Time
}

func (cancelled) EventName() string       { return "test.cancelled" }
func (e cancelled) OccurredAt() time.Time { return e.at }

// discardLogger は出力を捨てる構造化ロガー。
func discardLogger() *slog.Logger { return slog.New(slog.NewJSONHandler(io.Discard, nil)) }

// bufLogger はログを検査するための、バッファへ書く構造化ロガー。
func bufLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, nil))
}

func TestTyped_DispatchCallsSinksForEveryEvent(t *testing.T) {
	ctx := context.Background()
	var buf bytes.Buffer

	// 捕捉ハンドラ（Sink）は種別を問わず全イベントを受け取る。
	var captured []testDomainEvent
	d := event.NewTyped[testDomainEvent](bufLogger(&buf), func(_ context.Context, e testDomainEvent) {
		captured = append(captured, e)
	})

	at := time.Date(2026, 7, 25, 10, 0, 0, 0, time.UTC)
	d.Dispatch(ctx, placed{orderID: "O-1", at: at}, cancelled{orderID: "O-1", at: at})

	require.Len(t, captured, 2, "捕捉ハンドラは全イベントを受け取るべき")
	assert.Equal(t, "test.placed", captured[0].EventName(), "1 件目の種別")
	assert.Equal(t, "test.cancelled", captured[1].EventName(), "2 件目の種別")

	// 配信ログは種別（event）と発生時刻（occurred_at）を添える（運用の観測点）。
	logged := buf.String()
	assert.Contains(t, logged, "ドメインイベントを配信しました", "配信ログのメッセージ")
	assert.Contains(t, logged, `"event":"test.placed"`, "配信ログの event フィールド")
	assert.Contains(t, logged, `"occurred_at":`, "配信ログの occurred_at フィールド")
}

func TestTyped_OnHandlerReceivesDomainType(t *testing.T) {
	ctx := context.Background()
	d := event.NewTyped[testDomainEvent](discardLogger())

	// 購読ハンドラはドメイン型（testDomainEvent）で受け取る。型なし event.Event ではないので、
	// 購読側で型アサーションを書く必要がない（アサーションは On が内部で 1 回だけ行う）。
	var got []string
	d.On("test.placed", func(_ context.Context, e testDomainEvent) error {
		// 具体型へ到達できることまで確認する（インタフェース越しでも情報が失われていない）。
		p, ok := e.(placed)
		require.True(t, ok, "購読ハンドラは具体型 placed を受け取るべき")
		got = append(got, p.orderID)
		return nil
	})

	d.Dispatch(ctx,
		placed{orderID: "O-1", at: time.Now()},
		cancelled{orderID: "O-2", at: time.Now()}, // 別種別なのでこのハンドラには届かない
		placed{orderID: "O-3", at: time.Now()},
	)

	assert.Equal(t, []string{"O-1", "O-3"}, got, "登録した種別のイベントだけが届くべき")
}

func TestTyped_UnregisteredNameIsNoop(t *testing.T) {
	ctx := context.Background()
	d := event.NewTyped[testDomainEvent](discardLogger())

	// 購読者のいない種別は素通りする（購読者がいないことは正常）。
	// Dispatch は戻り値を持たないため、panic しないことが素通りの観測になる。
	assert.NotPanics(t, func() {
		d.Dispatch(ctx, placed{orderID: "O-1", at: time.Now()})
	}, "未登録種別の配信は何も起こさないべき")
}

func TestTyped_HandlerErrorIsLoggedNotReturned(t *testing.T) {
	ctx := context.Background()
	var buf bytes.Buffer

	// 捕捉ハンドラは、購読ハンドラのエラーに巻き込まれず呼ばれ続ける。
	sinkCalls := 0
	d := event.NewTyped[testDomainEvent](bufLogger(&buf), func(_ context.Context, _ testDomainEvent) {
		sinkCalls++
	})

	sentinel := errors.New("ハンドラ失敗")
	handlerCalls := 0
	d.On("test.placed", func(_ context.Context, _ testDomainEvent) error {
		handlerCalls++
		return sentinel
	})

	// Dispatch は永続化成功後の「後処理」なので戻り値を持たない（I-1）。
	// エラーを返す署名に変えると、コミット済みトランザクションを巻き戻せない
	// 呼び出し元にエラー処理を強いることになる。
	d.Dispatch(ctx, placed{orderID: "O-1", at: time.Now()})

	assert.Equal(t, 1, handlerCalls, "購読ハンドラは呼ばれるべき")
	assert.Equal(t, 1, sinkCalls, "捕捉ハンドラも呼ばれるべき")
	// エラーは呼び出し元へ返らないが、黙って捨てられるのでもない（警告ログに残る）。
	assert.Contains(t, buf.String(), "イベントハンドラがエラーを返しました",
		"ハンドラのエラーは警告ログに残すべき")
}
