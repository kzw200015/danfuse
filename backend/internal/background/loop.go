// Package background 后台循环：给 service 的 Run 用，定时触发和手动触发都在同一个循环里处理，
// 后台任务用循环的 ctx（应用级），循环返回前等它们结束。
//
// 手动触发经 Call 交给循环执行，不在 HTTP 请求的 goroutine 里直接起任务：起任务（Spawn）和等任务都在循环的 goroutine 里，
// 循环返回之后不会再有新任务；任务用的也因此是循环的 ctx，不是请求的 ctx。
// 用它的是同步（catalog.SyncService）和季绑定的补建（seasonbinding.Service）。
package background

import (
	"context"
	"sync"
	"time"

	"github.com/kzw200015/danfuse/backend/internal/httpx/apierr"
)

// ErrStopped 循环已经返回、服务正在关闭时，Call 返回它（503）。
var ErrStopped = apierr.ErrServiceUnavailable.WithMessage("服务正在关闭")

type Loop struct {
	calls   chan call
	stopped chan struct{}  // Run 返回时关闭，之后的 Call 不再等它
	wg      sync.WaitGroup // 进行中的后台任务
}

type call struct {
	fn    func(ctx context.Context) error
	reply chan error
}

func NewLoop() *Loop {
	return &Loop{calls: make(chan call), stopped: make(chan struct{})}
}

// Run 阻塞到 ctx 取消，返回前等 Spawn 起的任务都结束。只能调用一次。
// tick 每来一次调用 onTick，tick 为 nil 时没有定时触发。onTick 和 Call 的 fn 在循环里依次执行，拿到的是循环的 ctx；
// 耗时的事用 Spawn 放到后台。
func (l *Loop) Run(ctx context.Context, tick <-chan time.Time, onTick func(ctx context.Context)) {
	defer close(l.stopped)
	for {
		select {
		case <-ctx.Done():
			l.wg.Wait()
			return
		case <-tick:
			onTick(ctx)
		case c := <-l.calls:
			c.reply <- c.fn(ctx)
		}
	}
}

// Call 请循环执行 fn 并等它返回，fn 拿到的是循环的 ctx。循环已经返回（服务正在关闭）时返回 ErrStopped，
// 不拖住优雅关闭；ctx（请求的）先取消时返回它的错误。
func (l *Loop) Call(ctx context.Context, fn func(ctx context.Context) error) error {
	c := call{fn: fn, reply: make(chan error, 1)}
	select {
	case l.calls <- c:
	case <-l.stopped:
		return ErrStopped
	case <-ctx.Done():
		return ctx.Err()
	}
	return <-c.reply
}

// Spawn 起一个后台任务，Run 返回前等它结束。只在 onTick 和 Call 的 fn 里调用。
func (l *Loop) Spawn(fn func()) {
	l.wg.Go(fn)
}
