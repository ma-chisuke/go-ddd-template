// Package event はプロセス内のドメインイベント配信機構を提供する。
//
// 重要な設計方針として、ドメイン層はこのパッケージを import しない。ドメイン層は
// 各境界づけられたコンテキストで独自の DomainEvent 型を定義し(純粋なドメインを保つため)、
// アプリケーション層がその DomainEvent を本パッケージの Event へ適合させて配信する。
// Event が要求するのは名前(EventName)だけなので、EventName() string を持つ任意の
// ドメインイベント型は構造的に Event を満たし、余計な依存なしに適合できる。
//
// この機構はコンテキストを横断しない。クロスコンテキストへの伝播は outbox パッケージ
// 経由の「翻訳済み契約」で行い、ドメイン型をそのまま外へ渡さない。
package event

import (
	"context"
	"sync"
)

// Event は配信対象のマーカーインターフェース。安定した種別名だけを要求する。
type Event interface {
	// EventName はイベント種別を表す安定した名前を返す。振り分けやログ出力に使う。
	EventName() string
}

// Handler は特定種別のイベントを受け取る購読ハンドラ。
// 同期実行され、エラーを返すと Dispatch はそのエラーを呼び出し元へ伝播する。
type Handler func(ctx context.Context, e Event) error

// Dispatcher はハンドラ登録(Register)と同期配信(Dispatch)を提供するポート。
// 実装はプロセス内・同期。より強い配信保証(永続化・再送)が必要な経路は outbox を使う。
type Dispatcher interface {
	// Register は種別名 name に対する購読ハンドラを登録する。
	Register(name string, h Handler)
	// Dispatch は各イベントを、その EventName に登録されたハンドラへ順に配信する。
	// いずれかのハンドラがエラーを返したら、その時点で中断してエラーを返す。
	Dispatch(ctx context.Context, events ...Event) error
}

// InProcess は Dispatcher のプロセス内・同期実装。
// 種別名ごとに 0 個以上のハンドラを保持し、Dispatch でそれらを順に呼び出す。
// 並行アクセスを mutex で守る。
type InProcess struct {
	mu       sync.RWMutex
	handlers map[string][]Handler
}

// NewInProcess は空のディスパッチャを生成する。
func NewInProcess() *InProcess {
	return &InProcess{handlers: make(map[string][]Handler)}
}

// コンパイル時にポートを満たしていることを確認する。
var _ Dispatcher = (*InProcess)(nil)

// Register は種別名に対するハンドラを追加する。同一種別に複数登録できる。
func (d *InProcess) Register(name string, h Handler) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.handlers[name] = append(d.handlers[name], h)
}

// Dispatch は各イベントを、その種別に登録されたハンドラへ同期配信する。
// 登録の無い種別は素通りする(購読者がいないことは正常)。ハンドラが最初にエラーを
// 返した時点で配信を中断し、そのエラーを返す。
func (d *InProcess) Dispatch(ctx context.Context, events ...Event) error {
	for _, e := range events {
		d.mu.RLock()
		hs := d.handlers[e.EventName()]
		d.mu.RUnlock()
		for _, h := range hs {
			if err := h(ctx, e); err != nil {
				return err
			}
		}
	}
	return nil
}
