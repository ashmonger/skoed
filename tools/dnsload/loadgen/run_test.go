package loadgen

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// startRefuseEverySecond starts an in-process miekg/dns server on a
// loopback UDP port that answers odd queries with an A record and even
// queries with REFUSED.
func startRefuseEverySecond(t *testing.T) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var count atomic.Int64
	handler := dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
		resp := new(dns.Msg)
		if count.Add(1)%2 == 0 {
			resp.SetRcode(req, dns.RcodeRefused)
		} else {
			resp.SetReply(req)
			resp.Answer = append(resp.Answer, &dns.A{
				Hdr: dns.RR_Header{Name: req.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
				A:   net.IPv4(192, 0, 2, 1),
			})
		}
		_ = w.WriteMsg(resp)
	})
	started := make(chan struct{})
	srv := &dns.Server{PacketConn: pc, Handler: handler, NotifyStartedFunc: func() { close(started) }}
	go func() { _ = srv.ActivateAndServe() }()
	<-started
	t.Cleanup(func() { _ = srv.Shutdown() })
	return pc.LocalAddr().String()
}

func TestRunAgainstServerRefusingEverySecondQuery(t *testing.T) {
	addr := startRefuseEverySecond(t)
	mix := testMix(t)
	var mu sync.Mutex
	var rows []Second
	res, err := Run(context.Background(), Config{
		Server:  addr,
		Phases:  []Phase{{Rate: 400, Duration: 2 * time.Second}, {Rate: 100, Duration: time.Second}},
		Timeout: 500 * time.Millisecond,
		Workers: 3,
		Mix:     mix,
		Seed:    1,
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
	if tot.Lost != 0 || res.Late != 0 {
		t.Errorf("lost %d, late %d, want 0 and 0", tot.Lost, res.Late)
	}
	if tot.Refused() != 450 || tot.Answered != 450 {
		t.Errorf("refused %d answered %d, want 450 and 450", tot.Refused(), tot.Answered)
	}
	if tot.Ans.Count() != 450 || tot.All.Count() != 900 {
		t.Errorf("latencies: %d answers, %d responses, want 450 and 900", tot.Ans.Count(), tot.All.Count())
	}
	// Queries count in the second they were actually sent, so a query
	// sent a little late near the end of a second moves to the next one.
	near := func(got, want int64) bool { return got >= want-5 && got <= want+5 }
	if len(res.Phases) != 2 || !near(res.Phases[0].Summary.Sent, 800) || !near(res.Phases[1].Summary.Sent, 100) {
		t.Errorf("phases: sent %d and %d, want about 800 and 100", res.Phases[0].Summary.Sent, res.Phases[1].Summary.Sent)
	}
	// Per-second rows: one per second of the run, in order, each sent
	// at the rate of its phase.
	mu.Lock()
	defer mu.Unlock()
	if len(rows) != 3 {
		t.Fatalf("%d per-second rows, want 3", len(rows))
	}
	for i, want := range []int64{400, 400, 100} {
		if rows[i].Index != i || !near(rows[i].Sent, want) {
			t.Errorf("row %d: index %d sent %d, want %d and about %d", i, rows[i].Index, rows[i].Sent, i, want)
		}
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
		Mix:     mix,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	sum := res.Phases[0].Summary
	if sum.Sent != 200 || sum.Lost != 200 || sum.Answered != 0 {
		t.Errorf("sent %d lost %d answered %d, want 200 200 0", sum.Sent, sum.Lost, sum.Answered)
	}
}

func TestRunRejectsTooFewWorkers(t *testing.T) {
	_, err := Run(context.Background(), Config{
		Server:  "127.0.0.1:53",
		Phases:  []Phase{{Rate: 100000, Duration: time.Second}},
		Timeout: 5 * time.Second,
		Workers: 2,
		Mix:     testMix(t),
	}, nil)
	if err == nil {
		t.Error("2 workers at 100,000 qps with a 5 s timeout: want an error")
	}
}

func TestWarm(t *testing.T) {
	addr := startRefuseEverySecond(t)
	answered, err := Warm(context.Background(), addr, []string{"a.perf.test", "b.perf.test", "c.perf.test", "d.perf.test"}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if answered != 2 {
		t.Errorf("Warm answered %d, want 2", answered)
	}
}
