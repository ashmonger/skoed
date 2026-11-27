package loadgen

import (
	"testing"
	"time"
)

func TestParsePhases(t *testing.T) {
	got, err := ParsePhases("40000:60s,5000:2m:5s", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	want := []Phase{
		{Rate: 40000, Duration: 60 * time.Second, Settle: time.Second},
		{Rate: 5000, Duration: 2 * time.Minute, Settle: 5 * time.Second},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d phases, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("phase %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
	for _, bad := range []string{"", "1000", "x:1s", "1000:x", "-1:1s", "1000:0s", "1000:1s:x", "1000:1s:2s"} {
		if _, err := ParsePhases(bad, 0); err == nil {
			t.Errorf("ParsePhases(%q): want an error", bad)
		}
	}
}

func TestScheduleEvenSpacing(t *testing.T) {
	s := NewSchedule([]Phase{{Rate: 1000, Duration: 2 * time.Second}})
	if s.Total() != 2000 {
		t.Fatalf("Total = %d, want 2000", s.Total())
	}
	for n := int64(1); n < s.Total(); n++ {
		if gap := s.At(n) - s.At(n-1); gap != time.Millisecond {
			t.Fatalf("gap before query %d = %v, want 1ms", n, gap)
		}
	}
	if s.Duration() != 2*time.Second {
		t.Errorf("Duration = %v, want 2s", s.Duration())
	}
}

func TestSchedulePhases(t *testing.T) {
	// 2 × C for 3 s, then 25 % of C for 4 s, with C = 1,000.
	s := NewSchedule([]Phase{
		{Rate: 2000, Duration: 3 * time.Second},
		{Rate: 250, Duration: 4 * time.Second},
	})
	if s.Total() != 6000+1000 {
		t.Fatalf("Total = %d, want 7000", s.Total())
	}
	if got := s.At(6000); got != 3*time.Second {
		t.Errorf("first query of phase 2 at %v, want 3s", got)
	}
	if got := s.At(6001) - s.At(6000); got != 4*time.Millisecond {
		t.Errorf("phase 2 gap = %v, want 4ms", got)
	}
	if got := s.Due(2 * time.Second); got != 4001 {
		t.Errorf("Due(2s) = %d, want 4001", got)
	}
	if got := s.Due(3*time.Second + time.Second/2); got != 6000+126 {
		t.Errorf("Due(3.5s) = %d, want 6126", got)
	}
	if got := s.Due(time.Hour); got != s.Total() {
		t.Errorf("Due(1h) = %d, want %d", got, s.Total())
	}
	if got := s.Due(-time.Second); got != 0 {
		t.Errorf("Due(-1s) = %d, want 0", got)
	}
	if got := s.PhaseStart(1); got != 3*time.Second {
		t.Errorf("PhaseStart(1) = %v, want 3s", got)
	}
}

func TestScheduleDueMatchesAt(t *testing.T) {
	s := NewSchedule([]Phase{
		{Rate: 3333, Duration: 1500 * time.Millisecond},
		{Rate: 0, Duration: time.Second},
		{Rate: 777, Duration: 2 * time.Second},
	})
	// Due(t) is the number of queries whose send time is at or before t.
	for tt := time.Duration(0); tt < s.Duration()+time.Second; tt += 137 * time.Microsecond {
		due := s.Due(tt)
		if due > 0 && s.At(due-1) > tt {
			t.Fatalf("Due(%v) = %d but query %d is at %v", tt, due, due-1, s.At(due-1))
		}
		if due < s.Total() && s.At(due) <= tt {
			t.Fatalf("Due(%v) = %d but query %d is already due at %v", tt, due, due, s.At(due))
		}
	}
}

func TestWorkersShareTheScheduleEvenly(t *testing.T) {
	// Worker w sends the queries n with n % workers == w: over any second
	// the workers together send the phase rate, each its share.
	s := NewSchedule([]Phase{{Rate: 1000, Duration: 3 * time.Second}})
	const workers = 7
	perSecond := make([]int, 3)
	perWorker := make([]int, workers)
	for n := range s.Total() {
		perSecond[s.At(n)/time.Second]++
		perWorker[n%workers]++
	}
	for i, c := range perSecond {
		if c != 1000 {
			t.Errorf("second %d: %d queries, want 1000", i, c)
		}
	}
	for w, c := range perWorker {
		if c < 428 || c > 429 {
			t.Errorf("worker %d: %d queries, want 428 or 429", w, c)
		}
	}
}

func TestDefaultWorkers(t *testing.T) {
	// 100,000 qps with a 5 s timeout keeps up to 500,000 queries in
	// flight; each socket must reuse a DNS ID no sooner than twice the
	// timeout, so at least 500,000 × 2 / 65,536 = 16 sockets.
	if got := DefaultWorkers(100000, 5*time.Second); got != 16 {
		t.Errorf("DefaultWorkers(100000, 5s) = %d, want 16", got)
	}
	if got := DefaultWorkers(100, time.Second); got != 4 {
		t.Errorf("DefaultWorkers(100, 1s) = %d, want 4", got)
	}
}
