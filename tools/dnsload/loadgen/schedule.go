// Package loadgen sends DNS queries over UDP at an even rate and
// records, per second of send time, how many were answered, refused or
// lost and the latency of the answers and of all responses.
//
// It is the client code of tools/dnsload (TS-DnsOverloadProtection
// § 9.2), kept importable so that an acceptance test can drive the same
// load.
package loadgen

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// Phase is one step of a run: Rate queries per second for Duration.
// Settle is the part at the start of the phase left out of its summary.
type Phase struct {
	Rate     float64
	Duration time.Duration
	Settle   time.Duration
}

// ParsePhases parses "rate:duration[:settle],..." such as
// "40000:60s,5000:120s:5s". A phase without a settle time gets settle.
func ParsePhases(s string, settle time.Duration) ([]Phase, error) {
	if s == "" {
		return nil, fmt.Errorf("no phase")
	}
	var phases []Phase
	for part := range strings.SplitSeq(s, ",") {
		fields := strings.Split(part, ":")
		if len(fields) < 2 || len(fields) > 3 {
			return nil, fmt.Errorf("phase %q: want rate:duration[:settle]", part)
		}
		rate, err := strconv.ParseFloat(fields[0], 64)
		if err != nil || rate < 0 || math.IsInf(rate, 0) || math.IsNaN(rate) {
			return nil, fmt.Errorf("phase %q: bad rate", part)
		}
		dur, err := time.ParseDuration(fields[1])
		if err != nil || dur <= 0 {
			return nil, fmt.Errorf("phase %q: bad duration", part)
		}
		p := Phase{Rate: rate, Duration: dur, Settle: settle}
		if len(fields) == 3 {
			if p.Settle, err = time.ParseDuration(fields[2]); err != nil || p.Settle < 0 {
				return nil, fmt.Errorf("phase %q: bad settle time", part)
			}
		}
		if p.Settle >= p.Duration {
			return nil, fmt.Errorf("phase %q: settle time is not shorter than the phase", part)
		}
		phases = append(phases, p)
	}
	return phases, nil
}

// Schedule gives the send time of every query of a run. Query k of a
// phase is sent k / rate after the phase starts, so queries are evenly
// spaced and the run sends floor(rate × duration) queries per phase.
type Schedule struct {
	phases []Phase
	starts []time.Duration // start of each phase
	firsts []int64         // number of the first query of each phase
	counts []int64         // number of queries of each phase
	total  int64
	end    time.Duration
}

// NewSchedule builds the schedule of phases run one after the other.
func NewSchedule(phases []Phase) *Schedule {
	s := &Schedule{phases: phases}
	for _, p := range phases {
		count := int64(math.Floor(p.Rate*p.Duration.Seconds() + 1e-9))
		s.starts = append(s.starts, s.end)
		s.firsts = append(s.firsts, s.total)
		s.counts = append(s.counts, count)
		s.total += count
		s.end += p.Duration
	}
	return s
}

// Total is the number of queries of the run.
func (s *Schedule) Total() int64 { return s.total }

// Duration is the length of the run.
func (s *Schedule) Duration() time.Duration { return s.end }

// PhaseStart is the time phase i starts, from the start of the run.
func (s *Schedule) PhaseStart(i int) time.Duration { return s.starts[i] }

// PeakRate is the highest rate of the run.
func (s *Schedule) PeakRate() float64 {
	var peak float64
	for _, p := range s.phases {
		peak = max(peak, p.Rate)
	}
	return peak
}

// At is the send time of query n, from the start of the run.
func (s *Schedule) At(n int64) time.Duration {
	i := len(s.phases) - 1
	for i > 0 && n < s.firsts[i] {
		i--
	}
	k := n - s.firsts[i]
	return s.starts[i] + time.Duration(math.Ceil(float64(k)*1e9/s.phases[i].Rate))
}

// Due is the number of queries whose send time is at or before t.
func (s *Schedule) Due(t time.Duration) int64 {
	if t < 0 {
		return 0
	}
	if t >= s.end {
		return s.total
	}
	i := len(s.phases) - 1
	for i > 0 && t < s.starts[i] {
		i--
	}
	if s.counts[i] == 0 {
		return s.firsts[i]
	}
	// Estimate, then correct the float rounding against At.
	k := int64(math.Floor((t - s.starts[i]).Seconds() * s.phases[i].Rate))
	k = min(max(k, 0), s.counts[i]-1)
	for k+1 < s.counts[i] && s.At(s.firsts[i]+k+1) <= t {
		k++
	}
	for k >= 0 && s.At(s.firsts[i]+k) > t {
		k--
	}
	return s.firsts[i] + k + 1
}

// idsPerSocket is the number of DNS IDs; each worker has its own socket
// and gives its queries consecutive IDs.
const idsPerSocket = 65536

// DefaultWorkers is the number of sockets needed so that each reuses a
// DNS ID no sooner than twice the timeout after its last use, and at
// least 4.
func DefaultWorkers(peakRate float64, timeout time.Duration) int {
	need := math.Ceil(peakRate * timeout.Seconds() * 2 / idsPerSocket)
	return max(4, int(need))
}
