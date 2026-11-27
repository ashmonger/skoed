package loadgen

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/miekg/dns"
)

// Config describes a run.
type Config struct {
	Server  string        // host:port of the DNS server
	Phases  []Phase       // run one after the other
	Timeout time.Duration // a query with no response by then is lost
	Workers int           // sockets; 0 picks DefaultWorkers
	Mix     *Mix
	Seed    uint64 // seeds the choice of names
}

// PhaseResult is the summary of one phase, over the phase minus its
// settle time.
type PhaseResult struct {
	Phase   Phase
	From    int // first second of the summary
	To      int // second after the last one
	Summary Summary
}

// Result is the outcome of a run.
type Result struct {
	Seconds []Second
	Phases  []PhaseResult
	Workers int
	// Late counts responses that arrived after their query was counted
	// lost, or that match no query sent.
	Late int64
	// Evicted counts queries counted lost before their timeout because
	// their socket had to reuse their DNS ID (too few workers).
	Evicted int64
	// SendErrors counts sends that failed; those queries are counted lost.
	SendErrors int64
	// SendLag is how late each query was sent against its schedule; a
	// high p99 means the load generator could not keep the rate.
	SendLag Hist
	Elapsed time.Duration
}

// Run sends the queries of cfg and waits for their responses or their
// timeout. onSecond, when not nil, is called with each second of the run
// once all the queries sent in it have their outcome, in order.
func Run(ctx context.Context, cfg Config, onSecond func(Second)) (*Result, error) {
	sched := NewSchedule(cfg.Phases)
	if cfg.Timeout <= 0 {
		return nil, errors.New("timeout must be positive")
	}
	if cfg.Mix == nil {
		return nil, errors.New("no query mix")
	}
	workers := cfg.Workers
	if workers == 0 {
		workers = DefaultWorkers(sched.PeakRate(), cfg.Timeout)
	}
	if inFlight := sched.PeakRate() * cfg.Timeout.Seconds() / float64(workers); inFlight > idsPerSocket {
		return nil, fmt.Errorf("%d workers at %.0f qps with a %v timeout keep %.0f queries in flight per socket, more than the %d DNS IDs; use at least %d workers",
			workers, sched.PeakRate(), cfg.Timeout, inFlight, idsPerSocket, DefaultWorkers(sched.PeakRate(), cfg.Timeout))
	}

	run := &runState{
		sched:   sched,
		rec:     newRecorder(sched.Duration()),
		timeout: cfg.Timeout,
		workers: workers,
		lag:     make([]Hist, workers),
	}
	ws := make([]*worker, workers)
	for i := range ws {
		conn, err := net.Dial("udp", cfg.Server)
		if err != nil {
			for _, w := range ws[:i] {
				w.conn.Close()
			}
			return nil, err
		}
		ws[i] = &worker{
			run:   run,
			index: i,
			conn:  conn.(*net.UDPConn),
			mix:   cfg.Mix,
			rand:  rand.New(rand.NewPCG(cfg.Seed, uint64(i))),
			slots: new([idsPerSocket]atomic.Int64),
		}
	}

	run.start = time.Now()
	var wg sync.WaitGroup
	for _, w := range ws {
		wg.Go(w.receive)
		wg.Go(func() {
			w.send(ctx)
			w.conn.Close() // ends receive
		})
	}
	done := make(chan struct{})
	reported := make(chan struct{})
	go func() {
		run.report(done, onSecond)
		close(reported)
	}()
	wg.Wait()
	close(done)
	<-reported

	res := &Result{
		Workers:    workers,
		Late:       run.late.Load(),
		Evicted:    run.evicted.Load(),
		SendErrors: run.sendErrors.Load(),
		Elapsed:    time.Since(run.start),
	}
	for i := range run.lag {
		res.SendLag.Merge(&run.lag[i])
	}
	res.Seconds = make([]Second, len(run.rec.seconds))
	for i := range res.Seconds {
		res.Seconds[i] = run.rec.second(i)
	}
	// The last second only holds sends that ran past the schedule; fold
	// it into the one before so the seconds match the run's length.
	if n := len(res.Seconds); n > 1 {
		last := res.Seconds[n-1]
		if last.Sent != 0 {
			prev := &res.Seconds[n-2]
			prev.Sent += last.Sent
			prev.Lost += last.Lost
			for j, c := range last.Rcodes {
				prev.Rcodes[j] += c
			}
			prev.Ans.Merge(&last.Ans)
			prev.All.Merge(&last.All)
		}
		res.Seconds = res.Seconds[:n-1]
	}
	for i, p := range cfg.Phases {
		start := sched.PhaseStart(i)
		from := int((start + p.Settle + time.Second - 1) / time.Second)
		to := int((start + p.Duration) / time.Second)
		res.Phases = append(res.Phases, PhaseResult{
			Phase: p, From: from, To: to,
			Summary: Summarize(res.Seconds, from, to),
		})
	}
	return res, nil
}

// runState is shared by the workers of a run.
type runState struct {
	sched      *Schedule
	rec        *recorder
	timeout    time.Duration
	workers    int
	start      time.Time
	lag        []Hist // one per worker
	late       atomic.Int64
	evicted    atomic.Int64
	sendErrors atomic.Int64
}

func (r *runState) now() time.Duration { return time.Since(r.start) }

// report calls onSecond for each second once the timeout of its last
// query has passed, then for the remaining seconds when the run is done.
func (r *runState) report(done <-chan struct{}, onSecond func(Second)) {
	if onSecond == nil {
		<-done
		return
	}
	seconds := int((r.sched.Duration() + time.Second - 1) / time.Second)
	next := 0
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for next < seconds {
		select {
		case <-tick.C:
		case <-done:
			for ; next < seconds; next++ {
				onSecond(r.rec.second(next))
			}
			return
		}
		// Workers check timeouts at least every sweepEvery.
		settled := r.now() - r.timeout - 2*sweepEvery
		for ; next < seconds && time.Duration(next+1)*time.Second <= settled; next++ {
			onSecond(r.rec.second(next))
		}
	}
	<-done
}

// sweepEvery bounds how long a worker sleeps, so that it checks the
// timeouts of its queries at least that often.
const sweepEvery = 10 * time.Millisecond

// worker sends the queries n with n % workers == index on its own
// socket. Its queries get consecutive DNS IDs; slots[id] holds the send
// time + 1 of the query in flight with that ID, or 0. The receiver and
// the timeout sweep both clear a slot with a compare-and-swap, so each
// query is counted once, as a response or as lost.
type worker struct {
	run   *runState
	index int
	conn  *net.UDPConn
	mix   *Mix
	rand  *rand.Rand
	slots *[idsPerSocket]atomic.Int64

	sentCount  int64 // queries sent; the next ID is uint16(sentCount)
	sweptCount int64 // queries whose slot the sweep has passed
}

func (w *worker) send(ctx context.Context) {
	r := w.run
	lag := &r.lag[w.index]
	buf := make([]byte, 0, 512)
	n := int64(w.index)
	for n < r.sched.Total() && ctx.Err() == nil {
		now := r.now()
		for due := r.sched.Due(now); n < due; n += int64(r.workers) {
			if w.sentCount-w.sweptCount == idsPerSocket {
				w.evictOldest()
			}
			qname, _ := w.mix.Pick(w.rand, n)
			id := uint16(w.sentCount)
			buf = AppendQuery(buf[:0], id, qname)
			at := r.now()
			lag.Add(at - r.sched.At(n))
			stamp := int64(at) + 1
			w.slots[id].Store(stamp)
			r.rec.sent(at)
			w.sentCount++
			if _, err := w.conn.Write(buf); err != nil {
				r.sendErrors.Add(1)
				if w.slots[id].CompareAndSwap(stamp, 0) {
					r.rec.lost(at)
				}
			}
		}
		w.sweep(r.now())
		if n < r.sched.Total() {
			sleep(min(r.sched.At(n)-r.now(), sweepEvery))
		}
	}
	for w.sweptCount < w.sentCount {
		sleep(sweepEvery)
		w.sweep(r.now())
	}
}

// sweep counts as lost the queries in flight longer than the timeout.
// Queries are swept in send order, so it stops at the first one still
// within its timeout.
func (w *worker) sweep(now time.Duration) {
	for w.sweptCount < w.sentCount {
		slot := &w.slots[uint16(w.sweptCount)]
		if v := slot.Load(); v != 0 {
			sentAt := time.Duration(v - 1)
			if now-sentAt < w.run.timeout {
				return
			}
			if slot.CompareAndSwap(v, 0) {
				w.run.rec.lost(sentAt)
			}
		}
		w.sweptCount++
	}
}

// evictOldest frees the ID of the oldest query in flight, counting it
// lost, so that the ID can be reused.
func (w *worker) evictOldest() {
	slot := &w.slots[uint16(w.sweptCount)]
	if v := slot.Load(); v != 0 && slot.CompareAndSwap(v, 0) {
		w.run.rec.lost(time.Duration(v - 1))
		w.run.evicted.Add(1)
	}
	w.sweptCount++
}

// receive reads responses until the socket is closed. Only the header
// is read: the ID finds the query, the low 4 bits of the flags give the
// RCODE.
func (w *worker) receive() {
	r := w.run
	buf := make([]byte, 65535)
	for {
		k, err := w.conn.Read(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue // e.g. ICMP port unreachable on a connected socket
		}
		now := r.now()
		if k < 12 || buf[2]&0x80 == 0 {
			continue
		}
		slot := &w.slots[binary.BigEndian.Uint16(buf)]
		v := slot.Load()
		if v == 0 || !slot.CompareAndSwap(v, 0) {
			r.late.Add(1)
			continue
		}
		sentAt := time.Duration(v - 1)
		r.rec.response(sentAt, now-sentAt, int(buf[3]&0x0f))
	}
}

func sleep(d time.Duration) {
	if d > 0 {
		time.Sleep(d)
	}
}

// Warm sends one query for each name, a few at a time, and returns how
// many received an answer other than REFUSED, so that the names are in
// the server's cache before a run.
func Warm(ctx context.Context, server string, names []string, timeout time.Duration) (int, error) {
	const parallel = 32
	client := &dns.Client{Net: "udp", Timeout: timeout}
	var answered atomic.Int64
	var wg sync.WaitGroup
	next := make(chan string)
	for range parallel {
		wg.Go(func() {
			for name := range next {
				m := new(dns.Msg)
				m.SetQuestion(dns.Fqdn(name), dns.TypeA)
				resp, _, err := client.ExchangeContext(ctx, m, server)
				if err == nil && resp.Rcode != dns.RcodeRefused {
					answered.Add(1)
				}
			}
		})
	}
feed:
	for _, name := range names {
		select {
		case next <- name:
		case <-ctx.Done():
			break feed
		}
	}
	close(next)
	wg.Wait()
	return int(answered.Load()), ctx.Err()
}
