// Adapters from the DNS overload protection acceptance tests
// (dns_overload_protection_test.go) to the load client they share with
// tools/dnsload, skoed/dnsload/loadgen (TS-DnsOverloadProtection § 9.2).
//
// loadgen.Run sends A queries over UDP at an even rate and counts, per
// second of sending, the answers (NOERROR or NXDOMAIN), the REFUSED
// responses, the other responses, the queries with no response within
// the timeout (Lost) and those whose local send failed (SendErrors). The
// helpers below run it with the 5 s timeout of § 9.2 and reduce its
// result to what the scenarios check.
//
// The percentiles come from loadgen's latency histograms, which report
// the upper bound of a bucket: never below the true value and at most
// 3 % above it. The latency bounds of the functional spec (50 ms, 1 s)
// are kept as they are, so the checks are that much stricter.
package acceptance

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"
	"skoed/dnsload/loadgen"
)

// dnsLoadTimeout is the time after which a query with no response is
// lost (TS-DnsOverloadProtection § 9.2).
const dnsLoadTimeout = 5 * time.Second

// loadPhase is a part of a run at one rate.
type loadPhase struct {
	Rate     int // queries per second
	Duration time.Duration
}

// loadOptions are the optional settings of a run.
type loadOptions struct {
	// Start is when the run starts; zero is now. Two runs, or a run and
	// a measurement, are aligned on it.
	Start time.Time
	// Records keeps the outcome of every query in loadRun.Queries.
	Records bool
}

// loadRun is the result of runDNSLoad.
type loadRun struct {
	*loadgen.Result
	// Duration is the time the queries are sent over: the sum of the
	// phase durations.
	Duration time.Duration
}

// runDNSLoad sends the queries of source to server in the given phases
// and returns once every query sent has its outcome.
func runDNSLoad(t *testing.T, server string, phases []loadPhase, source loadgen.Source, opt loadOptions) *loadRun {
	t.Helper()
	var ps []loadgen.Phase
	var d time.Duration
	for _, p := range phases {
		ps = append(ps, loadgen.Phase{Rate: float64(p.Rate), Duration: p.Duration})
		d += p.Duration
	}
	res, err := loadgen.Run(context.Background(), loadgen.Config{
		Server:  server,
		Phases:  ps,
		Timeout: dnsLoadTimeout,
		Source:  source,
		Start:   opt.Start,
		Records: opt.Records,
	}, nil)
	if err != nil {
		// t.Errorf, not t.Fatalf: runDNSLoad can run in a goroutine of
		// its own. The empty result fails the rate check of the test.
		t.Errorf("load generator: %v", err)
		return &loadRun{Result: &loadgen.Result{}, Duration: d}
	}
	if res.Late > 0 || res.Evicted > 0 {
		t.Logf("load generator: %d late responses, %d queries evicted", res.Late, res.Evicted)
	}
	return &loadRun{Result: res, Duration: d}
}

// loadStats counts the outcomes of the queries sent in a range of
// seconds.
type loadStats struct {
	Sent, Answered, Refused, Other int64
	// NoResponse is the queries that received no response: Lost plus
	// SendErrors, since a query whose send failed gets no response
	// either.
	NoResponse   int64
	OtherRcodes  map[string]int64
	P99Answer    time.Duration // 99th percentile latency of the answers
	P999Response time.Duration // 99.9th percentile latency of all responses
}

func (s loadStats) String() string {
	return fmt.Sprintf("sent %d, answered %d, refused %d, other %d %v, no response %d, p99 answers %s, p99.9 responses %s",
		s.Sent, s.Answered, s.Refused, s.Other, s.OtherRcodes, s.NoResponse,
		s.P99Answer.Round(10*time.Microsecond), s.P999Response.Round(10*time.Microsecond))
}

func newLoadStats(sum loadgen.Summary) loadStats {
	s := loadStats{
		Sent:         sum.Sent,
		Answered:     sum.Answered(),
		Refused:      sum.Refused(),
		Other:        sum.Other(),
		NoResponse:   sum.Lost + sum.SendErrors,
		OtherRcodes:  map[string]int64{},
		P99Answer:    sum.Ans.Quantile(0.99),
		P999Response: sum.All.Quantile(0.999),
	}
	for rcode, n := range sum.Rcodes {
		if n > 0 && loadgen.Classify(rcode) == loadgen.OutcomeOther {
			s.OtherRcodes[dns.RcodeToString[rcode]] = n
		}
	}
	return s
}

// stats counts the queries sent in the whole seconds [from, to).
func (r *loadRun) stats(from, to time.Duration) loadStats {
	return newLoadStats(loadgen.Summarize(r.Seconds, int(from/time.Second), int(to/time.Second)))
}

// all counts every query of the run.
func (r *loadRun) all() loadStats {
	return newLoadStats(loadgen.Summarize(r.Seconds, 0, len(r.Seconds)))
}

// logPerSecond logs one line per second of the run.
func (r *loadRun) logPerSecond(t *testing.T) {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "%4s %6s %8s %7s %5s %5s %5s %9s\n", "s", "sent", "answered", "refused", "other", "lost", "sendE", "p99 ans")
	for _, s := range r.Seconds {
		fmt.Fprintf(&b, "%4d %6d %8d %7d %5d %5d %5d %9s\n", s.Index, s.Sent, s.Answered(), s.Refused(), s.Other(),
			s.Lost, s.SendErrors, s.Ans.Quantile(0.99))
	}
	t.Logf("per second:\n%s", b.String())
}

// checkRate fails the test when the load generator did not send rate
// queries per second, within 10 %, in every whole second of [from, to):
// the run would then not be the load the scenario describes.
func (r *loadRun) checkRate(t *testing.T, from, to time.Duration, rate int) {
	t.Helper()
	for i := int(from / time.Second); i < int(to/time.Second); i++ {
		var n int64
		if i < len(r.Seconds) {
			n = r.Seconds[i].Sent
		}
		if float64(n) < 0.9*float64(rate) || float64(n) > 1.1*float64(rate) {
			t.Errorf("load generator: sent %d queries in second %d, want %d ± 10 %%; the run does not test the scenario",
				n, i, rate)
		}
	}
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
