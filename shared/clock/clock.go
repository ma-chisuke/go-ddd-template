// Package clock は現在時刻を供給する仕組みを提供する。
//
// 時刻は外部世界の状態であり、ドメインやアプリケーション層が直接触れると
// 「その日その時刻でしか再現しないテスト」を生む。各コンテキストは自分の言葉で
// 時刻のポート（例: 在庫コンテキストの application.Clock）を宣言し、本番は実時計
// System を、テストは手で進める時計 Manual を注入する。どちらの型も Now() time.Time を
// 持つだけなので、Go の構造的型付けによって明示的な実装宣言なしにポートを満たす。
//
// 機構は共有し、型はコンテキスト固有に保つ — event.Typed[E] が各コンテキストの
// EventDispatcher ポートを満たすのと同じ境界の引き方である。
package clock

import (
	"sync"
	"time"
)

// System は実時間（UTC）を返す時計。本番の合成ルートで注入する。
type System struct{}

// Now は現在の UTC 時刻を返す。
func (System) Now() time.Time { return time.Now().UTC() }

// Manual は現在時刻を人為的に制御できる、手で進める時計。並行アクセスに耐えるよう
// mutex で守る。reaper のような時間依存処理を決定的にテストするために使う。
type Manual struct {
	mu  sync.Mutex
	now time.Time
}

// NewManual は指定時刻を「現在」とする、手で進める時計を生成する。
func NewManual(now time.Time) *Manual {
	return &Manual{now: now}
}

// Now は手で進める時計の現在時刻を返す。実時間とは無関係で、Advance/Set でのみ進む。
func (c *Manual) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance は時計を d だけ進める。
func (c *Manual) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// Set は時計の現在時刻を t に設定する。
func (c *Manual) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}
