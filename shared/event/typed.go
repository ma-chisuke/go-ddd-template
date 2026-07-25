package event

// このファイルは event.go の型なし機構（InProcess）を、各境界づけられたコンテキストの
// ドメインイベント型で扱えるようにする「型付きファサード」を提供する。
//
// なぜ必要か: ドメイン層は純粋さを保つため、コンテキストごとに独自の DomainEvent 型を
// 定義する（shared/event を import しない）。その結果、両コンテキストのアプリケーション層は
// 「型だけが違う」同一の適合ラッパを各 75 行ずつ手書きしていた。差がドメインイベント型
// だけなら、その差は型パラメータで表現できる。generics がこの重複を解く。
//
// import の向きは一方向のまま保たれる。shared/event は contexts/ を import せず
// （depguard rule shared-purity が機械的に強制する）、ドメイン層も shared/event を
// import しない。双方向に import が無いまま型が噛み合うのが、Go の構造的型付けを
// 活かした境界設計である。

import (
	"context"
	"log/slog"
	"time"
)

// Occurred は「種別名と発生時刻を持つイベント」を表す型制約。
//
// Event（EventName のみ）に OccurredAt を加えた合成である。Event だけでは足りないのは、
// 配信ログが発生時刻（occurred_at）を記録するために OccurredAt() を呼ぶからで、
// 型制約は実際に使うメソッドの合計でなければならない。
//
// 各コンテキストの DomainEvent はこの 2 メソッドを持つため、そのまま型引数に渡せる
// （この型制約を import する必要はない = 構造的に満たす）。
type Occurred interface {
	Event
	OccurredAt() time.Time
}

// Sink は種別を問わず全イベントを受け取る「捕捉ハンドラ」。
// テストでの記録やログ的な用途に使う。永続化成功後の後処理の一部なので、
// エラーを返さない（返しても巻き戻せる相手がいない）。
type Sink[E Occurred] func(ctx context.Context, e E)

// TypedHandler は種別名で購読するハンドラ。ドメイン型 E をそのまま受け取る。
// 型なし機構の Handler と違い、購読側で event.Event からの型アサーションを書く必要はない
// （アサーションは On が内部で 1 回だけ行う）。
type TypedHandler[E Occurred] func(ctx context.Context, e E) error

// Typed は E 型のドメインイベントを扱うプロセス内同期ディスパッチャ。
//
// 2 つの配信経路を束ねる:
//   - sinks（捕捉ハンドラ）… 種別を問わず全イベントへ渡す。
//   - inner（型なし InProcess 機構）… 種別名ごとに登録されたハンドラへ渡す。
//
// E は各コンテキストのドメインイベント型（order.DomainEvent など）であり、EventName() を
// 持つので Event を構造的に満たす。この適合をここで一度だけ行うことで、ドメイン層を
// shared/event に依存させずに汎用の配信機構へ載せられる。
//
// この機構はコンテキストを横断しない。クロスコンテキストへの伝播は、呼び出し側の
// ユースケースが同一トランザクション内で翻訳済み契約へ変換し、アウトボックス
// （shared/outbox）へ積むことで行う。
type Typed[E Occurred] struct {
	log   *slog.Logger
	sinks []Sink[E]
	inner *InProcess
}

// NewTyped は E 型のドメインイベントを配信するディスパッチャを生成する。
// sinks は種別を問わず全イベントを受け取る捕捉ハンドラで、0 個でよい。
func NewTyped[E Occurred](log *slog.Logger, sinks ...Sink[E]) *Typed[E] {
	return &Typed[E]{log: log, sinks: sinks, inner: NewInProcess()}
}

// On は種別名 name に対する購読ハンドラを登録する。同一種別に複数登録できる。
// ハンドラはドメイン型 E を受け取る（型アサーションは内部で 1 回だけ行う）。
func (d *Typed[E]) On(name string, h TypedHandler[E]) {
	d.inner.Register(name, func(ctx context.Context, e Event) error {
		// 同一ファサードは自分の inner にしか配信しないため te は常に成立するが、
		// panic しない ok 形式で安全側に倒す。
		te, ok := e.(E)
		if !ok {
			return nil
		}
		return h(ctx, te)
	})
}

// Dispatch は各イベントを配信の事実とともに構造化ログに記録し、捕捉ハンドラと
// 型なし機構（種別名で登録されたハンドラ）の双方へ届ける。
//
// これは永続化成功後に呼ばれる「後処理」なので、ハンドラのエラーは呼び出し元へ返さず
// ログに残すに留める（コミット済みのトランザクションは巻き戻せないため）。
// エラーを返さないこの形は各コンテキストの EventDispatcher ポートの契約であり、
// generics 化にあたっても変更しない。
func (d *Typed[E]) Dispatch(ctx context.Context, events ...E) {
	for _, e := range events {
		d.log.InfoContext(ctx, "ドメインイベントを配信しました",
			slog.String("event", e.EventName()),
			slog.Time("occurred_at", e.OccurredAt()),
		)
		for _, sink := range d.sinks {
			sink(ctx, e)
		}
	}

	// E は Occurred を通じて Event を満たすため、そのまま []Event へ適合できる。
	adapted := make([]Event, len(events))
	for i, e := range events {
		adapted[i] = e
	}
	if err := d.inner.Dispatch(ctx, adapted...); err != nil {
		d.log.WarnContext(ctx, "イベントハンドラがエラーを返しました", "error", err)
	}
}
