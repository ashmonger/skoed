package loadgen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/miekg/dns"
)

func testMix(t *testing.T) *Mix {
	t.Helper()
	m, err := NewMix(MixConfig{
		Cached:  GenerateCached("perf.test", 1000),
		Blocked: []string{"tracker.example.com", "ads.example.net"},
		Zone:    "perf.test",
		Percent: [3]int{70, 20, 10},
		RunID:   "r1",
		Seed:    1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestMixProportions(t *testing.T) {
	m := testMix(t)
	var counts [3]int
	const n = 100_000
	for i := range int64(n) {
		counts[m.Query(i).Kind]++
	}
	for kind, want := range [3]int{70, 20, 10} {
		got := float64(counts[kind]) * 100 / n
		if got < float64(want)-0.5 || got > float64(want)+0.5 {
			t.Errorf("kind %d: %.2f %%, want %d %% ± 0.5", kind, got, want)
		}
	}
}

func TestMixNames(t *testing.T) {
	m := testMix(t)
	cached := map[string]bool{}
	for _, name := range GenerateCached("perf.test", 1000) {
		cached[dns.Fqdn(name)] = true
	}
	misses := map[string]bool{}
	for i := range int64(20_000) {
		q := m.Query(i)
		wire, kind := q.Name, q.Kind
		if string(m.Query(i).Name) != string(wire) {
			t.Fatalf("query %d: two calls give two names", i)
		}
		name, _, err := dns.UnpackDomainName(wire, 0)
		if err != nil {
			t.Fatalf("query %d: name does not unpack: %v", i, err)
		}
		switch kind {
		case KindCached:
			if !cached[name] {
				t.Fatalf("cached query %d: %q is not in the cached set", i, name)
			}
		case KindMiss:
			if !strings.HasSuffix(name, ".perf.test.") || cached[name] {
				t.Fatalf("miss query %d: %q", i, name)
			}
			if misses[name] {
				t.Fatalf("miss query %d: %q sent twice", i, name)
			}
			misses[name] = true
		case KindBlocked:
			if name != "tracker.example.com." && name != "ads.example.net." {
				t.Fatalf("blocked query %d: %q", i, name)
			}
		}
	}
}

func TestMixRunsDoNotShareMissNames(t *testing.T) {
	a, _ := NewMix(MixConfig{Zone: "perf.test", Percent: [3]int{0, 100, 0}, RunID: "a"})
	b, _ := NewMix(MixConfig{Zone: "perf.test", Percent: [3]int{0, 100, 0}, RunID: "b"})
	if string(a.Query(7).Name) == string(b.Query(7).Name) {
		t.Errorf("two runs sent the same miss name")
	}
}

func TestNewMixValidates(t *testing.T) {
	for _, c := range []MixConfig{
		{Zone: "perf.test", Percent: [3]int{70, 20, 20}},
		{Zone: "perf.test", Percent: [3]int{70, 20, 10}, Cached: []string{"a.perf.test"}},
		{Zone: "perf.test", Percent: [3]int{70, 30, 0}},
		{Zone: "", Percent: [3]int{0, 100, 0}},
		{Zone: "perf.test", Percent: [3]int{100, 0, 0}, Cached: []string{"bad..name"}},
	} {
		if _, err := NewMix(c); err == nil {
			t.Errorf("NewMix(%+v): want an error", c)
		}
	}
}

func TestAppendQuery(t *testing.T) {
	m := testMix(t)
	pkt := AppendQuery(nil, 0xbeef, m.Query(0).Name)
	var msg dns.Msg
	if err := msg.Unpack(pkt); err != nil {
		t.Fatal(err)
	}
	if msg.Id != 0xbeef || msg.Response || !msg.RecursionDesired || msg.Opcode != dns.OpcodeQuery {
		t.Errorf("header: %+v", msg.MsgHdr)
	}
	if len(msg.Question) != 1 || msg.Question[0].Qtype != dns.TypeA || msg.Question[0].Qclass != dns.ClassINET {
		t.Errorf("question: %+v", msg.Question)
	}
}

func TestReadNames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ads.txt")
	content := "# list\n\ntracker.example.com\n0.0.0.0 ads.example.net\n  spaced.example.org  \n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadNames(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"tracker.example.com", "ads.example.net", "spaced.example.org"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("ReadNames = %v, want %v", got, want)
	}
}

func TestSourceFunc(t *testing.T) {
	src := SourceFunc(func(n int64) (string, Kind) {
		if n == 1 {
			return "bad..name", KindMiss
		}
		return "tracker.example.com", KindBlocked
	})
	q := src.Query(0)
	name, _, err := dns.UnpackDomainName(q.Name, 0)
	if err != nil || name != "tracker.example.com." || q.Kind != KindBlocked {
		t.Errorf("query 0: %q %d %v", name, q.Kind, err)
	}
	if q := src.Query(1); q.Name != nil || q.Kind != KindMiss {
		t.Errorf("query 1: invalid name gave %+v, want no name", q)
	}
}
