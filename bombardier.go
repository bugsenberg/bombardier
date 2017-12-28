package main

import (
	"fmt"
	"io"
	"io/ioutil"
	"math"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cheggaaa/pb"
	fhist "github.com/codesenberg/concurrent/float64/histogram"
	uhist "github.com/codesenberg/concurrent/uint64/histogram"
)

type bombardier struct {
	bytesRead, bytesWritten int64

	// HTTP codes
	req1xx uint64
	req2xx uint64
	req3xx uint64
	req4xx uint64
	req5xx uint64
	others uint64

	conf        config
	barrier     completionBarrier
	ratelimiter limiter
	workers     sync.WaitGroup

	timeTaken time.Duration
	latencies *uhist.Histogram
	requests  *fhist.Histogram

	client   client
	doneChan chan struct{}

	// RPS metrics
	rpl   sync.Mutex
	reqs  int64
	start time.Time

	// Errors
	errors *errorMap

	// Progress bar
	bar *pb.ProgressBar

	// Output
	out io.Writer
}

func newBombardier(c config) (*bombardier, error) {
	if err := c.checkArgs(); err != nil {
		return nil, err
	}
	b := new(bombardier)
	b.conf = c
	b.latencies = uhist.Default()
	b.requests = fhist.Default()

	if b.conf.testType() == counted {
		b.bar = pb.New64(int64(*b.conf.numReqs))
	} else if b.conf.testType() == timed {
		b.bar = pb.New64(b.conf.duration.Nanoseconds() / 1e9)
		b.bar.ShowCounters = false
		b.bar.ShowPercent = false
	}
	b.bar.ManualUpdate = true

	if b.conf.testType() == counted {
		b.barrier = newCountingCompletionBarrier(*b.conf.numReqs)
	} else {
		b.barrier = newTimedCompletionBarrier(*b.conf.duration)
	}

	if b.conf.rate != nil {
		b.ratelimiter = newBucketLimiter(*b.conf.rate)
	} else {
		b.ratelimiter = &nooplimiter{}
	}

	b.out = os.Stdout

	tlsConfig, err := generateTLSConfig(c)
	if err != nil {
		return nil, err
	}

	var (
		pbody *string
		bsp   bodyStreamProducer
	)
	if c.stream {
		if c.bodyFilePath != "" {
			bsp = func() (io.ReadCloser, error) {
				return os.Open(c.bodyFilePath)
			}
		} else {
			bsp = func() (io.ReadCloser, error) {
				return ioutil.NopCloser(
					proxyReader{strings.NewReader(c.body)},
				), nil
			}
		}
	} else {
		pbody = &c.body
		if c.bodyFilePath != "" {
			body, err := ioutil.ReadFile(c.bodyFilePath)
			if err != nil {
				return nil, err
			}
			sbody := string(body)
			pbody = &sbody
		}
	}

	cc := &clientOpts{
		HTTP2:     false,
		maxConns:  c.numConns,
		timeout:   c.timeout,
		tlsConfig: tlsConfig,

		headers:      c.headers,
		url:          c.url,
		method:       c.method,
		body:         pbody,
		bodProd:      bsp,
		bytesRead:    &b.bytesRead,
		bytesWritten: &b.bytesWritten,
	}
	b.client = makeHTTPClient(c.clientType, cc)

	b.workers.Add(int(c.numConns))
	b.errors = newErrorMap()
	b.doneChan = make(chan struct{}, 2)
	return b, nil
}

func makeHTTPClient(clientType clientTyp, cc *clientOpts) client {
	var cl client
	switch clientType {
	case nhttp1:
		cl = newHTTPClient(cc)
	case nhttp2:
		cc.HTTP2 = true
		cl = newHTTPClient(cc)
	case fhttp:
		fallthrough
	default:
		cl = newFastHTTPClient(cc)
	}
	return cl
}

func (b *bombardier) writeStatistics(
	code int, msTaken uint64,
) {
	b.latencies.Increment(msTaken)
	b.rpl.Lock()
	b.reqs++
	b.rpl.Unlock()
	var counter *uint64
	switch code / 100 {
	case 1:
		counter = &b.req1xx
	case 2:
		counter = &b.req2xx
	case 3:
		counter = &b.req3xx
	case 4:
		counter = &b.req4xx
	case 5:
		counter = &b.req5xx
	default:
		counter = &b.others
	}
	atomic.AddUint64(counter, 1)
}

func (b *bombardier) performSingleRequest() {
	code, msTaken, err := b.client.do()
	if err != nil {
		b.errors.add(err)
	}
	b.writeStatistics(code, msTaken)
}

func (b *bombardier) worker() {
	done := b.barrier.done()
	for b.barrier.tryGrabWork() {
		if b.ratelimiter.pace(done) == brk {
			break
		}
		b.performSingleRequest()
		b.barrier.jobDone()
	}
}

func (b *bombardier) barUpdater() {
	done := b.barrier.done()
	for {
		select {
		case <-done:
			b.bar.Set64(b.bar.Total)
			b.bar.Update()
			b.bar.Finish()
			fmt.Fprintln(b.out, "Done!")
			b.doneChan <- struct{}{}
			return
		default:
			current := int64(b.barrier.completed() * float64(b.bar.Total))
			b.bar.Set64(current)
			b.bar.Update()
			time.Sleep(b.bar.RefreshRate)
		}
	}
}

func (b *bombardier) rateMeter() {
	requestsInterval := 10 * time.Millisecond
	if b.conf.rate != nil {
		requestsInterval, _ = estimate(*b.conf.rate, rateLimitInterval)
	}
	requestsInterval += 10 * time.Millisecond
	ticker := time.NewTicker(requestsInterval)
	defer ticker.Stop()
	tick := ticker.C
	done := b.barrier.done()
	for {
		select {
		case <-tick:
			b.recordRps()
			continue
		case <-done:
			b.workers.Wait()
			b.recordRps()
			b.doneChan <- struct{}{}
			return
		}
	}
}

func (b *bombardier) recordRps() {
	b.rpl.Lock()
	duration := time.Since(b.start)
	reqs := b.reqs
	b.reqs = 0
	b.start = time.Now()
	b.rpl.Unlock()

	reqsf := float64(reqs) / duration.Seconds()
	b.requests.Increment(reqsf)
}

func (b *bombardier) bombard() {
	b.printIntro()
	b.bar.Start()
	bombardmentBegin := time.Now()
	b.start = time.Now()
	for i := uint64(0); i < b.conf.numConns; i++ {
		go func() {
			defer b.workers.Done()
			b.worker()
		}()
	}
	go b.rateMeter()
	go b.barUpdater()
	b.workers.Wait()
	b.timeTaken = time.Since(bombardmentBegin)
	<-b.doneChan
	<-b.doneChan
}

func (b *bombardier) printIntro() {
	if b.conf.testType() == counted {
		fmt.Fprintf(b.out, "Bombarding %v with %v requests using %v connections\n",
			b.conf.url, *b.conf.numReqs, b.conf.numConns)
	} else if b.conf.testType() == timed {
		fmt.Fprintf(b.out, "Bombarding %v for %v using %v connections\n",
			b.conf.url, *b.conf.duration, b.conf.numConns)
	}
}

func latenciesPercentile(h *uhist.Histogram, p float64) uint64 {
	keys := make([]uint64, 0, h.Count())
	totalCount := uint64(0)
	h.VisitAll(func(k uint64, v uint64) bool {
		keys = append(keys, k)
		totalCount += v
		return true
	})
	sort.Slice(keys, func(i, j int) bool {
		return keys[i] < keys[j]
	})
	rank := uint64((p/100.0)*float64(totalCount) + 0.5)
	total := uint64(0)
	for _, k := range keys {
		total += h.Get(k)
		if total >= rank {
			return k
		}
	}
	return 0
}

func (b *bombardier) printLatencyStats() {
	percentiles := []float64{50.0, 75.0, 90.0, 99.0}
	fmt.Fprintln(b.out, "  Latency Distribution")
	for i := 0; i < len(percentiles); i++ {
		p := percentiles[i]
		n := latenciesPercentile(b.latencies, p)
		fmt.Fprintf(b.out, "     %2.0f%% %10s",
			p, formatUnits(float64(n), timeUnitsUs, 2))
		fmt.Fprintf(b.out, "\n")
	}
}

func rpsString(h *fhist.Histogram) string {
	sum := float64(0)
	count := uint64(1)
	max := 0.0
	h.VisitAll(func(f float64, c uint64) bool {
		if math.IsInf(f, 0) || math.IsNaN(f) {
			return true
		}
		if f > max {
			max = f
		}
		sum += f * float64(c)
		count += c
		return true
	})
	mean := sum / float64(count)
	sumOfSquares := float64(0)
	h.VisitAll(func(f float64, c uint64) bool {
		if math.IsInf(f, 0) || math.IsNaN(f) {
			return true
		}
		sumOfSquares += math.Pow(f-mean, 2)
		return true
	})
	stddev := math.Sqrt(sumOfSquares / float64(count))
	return fmt.Sprintf("  %-10v %10.2f %10.2f %10.2f",
		"Reqs/sec", mean, stddev, max)
}

func latenciesString(h *uhist.Histogram) string {
	sum := uint64(0)
	count := uint64(1)
	max := uint64(0)
	h.VisitAll(func(f uint64, c uint64) bool {
		if f > max {
			max = f
		}
		sum += f * c
		count += c
		return true
	})
	mean := float64(sum) / float64(count)
	sumOfSquares := float64(0)
	h.VisitAll(func(f uint64, c uint64) bool {
		sumOfSquares += math.Pow(float64(f)-mean, 2)
		return true
	})
	stddev := math.Sqrt(sumOfSquares / float64(count))
	return fmt.Sprintf("  %-10v %10v %10v %10v",
		"Latency",
		formatTimeUs(mean),
		formatTimeUs(stddev),
		formatTimeUs(float64(max)))
}

func (b *bombardier) printStats() {
	fmt.Fprintf(b.out, "%10v %10v %10v %10v\n",
		"Statistics", "Avg", "Stdev", "Max")
	fmt.Fprintln(b.out, rpsString(b.requests))
	fmt.Fprintln(b.out, latenciesString(b.latencies))
	if b.conf.printLatencies {
		b.printLatencyStats()
	}
	fmt.Fprintln(b.out, "  HTTP codes:")
	fmt.Fprintf(b.out, "    1xx - %v, 2xx - %v, 3xx - %v, 4xx - %v, 5xx - %v\n",
		b.req1xx, b.req2xx, b.req3xx, b.req4xx, b.req5xx)
	fmt.Fprintf(b.out, "    others - %v\n", b.others)
	if b.errors.sum() > 0 {
		fmt.Fprintln(b.out, "  Errors:")
		for _, entry := range b.errors.byFrequency() {
			fmt.Fprintf(b.out, "    %10v - %v\n", entry.error, entry.count)
		}
	}
	fmt.Fprintf(b.out, "  %-10v %10v/s\n",
		"Throughput:",
		formatBinary(float64(b.bytesRead+b.bytesWritten)/b.timeTaken.Seconds()),
	)
}

func (b *bombardier) redirectOutputTo(out io.Writer) {
	b.bar.Output = out
	b.out = out
}

func (b *bombardier) disableOutput() {
	b.redirectOutputTo(ioutil.Discard)
	b.bar.NotPrint = true
}

func main() {
	cfg, err := parser.parse(os.Args)
	if err != nil {
		fmt.Println(err)
		os.Exit(exitFailure)
	}
	bombardier, err := newBombardier(cfg)
	if err != nil {
		fmt.Println(err)
		os.Exit(exitFailure)
	}
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	go func() {
		<-c
		bombardier.barrier.cancel()
	}()
	bombardier.bombard()
	bombardier.printStats()
}
