// Package outbox はトランザクショナルアウトボックス(transactional outbox)の機構を提供する。
//
// 目的は「集約の永続化」と「外部へのメッセージ送信」を確実に整合させることである。
// 素朴に「DB に保存してからメッセージブローカーへ publish する」と、片方だけ成功して
// もう片方が失敗する二重書き込み問題が起きる。アウトボックスはこれを避けるために、
// 送信したいメッセージを集約の書き込みと同一トランザクションで outbox テーブルへ書き込み
// (Enqueue)、別の中継プロセス(Runner)が未送信分をポーリングして送出する。
//
// このパッケージはドメインにもコンテキスト固有コードにも依存しない汎用機構である。
// 具体的な MessageStore(自スキーマの outbox テーブル)は各コンテキストの送信アダプタが実装し、
// トランスポート(Publisher)も配置側が差し替える。
//
// 関心の分離: outbox は「一時的な配送キュー」であり、送信に成功した行は削除される
// (delete-after-publish)。発行したメッセージの恒久的な履歴は、Enqueue と同一
// トランザクションで書かれる events テーブル(追記専用の恒久イベントログ)が担う。
// これにより配送キューは肥大化せず、監査に必要な履歴は失われない。
//
// 送信側(Runner)は at-least-once（少なくとも1回）で配送する。Publish 成功後・MarkPublished
// 前にクラッシュすると同じメッセージが再送されうるため、受信側の Consumer は必ず冪等に書く。
package outbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// ErrNoRoute は、受信した message_type に対応する Consumer が Router に登録されて
// いないことを表すセンチネルエラー。未登録種別を黙って捨てる(silent drop)代わりに
// これを返し、扱い(ログ／再試行拒否／デッドレター)はトランスポート側に委ねる。
var ErrNoRoute = errors.New("outbox: メッセージ種別に対応する経路がありません")

// Message はコンテキスト間・プロセス間で運ぶ中立なメッセージ封筒。
//
// Type(message_type)が送信先スキーマと受信側 Consumer を選択する。Type は
// ドメインイベント(例: 注文取消)とアプリケーション発行のクロスコンテキストコマンド
// (例: 予約確定要求)の双方を運べる中立な種別である。Payload は「翻訳済み契約」の
// シリアライズであり、コンテキスト間で Go の共有型を渡さない。TraceID は seam を跨ぐ
// 相関 ID で、フロー全体をサービス横断で追跡できるようにする。
type Message struct {
	// ID はメッセージの一意な識別子(冪等な MarkPublished/受信側重複排除に使う)。
	ID string
	// Type は message_type。スキーマと Consumer を選択する。
	Type string
	// Payload は翻訳済み契約のシリアライズ(例: JSON バイト列)。
	Payload []byte
	// TraceID は相関 ID。seam を跨いで伝播する。
	TraceID string
	// OccurredAt はメッセージの発生時刻(UTC)。
	OccurredAt time.Time
}

// MessageStore はコンテキストが自スキーマの outbox テーブルに対して実装する送信側ストア。
// Enqueue は集約書き込みと同一トランザクション(UoW)で呼ばれ、二重書き込みを避ける。
//
// outbox は「一時的な配送キュー」である。送信に成功した行は残さず削除するため、
// outbox には常に「まだ送っていないもの」だけが存在する。何を発行したかの恒久的な
// 履歴は、Enqueue と同一トランザクションで書かれる events テーブル(恒久イベントログ)が
// 担う。配送(Runner)は events を読まない。
type MessageStore interface {
	// Enqueue はメッセージを未送信状態で outbox に積む。UoW の内側で呼ぶこと。
	// 実装は同一トランザクションで恒久イベントログ(events)にも記録する。
	Enqueue(ctx context.Context, m Message) error
	// Unpublished は未送信(未 publish)のメッセージを最大 limit 件返す。
	// 送信済みの行は削除されているため、outbox に残っている行はすべて未送信である。
	Unpublished(ctx context.Context, limit int) ([]Message, error)
	// MarkPublished は指定 ID のメッセージを配送キューから取り除く(=行を削除する)。
	// 送信済みフラグを立てるのではなく行そのものを消す点に注意すること。
	// 恒久的な発行履歴は events テーブルに残るため、ここで削除しても記録は失われない。
	MarkPublished(ctx context.Context, ids ...string) error
}

// Publisher は実際にメッセージを外部トランスポートへ送出する送信ポート。
// 本番ではブローカーや HTTP push、開発用にはピアへ直接届ける同期実装などに差し替える。
type Publisher interface {
	Publish(ctx context.Context, m Message) error
}

// Runner は送信側の中継ループ。MessageStore の未送信分をポーリングし、Publisher で
// 送出してから MarkPublished する。at-least-once の本番経路を担う。
type Runner struct {
	store    MessageStore
	pub      Publisher
	interval time.Duration
	batch    int
	log      *slog.Logger
}

// RunnerOption は Runner の設定を変更する関数オプション。
type RunnerOption func(*Runner)

// WithInterval はポーリング間隔を設定する。
func WithInterval(d time.Duration) RunnerOption {
	return func(r *Runner) {
		if d > 0 {
			r.interval = d
		}
	}
}

// WithBatch は 1 回のポーリングで処理する最大件数を設定する。
func WithBatch(n int) RunnerOption {
	return func(r *Runner) {
		if n > 0 {
			r.batch = n
		}
	}
}

// NewRunner は中継ループを生成する。既定は 1 秒間隔・1 回あたり最大 100 件。
func NewRunner(store MessageStore, pub Publisher, log *slog.Logger, opts ...RunnerOption) *Runner {
	r := &Runner{
		store:    store,
		pub:      pub,
		interval: time.Second,
		batch:    100,
		log:      log,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Run はポーリングループを ctx がキャンセルされるまで回す。各周期で未送信分を送出する。
// 送出に失敗しても次の周期で再試行するため、この関数はポーリング中の一時的失敗では
// 抜けない(ctx.Err() のときだけ抜ける)。
func (r *Runner) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if _, err := r.RunOnce(ctx); err != nil {
				// 一時的失敗は次周期に再送されるため、ログに残して継続する(at-least-once)。
				r.log.WarnContext(ctx, "アウトボックス中継の周期でエラーが発生しました", "error", err)
			}
		}
	}
}

// RunOnce は 1 回だけ未送信分を送出する。テストや起動直後の即時ドレインに使う。
// 戻り値は今回送信できた件数。ある 1 件の送出が失敗したら、その時点で中断して
// 件数とエラーを返す(残りは次回に再試行される)。
//
// 順序 Unpublished → Publish → MarkPublished(=削除) は at-least-once の要である。
// 送出に成功した後にのみ配送キューから消すため、Publish と削除の間でクラッシュしても
// 行は残り、次のポーリングで再送される。この順序は変えてはならない。
func (r *Runner) RunOnce(ctx context.Context) (int, error) {
	msgs, err := r.store.Unpublished(ctx, r.batch)
	if err != nil {
		return 0, fmt.Errorf("未送信メッセージの取得に失敗しました: %w", err)
	}
	sent := 0
	for _, m := range msgs {
		if err := r.pub.Publish(ctx, m); err != nil {
			return sent, fmt.Errorf("メッセージ %q の送出に失敗しました: %w", m.ID, err)
		}
		if err := r.store.MarkPublished(ctx, m.ID); err != nil {
			return sent, fmt.Errorf("メッセージ %q の配送キューからの削除に失敗しました: %w", m.ID, err)
		}
		sent++
	}
	return sent, nil
}

// Consumer は受信側でメッセージを 1 件処理する関数。at-least-once ゆえ冪等に実装する。
type Consumer func(ctx context.Context, m Message) error

// Router は受信側で message_type から Consumer を解決するディスパッチャ。
//
// 注意: この「outbox Router(メッセージ種別ディスパッチ)」は、HTTP のルーティングを行う
// 「internal HTTP router(トランスポート)」とは別物である。トランスポートが受信した
// メッセージをデコードして Deliver を呼び、Router が種別に応じた Consumer へ振り分ける。
type Router struct {
	mu     sync.RWMutex
	routes map[string]Consumer
}

// NewRouter は空のルーターを生成する。
func NewRouter() *Router {
	return &Router{routes: make(map[string]Consumer)}
}

// Register は message_type に対する Consumer を登録する。同一種別の再登録は上書きする。
func (r *Router) Register(msgType string, c Consumer) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.routes[msgType] = c
}

// Deliver はメッセージを、その Type に登録された Consumer へ委譲する。
// 未登録種別に対しては ErrNoRoute を返す(黙って捨てない)。
func (r *Router) Deliver(ctx context.Context, m Message) error {
	r.mu.RLock()
	c, ok := r.routes[m.Type]
	r.mu.RUnlock()
	if !ok {
		return fmt.Errorf("メッセージ種別 %q: %w", m.Type, ErrNoRoute)
	}
	return c(ctx, m)
}
