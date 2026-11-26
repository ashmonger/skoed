package log

import (
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"
	"time"
)

// refLog is a copy of the slice-shifting QueryLog storage that preceded the
// ring buffer. It is the reference model the ring buffer must match.
type refLog struct {
	entries    []Entry
	maxEntries int
}

func newRefLog(maxEntries int) *refLog {
	if maxEntries < 0 {
		maxEntries = 0
	}
	return &refLog{entries: make([]Entry, 0, maxEntries), maxEntries: maxEntries}
}

func (l *refLog) Append(e Entry) {
	if l.maxEntries > 0 {
		if len(l.entries) >= l.maxEntries {
			copy(l.entries, l.entries[1:])
			l.entries = l.entries[:len(l.entries)-1]
		}
		l.entries = append(l.entries, e)
	}
}

func (l *refLog) Query(client, outcome string, limit, offset int) ([]Entry, int) {
	snapshot := make([]Entry, len(l.entries))
	copy(snapshot, l.entries)
	matched := make([]Entry, 0, len(snapshot))
	for i := len(snapshot) - 1; i >= 0; i-- {
		e := snapshot[i]
		if client != "" && e.Client != client {
			continue
		}
		if outcome != "" && string(e.Outcome) != outcome {
			continue
		}
		matched = append(matched, e)
	}
	total := len(matched)
	if offset > 0 {
		if offset >= len(matched) {
			return []Entry{}, total
		}
		matched = matched[offset:]
	}
	if limit > 0 && len(matched) > limit {
		matched = matched[:limit]
	}
	return matched, total
}

func (l *refLog) Snapshot(n int) []Entry {
	snap := make([]Entry, len(l.entries))
	copy(snap, l.entries)
	if n <= 0 || n >= len(snap) {
		return snap
	}
	return snap[len(snap)-n:]
}

func (l *refLog) SetMaxEntries(n int) {
	if n < 0 {
		n = 0
	}
	l.maxEntries = n
	if len(l.entries) > n {
		l.entries = l.entries[len(l.entries)-n:]
	}
}

var (
	testClients  = []string{"", "10.0.0.1", "10.0.0.2", "10.0.0.3"}
	testOutcomes = []string{"", string(OutcomeForwarded), string(OutcomeBlocked), string(OutcomeLocal), string(OutcomeCached)}
)

// TestQueryLogMatchesReference runs random sequences of Append,
// SetMaxEntries, Query, Snapshot and MaxEntries against QueryLog and the
// reference model and requires identical results, nil-ness included.
func TestQueryLogMatchesReference(t *testing.T) {
	for seed := int64(1); seed <= 200; seed++ {
		rng := rand.New(rand.NewSource(seed))
		initial := rng.Intn(12) - 2 // -2..9: covers negative and zero
		got, want := New(initial), newRefLog(initial)
		seq := 0
		for step := 0; step < 400; step++ {
			switch op := rng.Intn(20); {
			case op < 12:
				seq++
				e := Entry{
					ID:        fmt.Sprintf("id-%d", seq),
					Timestamp: time.Unix(int64(seq), 0),
					Client:    testClients[1+rng.Intn(len(testClients)-1)],
					Domain:    fmt.Sprintf("d%d.example", seq),
					Outcome:   Outcome(testOutcomes[1+rng.Intn(len(testOutcomes)-1)]),
				}
				got.Append(e)
				want.Append(e)
			case op < 14:
				n := rng.Intn(14) - 2
				got.SetMaxEntries(n)
				want.SetMaxEntries(n)
				if got.MaxEntries() != want.maxEntries {
					t.Fatalf("seed %d step %d: MaxEntries = %d, want %d", seed, step, got.MaxEntries(), want.maxEntries)
				}
			case op < 17:
				c := testClients[rng.Intn(len(testClients))]
				o := testOutcomes[rng.Intn(len(testOutcomes))]
				limit, offset := rng.Intn(8)-1, rng.Intn(10)-1
				ge, gt := got.Query(c, o, limit, offset)
				we, wt := want.Query(c, o, limit, offset)
				if gt != wt || !reflect.DeepEqual(ge, we) {
					t.Fatalf("seed %d step %d: Query(%q, %q, %d, %d) = %v, %d; want %v, %d",
						seed, step, c, o, limit, offset, ge, gt, we, wt)
				}
			default:
				n := rng.Intn(14) - 2
				if gs, ws := got.Snapshot(n), want.Snapshot(n); !reflect.DeepEqual(gs, ws) {
					t.Fatalf("seed %d step %d: Snapshot(%d) = %v, want %v", seed, step, n, gs, ws)
				}
			}
		}
	}
}

func TestAppendAssignsID(t *testing.T) {
	l := New(2)
	l.Append(Entry{Domain: "a.example"})
	l.Append(Entry{ID: "given", Domain: "b.example"})
	snap := l.Snapshot(0)
	if len(snap) != 2 {
		t.Fatalf("len = %d, want 2", len(snap))
	}
	if len(snap[0].ID) != 32 {
		t.Errorf("generated ID = %q, want 32 hex chars", snap[0].ID)
	}
	if snap[1].ID != "given" {
		t.Errorf("ID = %q, want the given one", snap[1].ID)
	}
}

func TestZeroMaxEntriesStillNotifies(t *testing.T) {
	l := New(0)
	var observed []string
	l.SetObserver(func(e Entry) { observed = append(observed, e.Domain) })
	id, ch := l.Subscribe()
	l.Append(Entry{Domain: "a.example"})
	if got := l.Snapshot(0); len(got) != 0 || got == nil {
		t.Fatalf("Snapshot = %#v, want empty non-nil", got)
	}
	if len(observed) != 1 || observed[0] != "a.example" {
		t.Fatalf("observer saw %v", observed)
	}
	if e := <-ch; e.Domain != "a.example" || e.ID == "" {
		t.Fatalf("subscriber got %+v", e)
	}
	l.Unsubscribe(id)
	if _, ok := <-ch; ok {
		t.Fatal("channel still open after Unsubscribe")
	}
}

func TestAppendWithoutSubscribersDoesNotAllocate(t *testing.T) {
	l := New(100)
	e := Entry{ID: "fixed"}
	for i := 0; i < 200; i++ {
		l.Append(e)
	}
	if n := testing.AllocsPerRun(1000, func() { l.Append(e) }); n != 0 {
		t.Fatalf("Append on a full log allocates %v times per call, want 0", n)
	}
}

// TestQueryLogConcurrent is meant for -race: appenders, readers, and
// subscribers run at the same time.
func TestQueryLogConcurrent(t *testing.T) {
	l := New(500)
	var appenders, others sync.WaitGroup
	stop := make(chan struct{})
	for w := 0; w < 4; w++ {
		appenders.Add(1)
		go func(w int) {
			defer appenders.Done()
			for i := 0; i < 2000; i++ {
				l.Append(Entry{Client: testClients[1+w%3], Outcome: OutcomeForwarded})
			}
		}(w)
	}
	for r := 0; r < 2; r++ {
		others.Add(1)
		go func() {
			defer others.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				l.Query("10.0.0.1", "", 10, 2)
				l.Snapshot(50)
				l.MaxEntries()
			}
		}()
	}
	others.Add(1)
	go func() {
		defer others.Done()
		for i := 0; i < 20; i++ {
			l.SetMaxEntries(300 + i*20)
		}
	}()
	var ids []uint64
	var chans []<-chan Entry
	for i := 0; i < 8; i++ {
		id, ch := l.Subscribe()
		ids = append(ids, id)
		chans = append(chans, ch)
		others.Add(1)
		go func(ch <-chan Entry) {
			defer others.Done()
			for range ch {
			}
		}(ch)
	}
	appenders.Wait()
	close(stop)
	for _, id := range ids {
		l.Unsubscribe(id)
	}
	others.Wait()
	if got := len(l.Snapshot(0)); got == 0 || got > l.MaxEntries() {
		t.Fatalf("len = %d, want 1..%d", got, l.MaxEntries())
	}
}

func BenchmarkAppendFull(b *testing.B) {
	l := New(10000)
	e := Entry{ID: "fixed", Client: "10.0.0.1", Domain: "example.com", QueryType: "A", Outcome: OutcomeForwarded}
	for i := 0; i < 10000; i++ {
		l.Append(e)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l.Append(e)
	}
}
