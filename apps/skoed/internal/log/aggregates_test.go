package log

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/skoed/skoed/internal/cluster"
)

// fakeCommitter records the aggregates it is given. failNext makes the
// next commits fail; duringCommit, when set, runs inside the commit call so
// a test can observe queries while a flush is in progress.
type fakeCommitter struct {
	mu           sync.Mutex
	committed    []cluster.HourAggregate
	calls        int
	failNext     int
	duringCommit func()
}

func (f *fakeCommitter) CommitHourlyAggregate(agg cluster.HourAggregate) error {
	f.mu.Lock()
	f.calls++
	hook := f.duringCommit
	fail := f.failNext > 0
	if fail {
		f.failNext--
	}
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	if fail {
		return errors.New("raft busy")
	}
	f.mu.Lock()
	f.committed = append(f.committed, agg)
	f.mu.Unlock()
	return nil
}

func (f *fakeCommitter) IsLeader() bool { return true }

func (f *fakeCommitter) snapshot() (calls int, committed []cluster.HourAggregate) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls, append([]cluster.HourAggregate(nil), f.committed...)
}

// bucketState returns a copy of the fields of the current bucket that the
// tests check, read under the lock.
func (a *Aggregator) bucketState() (exists bool, hourStart, openedAt time.Time, total int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.bucket == nil {
		return false, time.Time{}, time.Time{}, 0
	}
	return true, a.bucket.hourStart, a.bucket.openedAt, a.bucket.total
}

func sumCounts(ncs []cluster.NameCount) int {
	n := 0
	for _, nc := range ncs {
		n += nc.Count
	}
	return n
}

// observeAndFlushConcurrently runs observers goroutines calling Observe
// perObserver times each, with distinct domains and clients, while one
// goroutine calls tryFlush in a loop, as the flusher does on every tick
// under load. A final stop flush then commits what is left.
func observeAndFlushConcurrently(t *testing.T, a *Aggregator, observers, perObserver int) {
	t.Helper()
	stopFlush := make(chan struct{})
	flushDone := make(chan struct{})
	go func() {
		defer close(flushDone)
		for {
			select {
			case <-stopFlush:
				return
			default:
				a.tryFlush(time.Now(), false)
			}
		}
	}()

	var wg sync.WaitGroup
	for g := 0; g < observers; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perObserver; i++ {
				a.Observe(fmt.Sprintf("10.0.%d.%d", g, i%50), fmt.Sprintf("d%d-%d.example.com", g, i%50), OutcomeForwarded)
			}
		}(g)
	}
	wg.Wait()
	close(stopFlush)
	<-flushDone
	a.tryFlush(time.Now(), true)
}

// The flusher must not read the bucket maps while Observe writes them.
// Before the fix, this test fails under -race (or the runtime aborts with
// "concurrent map iteration and map write").
func TestAggregatorFlushDuringObserveIsRaceFree(t *testing.T) {
	fc := &fakeCommitter{}
	a := NewAggregator(AggregatorConfig{NodeID: "n1", FlushInterval: time.Nanosecond, TopN: 20}, fc)

	observeAndFlushConcurrently(t, a, 8, 2000)

	if calls, _ := fc.snapshot(); calls == 0 {
		t.Fatal("no aggregate was committed")
	}
}

// Every observation ends up in exactly one committed aggregate, including
// the ones recorded while a commit is in progress.
func TestAggregatorConcurrentFlushLosesNoObservation(t *testing.T) {
	const observers, perObserver = 8, 2000
	fc := &fakeCommitter{}
	// TopN above the number of distinct names, so the per-name counts of
	// the committed aggregates add up to the totals.
	a := NewAggregator(AggregatorConfig{NodeID: "n1", FlushInterval: time.Nanosecond, TopN: observers * 50}, fc)

	observeAndFlushConcurrently(t, a, observers, perObserver)

	_, committed := fc.snapshot()
	var total, forwarded, domains, clients int
	for _, agg := range committed {
		total += agg.Total
		forwarded += agg.Forwarded
		domains += sumCounts(agg.TopDomains)
		clients += sumCounts(agg.TopClients)
	}
	_, _, _, remaining := a.bucketState()
	want := observers * perObserver
	if total+remaining != want {
		t.Errorf("committed total %d + remaining %d = %d, want %d (%d commits)", total, remaining, total+remaining, want, len(committed))
	}
	if remaining != 0 {
		t.Errorf("%d observations left in the bucket after the stop flush", remaining)
	}
	if forwarded != total || domains != total || clients != total {
		t.Errorf("forwarded=%d domains=%d clients=%d, want each = total %d", forwarded, domains, clients, total)
	}
}

// An observation recorded while a successful commit is in progress is kept
// for the next flush instead of being discarded with the committed bucket.
func TestAggregatorObservationDuringSuccessfulCommitIsKept(t *testing.T) {
	fc := &fakeCommitter{}
	a := NewAggregator(AggregatorConfig{NodeID: "n1", FlushInterval: time.Minute, TopN: 20}, fc)

	a.Observe("10.0.0.1", "a.example.com", OutcomeForwarded)
	a.Observe("10.0.0.1", "a.example.com", OutcomeForwarded)
	_, hourStart, openedAt, _ := a.bucketState()

	fc.duringCommit = func() { a.Observe("10.0.0.2", "b.example.com", OutcomeBlocked) }
	a.tryFlush(openedAt.Add(time.Minute), false)
	fc.duringCommit = nil

	_, committed := fc.snapshot()
	if len(committed) != 1 || committed[0].Total != 2 || committed[0].HourStart != hourStart.Unix() {
		t.Fatalf("first commit = %+v, want one aggregate with total 2 for hour %d", committed, hourStart.Unix())
	}
	exists, _, _, total := a.bucketState()
	if !exists || total != 1 {
		t.Fatalf("bucket after commit: exists=%v total=%d, want the observation made during the commit", exists, total)
	}

	a.tryFlush(time.Now(), true)
	_, committed = fc.snapshot()
	if len(committed) != 2 || committed[1].Total != 1 || committed[1].Blocked != 1 {
		t.Fatalf("second commit = %+v, want total 1, blocked 1", committed[1:])
	}
}

// Counters observed before a failed commit, and while it was in progress,
// are part of the next successful commit, which keeps the hour of the
// first observation even when the retry happens after the hour boundary.
func TestAggregatorFailedCommitIsRetriedWithAllCounters(t *testing.T) {
	fc := &fakeCommitter{failNext: 1}
	a := NewAggregator(AggregatorConfig{NodeID: "n1", FlushInterval: time.Minute, TopN: 20}, fc)

	for i := 0; i < 3; i++ {
		a.Observe("10.0.0.1", "a.example.com", OutcomeForwarded)
	}
	_, hourStart, openedAt, _ := a.bucketState()

	// The commit fails, and one query is observed while it runs.
	fc.duringCommit = func() { a.Observe("10.0.0.2", "b.example.com", OutcomeBlocked) }
	failedAt := openedAt.Add(time.Minute)
	a.tryFlush(failedAt, false)
	fc.duringCommit = nil

	exists, gotHour, gotOpened, total := a.bucketState()
	if !exists || total != 4 || !gotHour.Equal(hourStart) || !gotOpened.Equal(openedAt) {
		t.Fatalf("bucket after failed commit: exists=%v total=%d hourStart=%v openedAt=%v, want total 4, hourStart %v, openedAt %v",
			exists, total, gotHour, gotOpened, hourStart, openedAt)
	}

	a.Observe("10.0.0.1", "a.example.com", OutcomeCached)

	// No retry before the backoff.
	a.tryFlush(failedAt.Add(retryBackoff-time.Second), false)
	if calls, _ := fc.snapshot(); calls != 1 {
		t.Fatalf("commit calls = %d before the backoff, want 1", calls)
	}

	// The retry happens in the next hour.
	retryAt := hourStart.Add(time.Hour).Add(retryBackoff)
	a.tryFlush(retryAt, false)

	_, committed := fc.snapshot()
	if len(committed) != 1 {
		t.Fatalf("committed %d aggregates, want 1", len(committed))
	}
	agg := committed[0]
	if agg.HourStart != hourStart.Unix() {
		t.Errorf("HourStart = %d, want %d", agg.HourStart, hourStart.Unix())
	}
	if agg.Total != 5 || agg.Forwarded != 3 || agg.Blocked != 1 || agg.Cached != 1 {
		t.Errorf("counters = total %d forwarded %d blocked %d cached %d, want 5/3/1/1", agg.Total, agg.Forwarded, agg.Blocked, agg.Cached)
	}
	wantDomains := []cluster.NameCount{{Name: "a.example.com", Count: 4}, {Name: "b.example.com", Count: 1}}
	if fmt.Sprint(agg.TopDomains) != fmt.Sprint(wantDomains) {
		t.Errorf("TopDomains = %v, want %v", agg.TopDomains, wantDomains)
	}
	if exists, _, _, _ := a.bucketState(); exists {
		t.Error("bucket still present after the successful retry")
	}
	a.mu.Lock()
	retry := a.nextRetryAfter
	a.mu.Unlock()
	if !retry.IsZero() {
		t.Errorf("nextRetryAfter = %v after a successful commit, want zero", retry)
	}
}

// A failed final flush on stop keeps the counters in the bucket, as a
// failed periodic flush does.
func TestAggregatorFailedStopFlushKeepsCounters(t *testing.T) {
	fc := &fakeCommitter{failNext: 1}
	a := NewAggregator(AggregatorConfig{NodeID: "n1", FlushInterval: time.Minute, TopN: 20}, fc)
	a.Observe("10.0.0.1", "a.example.com", OutcomeLocal)
	_, hourStart, _, _ := a.bucketState()

	a.tryFlush(time.Now(), true)

	exists, gotHour, _, total := a.bucketState()
	if !exists || total != 1 || !gotHour.Equal(hourStart) {
		t.Fatalf("bucket after failed stop flush: exists=%v total=%d hourStart=%v, want total 1, hourStart %v", exists, total, gotHour, hourStart)
	}
}
