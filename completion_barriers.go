package main

import (
	"sync"
	"sync/atomic"
	"time"
)

type completionBarrier interface {
	completed() float64
	tryGrabWork() bool
	jobDone()
	done() <-chan struct{}
	cancel()
}

type countingCompletionBarrier struct {
	numReqs, reqsDone uint64
	reqsRemaining     int64
	doneChan          chan struct{}
	closeOnce         sync.Once
}

func newCountingCompletionBarrier(numReqs uint64) completionBarrier {
	c := new(countingCompletionBarrier)
	c.reqsDone, c.reqsRemaining, c.numReqs = 0, int64(numReqs), numReqs
	c.doneChan = make(chan struct{})
	return completionBarrier(c)
}

func (c *countingCompletionBarrier) tryGrabWork() bool {
	return atomic.AddInt64(&c.reqsRemaining, -1)+1 > 0
}

func (c *countingCompletionBarrier) jobDone() {
	reqsDone := atomic.AddUint64(&c.reqsDone, 1)
	if reqsDone == c.numReqs {
		c.closeOnce.Do(func() {
			atomic.StoreInt64(&c.reqsRemaining, 0)
			close(c.doneChan)
		})
	}
}

func (c *countingCompletionBarrier) done() <-chan struct{} {
	return c.doneChan
}

func (c *countingCompletionBarrier) cancel() {
	c.closeOnce.Do(func() {
		atomic.StoreInt64(&c.reqsRemaining, 0)
		close(c.doneChan)
	})
}

func (c *countingCompletionBarrier) completed() float64 {
	select {
	case <-c.doneChan:
		return 1.0
	default:
		reqsDone := atomic.LoadUint64(&c.reqsDone)
		return float64(reqsDone) / float64(c.numReqs)
	}
}

type timedCompletionBarrier struct {
	doneChan  chan struct{}
	doneFlag  uint64
	closeOnce sync.Once
	start     time.Time
	duration  time.Duration
}

func newTimedCompletionBarrier(duration time.Duration) completionBarrier {
	if duration < 0 {
		panic("timedCompletionBarrier: negative duration")
	}
	c := new(timedCompletionBarrier)
	c.doneChan = make(chan struct{})
	c.start = time.Now()
	c.duration = duration
	go func() {
		time.AfterFunc(duration, func() {
			c.closeOnce.Do(func() {
				atomic.StoreUint64(&c.doneFlag, 1)
				close(c.doneChan)
			})
		})
	}()
	return completionBarrier(c)
}

func (c *timedCompletionBarrier) tryGrabWork() bool {
	return atomic.LoadUint64(&c.doneFlag) == 0
}

func (c *timedCompletionBarrier) jobDone() {
}

func (c *timedCompletionBarrier) done() <-chan struct{} {
	return c.doneChan
}

func (c *timedCompletionBarrier) cancel() {
	c.closeOnce.Do(func() {
		atomic.StoreUint64(&c.doneFlag, 1)
		close(c.doneChan)
	})
}

func (c *timedCompletionBarrier) completed() float64 {
	if atomic.LoadUint64(&c.doneFlag) == 0 {
		return float64(time.Since(c.start).Nanoseconds()) /
			float64(c.duration.Nanoseconds())
	}
	return 1.0
}
