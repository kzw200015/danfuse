package background

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

// start 在后台运行 l.Run，返回取消循环的函数，以及循环返回时关闭的 channel。
func start(l *Loop, tick <-chan time.Time, onTick func(ctx context.Context)) (stop context.CancelFunc, done <-chan struct{}) {
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan struct{})
	go func() {
		defer close(ch)
		l.Run(ctx, tick, onTick)
	}()
	return cancel, ch
}

// TestCallRunsOnLoop Call 的 fn 拿到的是循环的 ctx，不是请求的；它的返回值原样交给调用方。
func TestCallRunsOnLoop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := NewLoop()
		stop, done := start(l, nil, nil)

		reqCtx, cancelReq := context.WithCancel(t.Context())
		want := errors.New("busy")
		err := l.Call(reqCtx, func(ctx context.Context) error {
			cancelReq()
			if ctx.Err() != nil {
				t.Error("fn 拿到的应是循环的 ctx，请求取消不影响它")
			}
			return want
		})
		if !errors.Is(err, want) {
			t.Errorf("Call = %v, want %v", err, want)
		}

		stop()
		<-done
	})
}

// TestRunWaitsForSpawned 定时触发里起的任务在 ctx 取消后收尾，Run 等它结束才返回。
func TestRunWaitsForSpawned(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := NewLoop()
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		finished := false
		stop, done := start(l, ticker.C, func(ctx context.Context) {
			l.Spawn(func() {
				<-ctx.Done()
				time.Sleep(time.Second) // 收尾
				finished = true
			})
		})

		time.Sleep(time.Minute)
		synctest.Wait()
		stop()
		<-done
		if !finished {
			t.Error("Run 应等后台任务结束才返回")
		}
	})
}

// TestCallAfterStop 循环返回之后 Call 立即返回 ErrStopped；循环没在处理时，请求的 ctx 取消就返回。
func TestCallAfterStop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := NewLoop()
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		if err := l.Call(ctx, func(context.Context) error { return nil }); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("循环没有运行时 Call = %v, want DeadlineExceeded", err)
		}

		stop, done := start(l, nil, nil)
		stop()
		<-done
		if err := l.Call(t.Context(), func(context.Context) error { return nil }); !errors.Is(err, ErrStopped) {
			t.Errorf("循环返回后 Call = %v, want ErrStopped", err)
		}
	})
}
