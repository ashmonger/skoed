package loadgen

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
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
	Source  Source        // the query of each query number
	// Start is when the run starts; zero is now. A start in the future
	// lets several runs, or a run and a measurement, start together.
	Start time.Time
	// Records keeps a QueryRecord for every query in Result.Queries,
	// with the addresses of the A records of each response. It costs
	// memory and a full parse of every response: use it at test rates.
	Records bool
}

// QueryRecord is what happened to one query.
type QueryRecord struct {
	N       int64 // query number
	Kind    Kind
	Sent    time.Duration // from the start of the run
	Latency time.Duration // from the send to the response; 0 unless a response came
	Outcome Outcome
	Rcode   int      // RCODE of the response; 0 unless a response came
	Addrs   []net.IP // addresses of the A records of the response
	sent    bool
}

// PhaseResult summarizes one phase.
type PhaseResult struct {
	Phase Phase
	// From and To bound the seconds of the phase, To excluded.
	From, To int
	// Summary covers the whole phase. Use it for the shares of answered,
	// REFUSED, other and lost queries and for the latencies.
	Summary Summary
	// SettledFrom is the first second after the phase's settle time.
	SettledFrom int
	// Settled covers [SettledFrom, To). Use it for the answers per
	// second, which the burst allowance raises at the start of a phase.
	Settled Summary
}

// Result is the outcome of a run.
type Result struct {
	Seconds []Second
	Phases  []PhaseResult
	Queries []QueryRecord // with Config.Records, the queries sent in order
	Workers int
	// Late counts responses that match no query in flight: they came
	// after their query was counted lost, came twice, or carry another
	// question than the query with their ID.
	Late int64
	// Evicted counts queries counted lost before their timeout because
	// their socket had to reuse their DNS ID (too few workers).
	Evicted int64
	// SendLag is how late each query was sent against its schedule; a
	// high p99 means the load generator could not keep the rate.
	SendLag Hist
	Elapsed time.Duration // from the start to the outcome of the last query
}

// conn is the part of *net.UDPConn a worker uses.
type conn interface {
	Read([]byte) (int, error)
	Write([]byte) (int, error)
	Close() error
}

// Run sends the queries of cfg and waits for their responses or their
// timeout. Cancelling ctx stops the sending; the queries already sent
// still get their outcome. onSecond, when not nil, is called with each
// second of the run, in order, once all the queries sent in it have
// their outcome; the seconds it receives are those of Result.Seconds.
func Run(ctx context.Context, cfg Config, onSecond func(Second)) (*Result, error) {
	sched := NewSchedule(cfg.Phases)
	if cfg.Timeout <= 0 {
		return nil, errors.New("timeout must be positive")
	}
	if cfg.Source == nil {
		return nil, errors.New("no query source")
	}
	workers := cfg.Workers
	if workers == 0 {
		workers = DefaultWorkers(sched.PeakRate(), cfg.Timeout)
	}
	if inFlight := sched.PeakRate() * cfg.Timeout.Seconds() / float64(workers); inFlight > idsPerSocket {
		return nil, fmt.Errorf("%d workers at %.0f qps with a %v timeout keep %.0f queries in flight per socket, more than the %d DNS IDs; use at least %d workers",
			workers, sched.PeakRate(), cfg.Timeout, inFlight, idsPerSocket, DefaultWorkers(sched.PeakRate(), cfg.Timeout))
	}

	run := newRunState(sched, cfg.Timeout, workers, cfg.Source, cfg.Records)
	ws := make([]*worker, workers)
	for i := range ws {
		c, err := net.Dial("udp", cfg.Server)
		if err != nil {
			for _, w := range ws[:i] {
				w.conn.Close()
			}
			return nil, err
		}
		_ = c.(*net.UDPConn).SetReadBuffer(1 << 20)
		ws[i] = newWorker(run, i, c)
	}

	run.start = cfg.Start
	if run.start.IsZero() {
		run.start = time.Now()
	}
	var wg sync.WaitGroup
	for _, w := range ws {
		wg.Go(w.receive)
		wg.Go(func() {
			w.send(ctx)
			w.conn.Close() // ends receive
		})
	}
	done := make(chan struct{})
	reported := make(chan int)
	go func() { reported <- run.report(done, onSecond) }()
	wg.Wait()
	close(done)
	next := <-reported

	res := &Result{
		Seconds: run.rec.final(),
		Workers: workers,
		Late:    run.late.Load(),
		Evicted: run.evicted.Load(),
		Elapsed: time.Since(run.start),
	}
	if onSecond != nil {
		for _, s := range res.Seconds[next:] {
			onSecond(s)
		}
	}
	for i := range run.lag {
		res.SendLag.Merge(&run.lag[i])
	}
	for i := range run.records {
		if run.records[i].sent {
			res.Queries = append(res.Queries, run.records[i])
		}
	}
	for i, p := range cfg.Phases {
		start := sched.PhaseStart(i)
		pr := PhaseResult{
			Phase:       p,
			From:        int(start / time.Second),
			To:          int((start + p.Duration + time.Second - 1) / time.Second),
			SettledFrom: int((start + p.Settle + time.Second - 1) / time.Second),
		}
		pr.Summary = Summarize(res.Seconds, pr.From, pr.To)
		pr.Settled = Summarize(res.Seconds, pr.SettledFrom, pr.To)
		res.Phases = append(res.Phases, pr)
	}
	return res, nil
}

// runState is shared by the workers of a run.
type runState struct {
	sched   *Schedule
	rec     *recorder
	timeout time.Duration
	workers int
	source  Source
	records []QueryRecord // nil unless Config.Records
	start   time.Time
	lag     []Hist // one per worker
	late    atomic.Int64
	evicted atomic.Int64
}

func newRunState(sched *Schedule, timeout time.Duration, workers int, source Source, records bool) *runState {
	r := &runState{
		sched:   sched,
		rec:     newRecorder(sched.Duration()),
		timeout: timeout,
		workers: workers,
		source:  source,
		lag:     make([]Hist, workers),
	}
	if records {
		r.records = make([]QueryRecord, sched.Total())
	}
	return r
}

func (r *runState) now() time.Duration { return time.Since(r.start) }

// report calls onSecond for each second but the last one once the
// timeout of its last query has passed, until done. It returns the
// number of seconds reported; Run reports the others from the final
// seconds, whose last one also holds the sends that ran past the
// schedule.
func (r *runState) report(done <-chan struct{}, onSecond func(Second)) int {
	if onSecond == nil {
		<-done
		return 0
	}
	last := int((r.sched.Duration()+time.Second-1)/time.Second) - 1
	next := 0
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
		case <-done:
			return next
		}
		// Workers check timeouts at least every sweepEvery.
		settled := r.now() - r.timeout - 2*sweepEvery
		for ; next < last && time.Duration(next+1)*time.Second <= settled; next++ {
			onSecond(r.rec.second(next))
		}
	}
}

// sweepEvery bounds how long a worker sleeps, so that it checks the
// timeouts of its queries at least that often.
const sweepEvery = 10 * time.Millisecond

// worker sends the queries n with n % workers == index on its own
// socket. Its queries get consecutive DNS IDs. For the query in flight
// with ID id, stamps[id] holds its send time + 1 (0 when none is in
// flight), seqs[id] its number and names[id] a hash of its name. The
// receiver and the timeout sweep both clear stamps[id] with a
// compare-and-swap, so each query is counted once, as a response or as
// lost.
type worker struct {
	run    *runState
	index  int
	conn   conn
	stamps *[idsPerSocket]atomic.Int64
	seqs   *[idsPerSocket]atomic.Int64
	names  *[idsPerSocket]atomic.Uint64

	sentCount  int64 // queries sent; the next ID is uint16(sentCount)
	sweptCount int64 // queries whose slot the sweep has passed
}

func newWorker(run *runState, index int, c conn) *worker {
	return &worker{
		run:    run,
		index:  index,
		conn:   c,
		stamps: new([idsPerSocket]atomic.Int64),
		seqs:   new([idsPerSocket]atomic.Int64),
		names:  new([idsPerSocket]atomic.Uint64),
	}
}

func (w *worker) send(ctx context.Context) {
	r := w.run
	lag := &r.lag[w.index]
	buf := make([]byte, 0, 512)
	n := int64(w.index)
	for n < r.sched.Total() && ctx.Err() == nil {
		for due := r.sched.Due(r.now()); n < due; n += int64(r.workers) {
			w.sendOne(n, lag, &buf)
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

func (w *worker) sendOne(n int64, lag *Hist, buf *[]byte) {
	r := w.run
	if w.sentCount-w.sweptCount == idsPerSocket {
		w.evictOldest()
	}
	q := r.source.Query(n)
	id := uint16(w.sentCount)
	at := r.now()
	lag.Add(at - r.sched.At(n))
	r.rec.sent(at)
	var rec *QueryRecord
	if r.records != nil {
		rec = &r.records[n]
		*rec = QueryRecord{N: n, Kind: q.Kind, Sent: at, sent: true}
	}
	if q.Name == nil {
		r.rec.sendError(at)
		if rec != nil {
			rec.Outcome = OutcomeSendError
		}
		return
	}
	*buf = AppendQuery((*buf)[:0], id, q.Name)
	stamp := int64(at) + 1
	w.seqs[id].Store(n)
	w.names[id].Store(hashName(q.Name))
	w.stamps[id].Store(stamp)
	w.sentCount++
	if _, err := w.conn.Write(*buf); err != nil {
		if w.stamps[id].CompareAndSwap(stamp, 0) {
			r.rec.sendError(at)
			if rec != nil {
				rec.Outcome = OutcomeSendError
			}
		}
	}
}

// sweep counts as lost the queries in flight longer than the timeout.
// Queries are swept in send order, so it stops at the first one still
// within its timeout.
func (w *worker) sweep(now time.Duration) {
	for w.sweptCount < w.sentCount {
		slot := &w.stamps[uint16(w.sweptCount)]
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
	slot := &w.stamps[uint16(w.sweptCount)]
	if v := slot.Load(); v != 0 && slot.CompareAndSwap(v, 0) {
		w.run.rec.lost(time.Duration(v - 1))
		w.run.evicted.Add(1)
	}
	w.sweptCount++
}

// receive reads responses until the socket is closed. A response is
// matched to the query in flight with its ID and the same question name;
// the low 4 bits of the flags give the RCODE.
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
		msg := buf[:k]
		if k < 12 || msg[2]&0x80 == 0 {
			continue
		}
		id := binary.BigEndian.Uint16(msg)
		slot := &w.stamps[id]
		v := slot.Load()
		if v == 0 || !questionMatches(msg, w.names[id].Load()) {
			r.late.Add(1)
			continue
		}
		n := w.seqs[id].Load()
		if !slot.CompareAndSwap(v, 0) {
			r.late.Add(1)
			continue
		}
		sentAt := time.Duration(v - 1)
		rcode := int(msg[3] & 0x0f)
		r.rec.response(sentAt, now-sentAt, rcode)
		if r.records != nil {
			rec := &r.records[n]
			rec.Latency, rec.Rcode, rec.Outcome = now-sentAt, rcode, Classify(rcode)
			rec.Addrs = addrs(msg)
		}
	}
}

// questionMatches says whether the question name of response msg has
// hash h. A response with no question, as a REFUSED response to a
// query whose question could not be parsed, matches by ID alone.
func questionMatches(msg []byte, h uint64) bool {
	if binary.BigEndian.Uint16(msg[4:]) == 0 {
		return true
	}
	end := 12
	for {
		if end >= len(msg) {
			return false
		}
		l := int(msg[end])
		if l&0xc0 != 0 {
			return false // the question name is never compressed
		}
		end += 1 + l
		if l == 0 {
			break
		}
	}
	if end > len(msg) {
		return false
	}
	return hashName(msg[12:end]) == h
}

// hashName is the FNV-1a hash of a wire-format name, ignoring ASCII case.
func hashName(name []byte) uint64 {
	h := uint64(14695981039346656037)
	for _, b := range name {
		if 'A' <= b && b <= 'Z' {
			b += 'a' - 'A'
		}
		h ^= uint64(b)
		h *= 1099511628211
	}
	return h
}

// addrs returns the addresses of the A records of response msg.
func addrs(msg []byte) []net.IP {
	var m dns.Msg
	if m.Unpack(msg) != nil {
		return nil
	}
	var out []net.IP
	for _, rr := range m.Answer {
		if a, ok := rr.(*dns.A); ok {
			out = append(out, a.A)
		}
	}
	return out
}

func sleep(d time.Duration) {
	if d > 0 {
		time.Sleep(d)
	}
}

// Warm sends one query for each name, a few at a time, and returns how
// many received an answer (NOERROR or NXDOMAIN), so that the names are
// in the server's cache before a run.
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
				if err == nil && Classify(resp.Rcode) == OutcomeAnswer {
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
