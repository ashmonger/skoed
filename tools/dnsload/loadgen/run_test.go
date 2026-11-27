package loadgen

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// startServer starts an in-process miekg/dns server on a loopback UDP
// port; reply gives the response to the i-th query (from 1).
func startServer(t *testing.T, reply func(i int64, req *dns.Msg) *dns.Msg) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var count atomic.Int64
	handler := dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
		_ = w.WriteMsg(reply(count.Add(1), req))
	})
	started := make(chan struct{})
	srv := &dns.Server{PacketConn: pc, Handler: handler, NotifyStartedFunc: func() { close(started) }}
	go func() { _ = srv.ActivateAndServe() }()
	<-started
	t.Cleanup(func() { _ = srv.Shutdown() })
	return pc.LocalAddr().String()
}

func answerA(req *dns.Msg) *dns.Msg {
	resp := new(dns.Msg)
	resp.SetReply(req)
	resp.Answer = append(resp.Answer, &dns.A{
		Hdr: dns.RR_Header{Name: req.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
		A:   net.IPv4(192, 0, 2, 1),
	})
	return resp
}

func withRcode(req *dns.Msg, rcode int) *dns.Msg {
	resp := new(dns.Msg)
	resp.SetRcode(req, rcode)
	return resp
}

// startRawServer starts a UDP server that sends back what respond
// returns for each query, possibly several packets.
func startRawServer(t *testing.T, respond func(query []byte) [][]byte) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	go func() {
		buf := make([]byte, 65535)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			for _, out := range respond(append([]byte(nil), buf[:n]...)) {
				_, _ = pc.WriteTo(out, from)
			}
		}
	}()
	return pc.LocalAddr().String()
}

func TestRunAgainstServerRefusingEverySecondQuery(t *testing.T) {
	addr := startServer(t, func(i int64, req *dns.Msg) *dns.Msg {
		if i%2 == 0 {
			return withRcode(req, dns.RcodeRefused)
		}
		return answerA(req)
	})
	var mu sync.Mutex
	var rows []Second
	res, err := Run(context.Background(), Config{
		Server:  addr,
		Phases:  []Phase{{Rate: 400, Duration: 2 * time.Second}, {Rate: 100, Duration: time.Second}},
		Timeout: 500 * time.Millisecond,
		Workers: 3,
		Source:  testMix(t),
	}, func(s Second) {
		mu.Lock()
		rows = append(rows, s)
		mu.Unlock()
	})
	if err != nil {
		t.Fatal(err)
	}
	tot := Summarize(res.Seconds, 0, len(res.Seconds))
	if tot.Sent != 900 {
		t.Errorf("sent %d, want 900", tot.Sent)
	}
	if tot.Lost != 0 || res.Late != 0 || tot.SendErrors != 0 {
		t.Errorf("lost %d, late %d, send errors %d, want 0", tot.Lost, res.Late, tot.SendErrors)
	}
	if tot.Refused() != 450 || tot.Answered() != 450 || tot.Other() != 0 {
		t.Errorf("refused %d answered %d other %d, want 450 450 0", tot.Refused(), tot.Answered(), tot.Other())
	}
	if tot.Ans.Count() != 450 || tot.All.Count() != 900 {
		t.Errorf("latencies: %d answers, %d responses, want 450 and 900", tot.Ans.Count(), tot.All.Count())
	}
	// Queries count in the second they were actually sent, so a query
	// sent a little late near the end of a second moves to the next one.
	near := func(got, want int64) bool { return got >= want-5 && got <= want+5 }
	if len(res.Phases) != 2 || !near(res.Phases[0].Summary.Sent, 800) || !near(res.Phases[1].Summary.Sent, 100) {
		t.Fatalf("phases: %+v", res.Phases)
	}
	if p := res.Phases[0]; p.From != 0 || p.To != 2 || p.SettledFrom != 0 || p.Settled.Seconds != 2 {
		t.Errorf("phase 1 window: from %d to %d settled from %d", p.From, p.To, p.SettledFrom)
	}
	// The rows passed to onSecond are the final seconds, in order.
	mu.Lock()
	defer mu.Unlock()
	if len(rows) != 3 || len(res.Seconds) != 3 {
		t.Fatalf("%d per-second rows and %d seconds, want 3", len(rows), len(res.Seconds))
	}
	for i, want := range []int64{400, 400, 100} {
		if rows[i].Index != i || !near(rows[i].Sent, want) || rows[i].Sent != res.Seconds[i].Sent {
			t.Errorf("row %d: index %d sent %d (final %d), want about %d", i, rows[i].Index, rows[i].Sent, res.Seconds[i].Sent, want)
		}
	}
}

func TestRunSettleOnlyExcludesTheAnswerRate(t *testing.T) {
	addr := startServer(t, func(_ int64, req *dns.Msg) *dns.Msg { return withRcode(req, dns.RcodeRefused) })
	mix, _ := NewMix(MixConfig{Zone: "perf.test", Percent: [3]int{0, 100, 0}, RunID: "s"})
	res, err := Run(context.Background(), Config{
		Server:  addr,
		Phases:  []Phase{{Rate: 100, Duration: 2 * time.Second, Settle: time.Second}},
		Timeout: 500 * time.Millisecond,
		Source:  mix,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	p := res.Phases[0]
	// Every REFUSED of the phase counts, including those of the settle
	// time; only the answer rate leaves it out.
	if p.Summary.Refused() != 200 || p.Settled.Seconds != 1 || p.SettledFrom != 1 {
		t.Errorf("refused %d over the phase, settled window %d s from %d, want 200, 1 s from 1",
			p.Summary.Refused(), p.Settled.Seconds, p.SettledFrom)
	}
}

func TestRunKeepsOtherRcodesApart(t *testing.T) {
	addr := startServer(t, func(i int64, req *dns.Msg) *dns.Msg {
		switch i % 4 {
		case 0:
			return withRcode(req, dns.RcodeServerFailure)
		case 1:
			return withRcode(req, dns.RcodeNameError)
		case 2:
			return withRcode(req, dns.RcodeRefused)
		default:
			return answerA(req)
		}
	})
	res, err := Run(context.Background(), Config{
		Server:  addr,
		Phases:  []Phase{{Rate: 400, Duration: time.Second}},
		Timeout: 500 * time.Millisecond,
		Source:  testMix(t),
		Records: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := res.Phases[0].Summary
	if s.Answered() != 200 || s.Refused() != 100 || s.Other() != 100 || s.Rcodes[dns.RcodeServerFailure] != 100 {
		t.Errorf("answered %d refused %d other %d, want 200 100 100", s.Answered(), s.Refused(), s.Other())
	}
	if s.PercentAnsweredOrRefused() != 75 || s.Ans.Count() != 200 || s.All.Count() != 400 {
		t.Errorf("answered or refused %v %%, %d answer and %d response latencies, want 75, 200, 400",
			s.PercentAnsweredOrRefused(), s.Ans.Count(), s.All.Count())
	}
	if len(res.Queries) != 400 {
		t.Fatalf("%d records, want 400", len(res.Queries))
	}
	var outcomes [5]int
	withAddr := 0
	for i, q := range res.Queries {
		if q.N != int64(i) || q.Latency <= 0 {
			t.Fatalf("record %d: %+v", i, q)
		}
		outcomes[q.Outcome]++
		if len(q.Addrs) == 1 && q.Addrs[0].Equal(net.IPv4(192, 0, 2, 1)) {
			withAddr++
		}
	}
	if outcomes[OutcomeAnswer] != 200 || outcomes[OutcomeRefused] != 100 || outcomes[OutcomeOther] != 100 || withAddr != 100 {
		t.Errorf("record outcomes %v, %d with an address, want answers 200, refused 100, other 100, 100 with an address", outcomes, withAddr)
	}
}

func TestRunCountsQueriesWithoutResponseAsLost(t *testing.T) {
	// A socket that never reads: every query times out.
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	mix, err := NewMix(MixConfig{Zone: "perf.test", Percent: [3]int{0, 100, 0}, RunID: "lost"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := Run(context.Background(), Config{
		Server:  pc.LocalAddr().String(),
		Phases:  []Phase{{Rate: 200, Duration: time.Second}},
		Timeout: 200 * time.Millisecond,
		Workers: 2,
		Source:  mix,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	sum := res.Phases[0].Summary
	if sum.Sent != 200 || sum.Lost != 200 || sum.Answered() != 0 {
		t.Errorf("sent %d lost %d answered %d, want 200 200 0", sum.Sent, sum.Lost, sum.Answered())
	}
}

func TestRunCountsDuplicateResponsesAsLate(t *testing.T) {
	addr := startRawServer(t, func(q []byte) [][]byte {
		q[2] |= 0x80
		return [][]byte{q, q}
	})
	mix, _ := NewMix(MixConfig{Zone: "perf.test", Percent: [3]int{0, 100, 0}, RunID: "dup"})
	res, err := Run(context.Background(), Config{
		Server: addr, Phases: []Phase{{Rate: 100, Duration: time.Second}},
		Timeout: 500 * time.Millisecond, Source: mix,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	sum := res.Phases[0].Summary
	if sum.Answered() != 100 || res.Late != 100 {
		t.Errorf("answered %d, late %d, want 100 and 100", sum.Answered(), res.Late)
	}
}

func TestRunDoesNotMatchAnotherQuestion(t *testing.T) {
	// The response has the query's ID but another question name: it is
	// unmatched and the query is lost.
	addr := startRawServer(t, func(q []byte) [][]byte {
		var m dns.Msg
		if m.Unpack(q) != nil {
			return nil
		}
		m.Question[0].Name = "other.perf.test."
		resp := new(dns.Msg)
		resp.SetReply(&m)
		out, _ := resp.Pack()
		return [][]byte{out}
	})
	mix, _ := NewMix(MixConfig{Zone: "perf.test", Percent: [3]int{0, 100, 0}, RunID: "q"})
	res, err := Run(context.Background(), Config{
		Server: addr, Phases: []Phase{{Rate: 100, Duration: time.Second}},
		Timeout: 300 * time.Millisecond, Source: mix,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	sum := res.Phases[0].Summary
	if sum.Answered() != 0 || sum.Lost != 100 || res.Late != 100 {
		t.Errorf("answered %d lost %d late %d, want 0 100 100", sum.Answered(), sum.Lost, res.Late)
	}
}

func TestQuestionMatches(t *testing.T) {
	m := new(dns.Msg)
	m.SetQuestion("Tracker.Example.com.", dns.TypeA)
	resp := new(dns.Msg)
	resp.SetRcode(m, dns.RcodeRefused)
	out, _ := resp.Pack()
	want, _ := packName("tracker.example.com")
	if !questionMatches(out, hashName(want)) {
		t.Error("same name in another case: want a match")
	}
	other, _ := packName("tracker.example.net")
	if questionMatches(out, hashName(other)) {
		t.Error("another name: want no match")
	}
	// No question: matched by ID alone.
	resp.Question = nil
	out, _ = resp.Pack()
	if !questionMatches(out, hashName(other)) {
		t.Error("no question: want a match")
	}
	// Truncated question.
	bad := []byte{0, 1, 0x81, 0x80, 0, 1, 0, 0, 0, 0, 0, 0, 7, 't'}
	if questionMatches(bad, hashName(want)) {
		t.Error("truncated question: want no match")
	}
}

// fakeConn is a socket whose writes fail with writeErr, or succeed and
// are dropped; reads block until Close.
type fakeConn struct {
	writeErr error
	closed   chan struct{}
	once     sync.Once
}

func newFakeConn(writeErr error) *fakeConn {
	return &fakeConn{writeErr: writeErr, closed: make(chan struct{})}
}

func (c *fakeConn) Read([]byte) (int, error) { <-c.closed; return 0, net.ErrClosed }
func (c *fakeConn) Write(b []byte) (int, error) {
	if c.writeErr != nil {
		return 0, c.writeErr
	}
	return len(b), nil
}
func (c *fakeConn) Close() error { c.once.Do(func() { close(c.closed) }); return nil }

func TestSendErrorsAreNotLost(t *testing.T) {
	mix, _ := NewMix(MixConfig{Zone: "perf.test", Percent: [3]int{0, 100, 0}, RunID: "e"})
	run := newRunState(NewSchedule([]Phase{{Rate: 1000, Duration: 100 * time.Millisecond}}), time.Second, 1, mix, true)
	run.start = time.Now()
	w := newWorker(run, 0, newFakeConn(errors.New("no buffer space available")))
	w.send(context.Background())
	s := run.rec.final()[0]
	if s.Sent != 100 || s.SendErrors != 100 || s.Lost != 0 {
		t.Errorf("sent %d, send errors %d, lost %d, want 100 100 0", s.Sent, s.SendErrors, s.Lost)
	}
	if run.records[0].Outcome != OutcomeSendError {
		t.Errorf("record outcome %d, want OutcomeSendError", run.records[0].Outcome)
	}
}

func TestEvictionWhenIDsRunOut(t *testing.T) {
	// One socket, 70,000 queries in flight within the timeout: the first
	// 4,464 are evicted to free their IDs, every query is lost in the end.
	mix, _ := NewMix(MixConfig{Zone: "perf.test", Percent: [3]int{0, 100, 0}, RunID: "x"})
	run := newRunState(NewSchedule([]Phase{{Rate: 70000, Duration: time.Second}}), 5*time.Second, 1, mix, false)
	run.start = time.Now()
	w := newWorker(run, 0, newFakeConn(nil))
	w.send(context.Background())
	sum := Summarize(run.rec.final(), 0, 2)
	if sum.Sent != 70000 || sum.Lost != 70000 {
		t.Errorf("sent %d lost %d, want 70000 and 70000", sum.Sent, sum.Lost)
	}
	if got := run.evicted.Load(); got != 70000-idsPerSocket {
		t.Errorf("evicted %d, want %d", got, 70000-idsPerSocket)
	}
}

func TestRunRejectsTooFewWorkers(t *testing.T) {
	_, err := Run(context.Background(), Config{
		Server:  "127.0.0.1:53",
		Phases:  []Phase{{Rate: 100000, Duration: time.Second}},
		Timeout: 5 * time.Second,
		Workers: 2,
		Source:  testMix(t),
	}, nil)
	if err == nil {
		t.Error("2 workers at 100,000 qps with a 5 s timeout: want an error")
	}
}

func TestWarm(t *testing.T) {
	addr := startServer(t, func(i int64, req *dns.Msg) *dns.Msg {
		switch i {
		case 1:
			return withRcode(req, dns.RcodeRefused)
		case 2:
			return withRcode(req, dns.RcodeServerFailure)
		default:
			return answerA(req)
		}
	})
	answered, err := Warm(context.Background(), addr, []string{"a.perf.test", "b.perf.test", "c.perf.test", "d.perf.test"}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if answered != 2 {
		t.Errorf("Warm answered %d, want 2", answered)
	}
}
