package loadgen

import (
	"sync/atomic"
	"time"

	"github.com/miekg/dns"
)

// Second holds what happened to the queries sent during one second of
// the run. A query belongs to the second it was sent in, whenever its
// response arrives.
type Second struct {
	Index  int       // seconds since the start of the run
	Sent   int64     // queries sent
	Rcodes [16]int64 // responses received, by RCODE
	Lost   int64     // queries with no response within the timeout
	Ans    Hist      // latency of the answers (responses other than REFUSED)
	All    Hist      // latency of all responses, answers and refusals
}

// Refused is the number of REFUSED responses.
func (s *Second) Refused() int64 { return s.Rcodes[dns.RcodeRefused] }

// Answered is the number of responses other than REFUSED, as the
// functional spec counts answers.
func (s *Second) Answered() int64 { return s.responses() - s.Refused() }

// Other is the number of answers that are neither NOERROR nor NXDOMAIN,
// e.g. SERVFAIL.
func (s *Second) Other() int64 {
	return s.Answered() - s.Rcodes[dns.RcodeSuccess] - s.Rcodes[dns.RcodeNameError]
}

func (s *Second) responses() int64 {
	var n int64
	for _, c := range s.Rcodes {
		n += c
	}
	return n
}

// recordedSecond is the live, concurrently written form of Second.
type recordedSecond struct {
	sent   atomic.Int64
	rcodes [16]atomic.Int64
	lost   atomic.Int64
	ans    atomicHist
	all    atomicHist
}

// recorder keeps one recordedSecond per second of the run, plus the
// seconds after it in which late sends could land.
type recorder struct {
	seconds []recordedSecond
}

func newRecorder(d time.Duration) *recorder {
	n := int((d+time.Second-1)/time.Second) + 1
	return &recorder{seconds: make([]recordedSecond, n)}
}

func (r *recorder) at(sentAt time.Duration) *recordedSecond {
	i := min(max(int(sentAt/time.Second), 0), len(r.seconds)-1)
	return &r.seconds[i]
}

func (r *recorder) sent(at time.Duration) { r.at(at).sent.Add(1) }

func (r *recorder) lost(sentAt time.Duration) { r.at(sentAt).lost.Add(1) }

func (r *recorder) response(sentAt, latency time.Duration, rcode int) {
	s := r.at(sentAt)
	s.rcodes[rcode&0xf].Add(1)
	s.all.add(latency)
	if rcode != dns.RcodeRefused {
		s.ans.add(latency)
	}
}

func (r *recorder) second(i int) Second {
	rs := &r.seconds[i]
	s := Second{Index: i, Sent: rs.sent.Load(), Lost: rs.lost.Load()}
	for j := range rs.rcodes {
		s.Rcodes[j] = rs.rcodes[j].Load()
	}
	rs.ans.load(&s.Ans)
	rs.all.load(&s.All)
	return s
}

// Summary adds up the seconds of a window of the run.
type Summary struct {
	Seconds  int // seconds in the window
	Sent     int64
	Answered int64
	Rcodes   [16]int64
	Lost     int64
	Ans      Hist
	All      Hist

	MinSecond         int   // the second with the fewest answers
	MinSecondAnswered int64 // answers in that second
	// MinMinuteAnsweredPerSecond is the lowest answers per second over
	// the minutes of the window, counted from its start (the last one
	// may be shorter).
	MinMinuteAnsweredPerSecond float64
}

// Summarize adds up seconds[from:to].
func Summarize(seconds []Second, from, to int) Summary {
	from = max(from, 0)
	to = min(to, len(seconds))
	var sum Summary
	sum.MinSecond = -1
	var minute struct {
		answered int64
		seconds  int
	}
	firstMinute := true
	for i := from; i < to; i++ {
		s := &seconds[i]
		sum.Seconds++
		sum.Sent += s.Sent
		sum.Answered += s.Answered()
		sum.Lost += s.Lost
		for j, c := range s.Rcodes {
			sum.Rcodes[j] += c
		}
		sum.Ans.Merge(&s.Ans)
		sum.All.Merge(&s.All)
		if sum.MinSecond < 0 || s.Answered() < sum.MinSecondAnswered {
			sum.MinSecond, sum.MinSecondAnswered = i, s.Answered()
		}
		minute.answered += s.Answered()
		minute.seconds++
		if minute.seconds == 60 || i == to-1 {
			perSecond := float64(minute.answered) / float64(minute.seconds)
			if firstMinute || perSecond < sum.MinMinuteAnsweredPerSecond {
				sum.MinMinuteAnsweredPerSecond = perSecond
				firstMinute = false
			}
			minute.answered, minute.seconds = 0, 0
		}
	}
	return sum
}

// Refused is the number of REFUSED responses.
func (s *Summary) Refused() int64 { return s.Rcodes[dns.RcodeRefused] }

// Other is the number of answers that are neither NOERROR nor NXDOMAIN.
func (s *Summary) Other() int64 {
	return s.Answered - s.Rcodes[dns.RcodeSuccess] - s.Rcodes[dns.RcodeNameError]
}

// AnsweredPerSecond is the mean number of answers per second.
func (s *Summary) AnsweredPerSecond() float64 {
	if s.Seconds == 0 {
		return 0
	}
	return float64(s.Answered) / float64(s.Seconds)
}

// PercentAnsweredOrRefused is the share of the queries sent that
// received an answer or a REFUSED response.
func (s *Summary) PercentAnsweredOrRefused() float64 {
	return percent(s.Answered+s.Refused(), s.Sent)
}

// PercentLost is the share of the queries sent that received no
// response within the timeout.
func (s *Summary) PercentLost() float64 { return percent(s.Lost, s.Sent) }

// PercentRefused is the share of the queries sent that received REFUSED.
func (s *Summary) PercentRefused() float64 { return percent(s.Refused(), s.Sent) }

func percent(n, of int64) float64 {
	if of == 0 {
		return 0
	}
	return float64(100*n) / float64(of)
}
