package service

import (
	"context"
	"sync"
	"time"
)

// backgroundLoop 后台循环的公共部分（SyncService、SeasonBindingService 的 Run）：定时触发和手动触发都在同一个循环里处理，
// 后台任务用循环的 ctx（应用级），循环返回前等它们结束。
//
// 手动触发经 call 交给循环执行，不在 HTTP 请求的 goroutine 里直接起任务：起任务（spawn）和等任务都在循环的 goroutine 里，
// 循环返回之后不会再有新任务；任务用的也因此是循环的 ctx，不是请求的 ctx。
type backgroundLoop struct {
	calls   chan loopCall
	stopped chan struct{}  // run 返回时关闭，之后的 call 不再等它
	wg      sync.WaitGroup // 进行中的后台任务
}

type loopCall struct {
	fn    func(ctx context.Context) error
	reply chan error
}

func newBackgroundLoop() *backgroundLoop {
	return &backgroundLoop{calls: make(chan loopCall), stopped: make(chan struct{})}
}

// run 阻塞到 ctx 取消，返回前等 spawn 起的任务都结束。只能调用一次。
// tick 每来一次调用 onTick，tick 为 nil 时没有定时触发。onTick 和 call 的 fn 在循环里依次执行，拿到的是循环的 ctx；
// 耗时的事用 spawn 放到后台。
func (l *backgroundLoop) run(ctx context.Context, tick <-chan time.Time, onTick func(ctx context.Context)) {
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

// call 请循环执行 fn 并等它返回，fn 拿到的是循环的 ctx。循环已经返回（服务正在关闭）时返回 errShuttingDown，
// 不拖住优雅关闭；ctx（请求的）先取消时返回它的错误。
func (l *backgroundLoop) call(ctx context.Context, fn func(ctx context.Context) error) error {
	c := loopCall{fn: fn, reply: make(chan error, 1)}
	select {
	case l.calls <- c:
	case <-l.stopped:
		return errShuttingDown
	case <-ctx.Done():
		return ctx.Err()
	}
	return <-c.reply
}

// spawn 起一个后台任务，run 返回前等它结束。只在 onTick 和 call 的 fn 里调用。
func (l *backgroundLoop) spawn(fn func()) {
	l.wg.Go(fn)
}
