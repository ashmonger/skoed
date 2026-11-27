package loadgen

import (
	"sync/atomic"
	"time"

	"github.com/miekg/dns"
)

// Outcome is what a query received.
type Outcome int

const (
	OutcomeLost      Outcome = iota // no response within the timeout
	OutcomeAnswer                   // NOERROR or NXDOMAIN
	OutcomeRefused                  // REFUSED
	OutcomeOther                    // any other RCODE, e.g. SERVFAIL
	OutcomeSendError                // the local send failed, e.g. ENOBUFS
)

// Classify gives the outcome of a response with RCODE rcode. An answer
// is NOERROR or NXDOMAIN (UBIQUITOUS_LANGUAGE.md, "Answer"): a response
// that resolves the query or applies a filtering rule to it.
func Classify(rcode int) Outcome {
	switch rcode {
	case dns.RcodeSuccess, dns.RcodeNameError:
		return OutcomeAnswer
	case dns.RcodeRefused:
		return OutcomeRefused
	default:
		return OutcomeOther
	}
}

// Second holds what happened to the queries sent during one second of
// the run. A query belongs to the second it was sent in, whenever its
// response arrives. Every query sent is counted once: Sent = answered +
// REFUSED + other + Lost + SendErrors.
type Second struct {
	Index      int       // seconds since the start of the run
	Sent       int64     // queries sent, including failed sends
	Rcodes     [16]int64 // responses received, by RCODE
	Lost       int64     // queries with no response within the timeout
	SendErrors int64     // queries whose local send failed
	Ans        Hist      // latency of the answers
	All        Hist      // latency of all responses: answers, REFUSED and other
}

// Answered is the number of answers (NOERROR or NXDOMAIN).
func (s *Second) Answered() int64 { return answered(&s.Rcodes) }

// Refused is the number of REFUSED responses.
func (s *Second) Refused() int64 { return s.Rcodes[dns.RcodeRefused] }

// Other is the number of responses that are neither answers nor REFUSED.
func (s *Second) Other() int64 { return other(&s.Rcodes) }

func answered(rcodes *[16]int64) int64 {
	return rcodes[dns.RcodeSuccess] + rcodes[dns.RcodeNameError]
}

func other(rcodes *[16]int64) int64 {
	var n int64
	for rcode, c := range rcodes {
		if Classify(rcode) == OutcomeOther {
			n += c
		}
	}
	return n
}

// add adds the counts of o to s.
func (s *Second) add(o *Second) {
	s.Sent += o.Sent
	s.Lost += o.Lost
	s.SendErrors += o.SendErrors
	for j, c := range o.Rcodes {
		s.Rcodes[j] += c
	}
	s.Ans.Merge(&o.Ans)
	s.All.Merge(&o.All)
}

// recordedSecond is the live, concurrently written form of Second.
type recordedSecond struct {
	sent       atomic.Int64
	rcodes     [16]atomic.Int64
	lost       atomic.Int64
	sendErrors atomic.Int64
	ans        atomicHist
	all        atomicHist
}

// recorder keeps one recordedSecond per second of the run, plus one
// after it for the sends that run past the schedule.
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

func (r *recorder) sendError(sentAt time.Duration) { r.at(sentAt).sendErrors.Add(1) }

func (r *recorder) response(sentAt, latency time.Duration, rcode int) {
	s := r.at(sentAt)
	s.rcodes[rcode&0xf].Add(1)
	s.all.add(latency)
	if Classify(rcode) == OutcomeAnswer {
		s.ans.add(latency)
	}
}

func (r *recorder) second(i int) Second {
	rs := &r.seconds[i]
	s := Second{Index: i, Sent: rs.sent.Load(), Lost: rs.lost.Load(), SendErrors: rs.sendErrors.Load()}
	for j := range rs.rcodes {
		s.Rcodes[j] = rs.rcodes[j].Load()
	}
	rs.ans.load(&s.Ans)
	rs.all.load(&s.All)
	return s
}

// final returns the seconds of the run, with the extra last second, which
// only holds sends that ran past the schedule, added to the one before.
func (r *recorder) final() []Second {
	out := make([]Second, len(r.seconds))
	for i := range out {
		out[i] = r.second(i)
	}
	n := len(out)
	if n > 1 {
		out[n-2].add(&out[n-1])
		out = out[:n-1]
	}
	return out
}

// Summary adds up the seconds of a window of the run.
type Summary struct {
	Seconds    int // seconds in the window
	Sent       int64
	Rcodes     [16]int64
	Lost       int64
	SendErrors int64
	Ans        Hist
	All        Hist

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
		sum.Lost += s.Lost
		sum.SendErrors += s.SendErrors
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

// Answered is the number of answers (NOERROR or NXDOMAIN).
func (s *Summary) Answered() int64 { return answered(&s.Rcodes) }

// Refused is the number of REFUSED responses.
func (s *Summary) Refused() int64 { return s.Rcodes[dns.RcodeRefused] }

// Other is the number of responses that are neither answers nor REFUSED.
func (s *Summary) Other() int64 { return other(&s.Rcodes) }

// AnsweredPerSecond is the mean number of answers per second.
func (s *Summary) AnsweredPerSecond() float64 {
	if s.Seconds == 0 {
		return 0
	}
	return float64(s.Answered()) / float64(s.Seconds)
}

// PercentAnsweredOrRefused is the share of the queries sent that
// received an answer or a REFUSED response.
func (s *Summary) PercentAnsweredOrRefused() float64 {
	return percent(s.Answered()+s.Refused(), s.Sent)
}

// PercentLost is the share of the queries sent that received no
// response within the timeout.
func (s *Summary) PercentLost() float64 { return percent(s.Lost, s.Sent) }

// PercentRefused is the share of the queries sent that received REFUSED.
func (s *Summary) PercentRefused() float64 { return percent(s.Refused(), s.Sent) }

// PercentOther is the share of the queries sent that received a response
// other than an answer or REFUSED.
func (s *Summary) PercentOther() float64 { return percent(s.Other(), s.Sent) }

func percent(n, of int64) float64 {
	if of == 0 {
		return 0
	}
	return float64(100*n) / float64(of)
}
