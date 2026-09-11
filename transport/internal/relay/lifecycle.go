package relay

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"time"
)

var errRelayTimeout = errors.New("upstream header or transfer inactivity timeout")

// activityDeadline can change phases without leaving a stale timer callback
// able to cancel a request that has since made progress.
type activityDeadline struct {
	mu       sync.Mutex
	timer    *time.Timer
	deadline time.Time
	stopped  bool
	cancel   context.CancelCauseFunc
}

func newActivityDeadline(cancel context.CancelCauseFunc) *activityDeadline {
	return &activityDeadline{cancel: cancel}
}

func (watch *activityDeadline) reset(timeout time.Duration) {
	watch.mu.Lock()
	defer watch.mu.Unlock()
	if watch.stopped {
		return
	}
	watch.deadline = time.Now().Add(timeout)
	if watch.timer == nil {
		watch.timer = time.AfterFunc(timeout, watch.expire)
	} else {
		watch.timer.Reset(timeout)
	}
}

func (watch *activityDeadline) expire() {
	watch.mu.Lock()
	defer watch.mu.Unlock()
	if watch.stopped {
		return
	}
	if remaining := time.Until(watch.deadline); remaining > 0 {
		watch.timer.Reset(remaining)
		return
	}
	watch.stopped = true
	watch.cancel(errRelayTimeout)
}

func (watch *activityDeadline) stop() {
	watch.mu.Lock()
	defer watch.mu.Unlock()
	watch.stopped = true
	if watch.timer != nil {
		watch.timer.Stop()
	}
}

type progressReader struct {
	reader    io.Reader
	watch     *activityDeadline
	timeout   time.Duration
	remaining int64
	done      chan struct{}
	once      sync.Once
}

func (reader *progressReader) Read(buffer []byte) (int, error) {
	reader.watch.reset(reader.timeout)
	n, err := reader.reader.Read(buffer)
	if n > 0 {
		reader.watch.reset(reader.timeout)
	}
	if reader.remaining >= 0 {
		reader.remaining -= int64(n)
	}
	if reader.done != nil && (err != nil || reader.remaining == 0) {
		reader.once.Do(func() { close(reader.done) })
	}
	return n, err
}

type progressBody struct {
	*progressReader
	io.Closer
}

type idleWriter struct {
	net.Conn
	timeout time.Duration
}

func (writer idleWriter) Write(buffer []byte) (int, error) {
	_ = writer.SetWriteDeadline(time.Now().Add(writer.timeout))
	return writer.Conn.Write(buffer)
}
