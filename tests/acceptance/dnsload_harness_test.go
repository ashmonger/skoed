// DNS load generator for the DNS overload protection acceptance tests
// (dns_overload_protection_test.go, TS-DnsOverloadProtection § 9.2 and
// § 10).
//
// runDNSLoad sends A queries over UDP to one server at an even rate and
// records, for every query, when it was sent and whether it received an
// answer, a REFUSED response, another response or nothing within
// dnsLoadTimeout. loadRun then gives the per-second counts, the 99th
// percentile latency of the answers and the 99.9th percentile of all
// responses.
//
// How the rate is kept even: the run is a list of phases, each with a rate
// r. Query j of a phase is due at phaseStart + j/r, so the schedule does
// not drift when a send is late (a late query is sent at once, and the
// next ones keep their own time). The queries of a phase are split across
// workers round-robin; each worker has its own UDP socket and its own
// reader goroutine, and gives each of its queries a distinct DNS ID, so a
// response is matched to its query by ID. A worker sends at most
// dnsLoadWorkerRate queries per second, so its IDs are not reused within
// dnsLoadTimeout (dnsLoadWorkerRate × 5 s = 1,000 < 65,536). Each send and
// each read is one system call and one small allocation; at 3,000 queries
// per second, the highest rate of the overload tests, that is 15 sockets,
// far from what one core does.
//
// The name of query seq comes from a function of seq, so the caller sets
// the query mix (see overloadNames.mix in dns_overload_protection_test.go).
package acceptance

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"
)

const (
	// dnsLoadTimeout is the time after which a query with no response is
	// lost (TS-DnsOverloadProtection § 9.2).
	dnsLoadTimeout = 5 * time.Second
	// dnsLoadWorkerRate is the highest rate one worker, and so one socket,
	// sends at.
	dnsLoadWorkerRate = 200
)

// loadPhase is a part of a run at one rate.
type loadPhase struct {
	Rate     int           // queries per second
	Duration time.Duration // the run can be cancelled before the end
}

// queryKind says which part of the query mix a query belongs to.
type queryKind int

const (
	queryCached  queryKind = iota // a name the node has in its DNS cache
	queryMiss                     // a name the node has never seen
	queryBlocked                  // a name blocked by the "ads" list
)

// loadQuery is the A query sent for one sequence number.
type loadQuery struct {
	Name string
	Kind queryKind
}

// queryOutcome is what a query received.
type queryOutcome int

const (
	outcomeLost    queryOutcome = iota // no response within dnsLoadTimeout
	outcomeAnswer                      // NOERROR or NXDOMAIN
	outcomeRefused                     // REFUSED
	outcomeOther                       // any other RCODE
)

// queryRecord is what the load generator records for one query.
type queryRecord struct {
	Query   loadQuery
	Sent    time.Duration // when it was sent, from the start of the run
	Latency time.Duration // from the send to the response; 0 when lost
	Outcome queryOutcome
	Rcode   int      // RCODE of the response; 0 when lost
	Addrs   []net.IP // addresses of the A records of the response
	sent    bool
}

// loadRun is the result of runDNSLoad.
type loadRun struct {
	Start    time.Time
	Duration time.Duration // from the start to the last send
	Records  []queryRecord // the queries sent, in sequence order
}

// loadStats counts the outcomes of the queries sent in a time range.
type loadStats struct {
	Sent, Answered, Refused, Other, Lost int
	OtherRcodes                          map[string]int
	P99Answer                            time.Duration // 99th percentile latency of the answers
	P999Response                         time.Duration // 99.9th percentile latency of all responses
}

func (s loadStats) String() string {
	return fmt.Sprintf("sent %d, answered %d, refused %d, other %d %v, lost %d, p99 answers %s, p99.9 responses %s",
		s.Sent, s.Answered, s.Refused, s.Other, s.OtherRcodes, s.Lost,
		s.P99Answer.Round(10*time.Microsecond), s.P999Response.Round(10*time.Microsecond))
}

// runDNSLoad sends the queries of phases to server, starting at start,
// and returns once every query sent has received a response or timed out.
// Cancelling ctx stops the sending early; the queries not sent are not in
// the result. next gives the query for each sequence number.
func runDNSLoad(ctx context.Context, t *testing.T, server string, start time.Time, phases []loadPhase, next func(seq int) loadQuery) *loadRun {
	t.Helper()
	var plan []plannedPhase
	var offset time.Duration
	total, maxRate := 0, 0
	for _, p := range phases {
		n := int(int64(p.Rate) * int64(p.Duration) / int64(time.Second))
		plan = append(plan, plannedPhase{start: offset, firstSeq: total, count: n, rate: p.Rate})
		total += n
		offset += p.Duration
		maxRate = max(maxRate, p.Rate)
	}
	workers := max(2, (maxRate+dnsLoadWorkerRate-1)/dnsLoadWorkerRate)
	records := make([]queryRecord, total)

	var lws []*loadWorker
	for range workers {
		lw, err := newLoadWorker(server, start, records)
		if err != nil {
			// t.Errorf, not t.Fatalf: runDNSLoad can run in a goroutine
			// of its own.
			t.Errorf("load generator: %v", err)
			for _, lw := range lws {
				lw.conn.Close() //nolint:errcheck
			}
			return &loadRun{Start: start}
		}
		lws = append(lws, lw)
	}
	var wg sync.WaitGroup
	for w, lw := range lws {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lw.send(ctx, plan, w, workers, next)
		}()
	}
	wg.Wait()
	duration := time.Since(start)
	time.Sleep(dnsLoadTimeout)
	for _, lw := range lws {
		lw.conn.Close() //nolint:errcheck
		<-lw.done
	}

	run := &loadRun{Start: start, Duration: duration}
	for _, r := range records {
		if r.sent {
			run.Records = append(run.Records, r)
		}
	}
	return run
}

// runDNSLoadNow is runDNSLoad starting now, without cancellation.
func runDNSLoadNow(t *testing.T, server string, phases []loadPhase, next func(seq int) loadQuery) *loadRun {
	t.Helper()
	return runDNSLoad(context.Background(), t, server, time.Now(), phases, next)
}

type plannedPhase struct {
	start           time.Duration
	firstSeq, count int
	rate            int
}

// loadWorker sends its share of the queries on its own socket and reads
// the responses in its own goroutine.
type loadWorker struct {
	conn    *net.UDPConn
	start   time.Time
	records []queryRecord

	mu      sync.Mutex
	pending map[uint16]int // DNS ID → sequence number
	nextID  uint16
	done    chan struct{}
}

func newLoadWorker(server string, start time.Time, records []queryRecord) (*loadWorker, error) {
	raddr, err := net.ResolveUDPAddr("udp", server)
	if err != nil {
		return nil, err
	}
	conn, err := net.DialUDP("udp", nil, raddr)
	if err != nil {
		return nil, err
	}
	// Keep the responses of a burst in the kernel until the reader gets
	// them; the kernel caps it at net.core.rmem_max.
	conn.SetReadBuffer(1 << 20) //nolint:errcheck
	lw := &loadWorker{
		conn:    conn,
		start:   start,
		records: records,
		pending: map[uint16]int{},
		nextID:  uint16(time.Now().UnixNano()),
		done:    make(chan struct{}),
	}
	go lw.read()
	return lw, nil
}

// send sends query j of each phase when j % workers == w, at its due time.
func (lw *loadWorker) send(ctx context.Context, plan []plannedPhase, w, workers int, next func(int) loadQuery) {
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	buf := make([]byte, 0, 512)
	for _, p := range plan {
		for j := w; j < p.count; j += workers {
			due := lw.start.Add(p.start + time.Duration(int64(j)*int64(time.Second)/int64(p.rate)))
			if d := time.Until(due); d > 0 {
				timer.Reset(d)
				select {
				case <-ctx.Done():
					return
				case <-timer.C:
				}
			} else if ctx.Err() != nil {
				return
			}
			seq := p.firstSeq + j
			q := next(seq)
			m := new(dns.Msg)
			m.SetQuestion(dns.Fqdn(q.Name), dns.TypeA)

			lw.mu.Lock()
			m.Id = lw.nextID
			lw.nextID++
			lw.pending[m.Id] = seq
			rec := &lw.records[seq]
			rec.Query = q
			rec.sent = true
			rec.Sent = time.Since(lw.start)
			lw.mu.Unlock()

			out, err := m.PackBuffer(buf)
			if err != nil {
				continue // left as lost
			}
			lw.conn.Write(out) //nolint:errcheck // a failed send is a lost query
		}
	}
}

// read matches each response to its query until the socket is closed.
func (lw *loadWorker) read() {
	defer close(lw.done)
	buf := make([]byte, 65535)
	for {
		n, err := lw.conn.Read(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			// e.g. ECONNREFUSED from an ICMP port unreachable: the
			// socket stays usable.
			continue
		}
		now := time.Since(lw.start)
		var m dns.Msg
		if m.Unpack(buf[:n]) != nil {
			continue
		}
		lw.mu.Lock()
		seq, ok := lw.pending[m.Id]
		if ok {
			delete(lw.pending, m.Id)
			rec := &lw.records[seq]
			if lat := now - rec.Sent; lat <= dnsLoadTimeout {
				rec.Latency = lat
				rec.Rcode = m.Rcode
				switch m.Rcode {
				case dns.RcodeSuccess, dns.RcodeNameError:
					rec.Outcome = outcomeAnswer
				case dns.RcodeRefused:
					rec.Outcome = outcomeRefused
				default:
					rec.Outcome = outcomeOther
				}
				for _, rr := range m.Answer {
					if a, ok := rr.(*dns.A); ok {
						rec.Addrs = append(rec.Addrs, a.A)
					}
				}
			}
		}
		lw.mu.Unlock()
	}
}

// stats counts the queries sent in [from, to).
func (r *loadRun) stats(from, to time.Duration) loadStats {
	s := loadStats{OtherRcodes: map[string]int{}}
	var answers, responses []time.Duration
	for i := range r.Records {
		q := &r.Records[i]
		if q.Sent < from || q.Sent >= to {
			continue
		}
		s.Sent++
		switch q.Outcome {
		case outcomeLost:
			s.Lost++
			continue
		case outcomeAnswer:
			s.Answered++
			answers = append(answers, q.Latency)
		case outcomeRefused:
			s.Refused++
		default:
			s.Other++
			s.OtherRcodes[dns.RcodeToString[q.Rcode]]++
		}
		responses = append(responses, q.Latency)
	}
	s.P99Answer = percentile(answers, 0.99)
	s.P999Response = percentile(responses, 0.999)
	return s
}

// all counts every query of the run.
func (r *loadRun) all() loadStats {
	return r.stats(0, r.Duration+time.Second)
}

// perSecond counts the queries sent in each second of the run.
func (r *loadRun) perSecond() []loadStats {
	n := int((r.Duration + time.Second - 1) / time.Second)
	out := make([]loadStats, n)
	for i := range out {
		out[i] = r.stats(time.Duration(i)*time.Second, time.Duration(i+1)*time.Second)
	}
	return out
}

// logPerSecond logs one line per second of the run.
func (r *loadRun) logPerSecond(t *testing.T) {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "%4s %6s %8s %7s %5s %5s %9s\n", "s", "sent", "answered", "refused", "other", "lost", "p99 ans")
	for i, s := range r.perSecond() {
		fmt.Fprintf(&b, "%4d %6d %8d %7d %5d %5d %9s\n", i, s.Sent, s.Answered, s.Refused, s.Other, s.Lost,
			s.P99Answer.Round(10*time.Microsecond))
	}
	t.Logf("per second:\n%s", b.String())
}

// checkRate fails the test when the load generator did not send rate
// queries per second, within 10 %, in every whole second of [from, to):
// the run would then not be the load the scenario describes.
func (r *loadRun) checkRate(t *testing.T, from, to time.Duration, rate int) {
	t.Helper()
	for sec := from; sec+time.Second <= to; sec += time.Second {
		n := r.stats(sec, sec+time.Second).Sent
		if float64(n) < 0.9*float64(rate) || float64(n) > 1.1*float64(rate) {
			t.Errorf("load generator: sent %d queries in second %d, want %d ± 10 %%; the run does not test the scenario",
				n, sec/time.Second, rate)
		}
	}
}

// percentile returns the nearest-rank p-th percentile of ds (0 when empty).
func percentile(ds []time.Duration, p float64) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	sorted := append([]time.Duration(nil), ds...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	rank := int(p*float64(len(sorted)) + 0.999999999)
	return sorted[min(max(rank, 1), len(sorted))-1]
}

// processRSS returns the resident memory of process pid, from VmRSS in
// /proc/<pid>/status: the memory used by a node in the sense of the
// functional spec.
func processRSS(pid int) (int64, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		rest, ok := strings.CutPrefix(line, "VmRSS:")
		if !ok {
			continue
		}
		f := strings.Fields(rest) // "123456 kB"
		if len(f) != 2 || f[1] != "kB" {
			return 0, fmt.Errorf("unexpected VmRSS line %q", line)
		}
		kb, err := strconv.ParseInt(f[0], 10, 64)
		if err != nil {
			return 0, err
		}
		return kb * 1024, nil
	}
	return 0, fmt.Errorf("no VmRSS in /proc/%d/status", pid)
}
