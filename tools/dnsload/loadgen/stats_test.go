package loadgen

import (
	"testing"
	"time"

	"github.com/miekg/dns"
)

func TestRecorderBucketsBySendSecond(t *testing.T) {
	r := newRecorder(3 * time.Second)
	// Sent in second 0, answered in second 1: counted in second 0.
	r.sent(900 * time.Millisecond)
	r.response(900*time.Millisecond, 200*time.Millisecond, dns.RcodeSuccess)
	// Second 1: one REFUSED, one NXDOMAIN, one SERVFAIL, one lost.
	for range 4 {
		r.sent(1500 * time.Millisecond)
	}
	r.response(1500*time.Millisecond, time.Millisecond, dns.RcodeRefused)
	r.response(1500*time.Millisecond, 2*time.Millisecond, dns.RcodeNameError)
	r.response(1500*time.Millisecond, 3*time.Millisecond, dns.RcodeServerFailure)
	r.lost(1500 * time.Millisecond)
	// Past the end: clamped to the last second.
	r.sent(10 * time.Second)

	s0 := r.second(0)
	if s0.Sent != 1 || s0.Answered() != 1 || s0.Lost != 0 {
		t.Errorf("second 0: %+v", s0)
	}
	if p := s0.Ans.Quantile(0.5); p < 200*time.Millisecond || p > 207*time.Millisecond {
		t.Errorf("second 0: answer p50 %v, want 200ms", p)
	}
	s1 := r.second(1)
	if s1.Sent != 4 || s1.Answered() != 2 || s1.Refused() != 1 || s1.Other() != 1 || s1.Lost != 1 {
		t.Errorf("second 1: sent %d answered %d refused %d other %d lost %d, want 4 2 1 1 1",
			s1.Sent, s1.Answered(), s1.Refused(), s1.Other(), s1.Lost)
	}
	if s1.Ans.Count() != 2 || s1.All.Count() != 3 {
		t.Errorf("second 1: %d answer and %d response latencies, want 2 and 3", s1.Ans.Count(), s1.All.Count())
	}
	if got := r.second(3).Sent; got != 1 {
		t.Errorf("last second: sent %d, want 1", got)
	}
}

func TestSummarize(t *testing.T) {
	seconds := make([]Second, 125)
	for i := range seconds {
		s := &seconds[i]
		s.Index = i
		s.Sent = 2000
		s.Rcodes[dns.RcodeSuccess] = 900
		s.Rcodes[dns.RcodeRefused] = 1090
		s.Lost = 10
		for range 900 {
			s.Ans.Add(10 * time.Millisecond)
			s.All.Add(10 * time.Millisecond)
		}
		for range 1090 {
			s.All.Add(time.Millisecond)
		}
	}
	// Second 0 is the burst second; it must not count.
	seconds[0].Rcodes[dns.RcodeSuccess] = 2000
	seconds[0].Rcodes[dns.RcodeRefused] = 0
	seconds[0].Lost = 0
	// One slow minute: second 70 answers less.
	seconds[70].Rcodes[dns.RcodeSuccess] = 300
	seconds[70].Rcodes[dns.RcodeRefused] = 1690

	sum := Summarize(seconds, 1, 125)
	if sum.Seconds != 124 {
		t.Fatalf("Seconds = %d, want 124", sum.Seconds)
	}
	if sum.Sent != 124*2000 {
		t.Errorf("Sent = %d", sum.Sent)
	}
	wantAnswered := int64(123*900 + 300)
	if sum.Answered != wantAnswered {
		t.Errorf("Answered = %d, want %d", sum.Answered, wantAnswered)
	}
	if got, want := sum.AnsweredPerSecond(), float64(wantAnswered)/124; got != want {
		t.Errorf("AnsweredPerSecond = %v, want %v", got, want)
	}
	if sum.MinSecondAnswered != 300 || sum.MinSecond != 70 {
		t.Errorf("min second = %d answered in second %d, want 300 in 70", sum.MinSecondAnswered, sum.MinSecond)
	}
	// Minutes are counted from the window start: [1,61), [61,121), [121,125).
	if got, want := sum.MinMinuteAnsweredPerSecond, float64(59*900+300)/60; got != want {
		t.Errorf("MinMinuteAnsweredPerSecond = %v, want %v", got, want)
	}
	if got := sum.PercentLost(); got != 0.5 {
		t.Errorf("PercentLost = %v, want 0.5", got)
	}
	if got := sum.PercentAnsweredOrRefused(); got != 99.5 {
		t.Errorf("PercentAnsweredOrRefused = %v, want 99.5", got)
	}
	if p := sum.Ans.Quantile(0.99); p < 10*time.Millisecond || p > 11*time.Millisecond {
		t.Errorf("answer p99 = %v, want 10ms", p)
	}
	// 45 % of the responses are answers at 10 ms: p99.9 of all is 10 ms,
	// p50 of all is a refusal at 1 ms.
	if p := sum.All.Quantile(0.999); p < 10*time.Millisecond || p > 11*time.Millisecond {
		t.Errorf("all p99.9 = %v, want 10ms", p)
	}
	if p := sum.All.Quantile(0.5); p > 1100*time.Microsecond {
		t.Errorf("all p50 = %v, want 1ms", p)
	}
}

func TestSummarizeEmptyWindow(t *testing.T) {
	sum := Summarize(make([]Second, 3), 2, 2)
	if sum.Seconds != 0 || sum.AnsweredPerSecond() != 0 || sum.PercentLost() != 0 {
		t.Errorf("empty window: %+v", sum)
	}
}
