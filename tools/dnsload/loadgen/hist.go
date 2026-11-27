package loadgen

import (
	"math"
	"math/bits"
	"sync/atomic"
	"time"
)

// Latencies are counted in microseconds in log-linear buckets: exact
// below 64 µs, then 32 buckets per power of two, so a bucket is at most
// 1/32 (3 %) wide relative to its values. The last bucket holds every
// value from about 66 s up.
const (
	subBuckets  = 32
	histBuckets = subBuckets * 22
)

func bucketOf(us int64) int {
	if us < 0 {
		us = 0
	}
	e := max(0, bits.Len64(uint64(us))-6)
	return min(subBuckets*e+int(us>>e), histBuckets-1)
}

// bucketUpper is the highest value, in microseconds, of bucket i.
func bucketUpper(i int) int64 {
	e := max(0, i/subBuckets-1)
	m := int64(i - subBuckets*e)
	return (m+1)<<e - 1
}

// Hist is a latency histogram. Quantiles are reported as the upper bound
// of their bucket, so they are never below the true value and at most
// 3 % above it.
type Hist struct {
	counts [histBuckets]uint64
	n      uint64
}

// Add counts one latency.
func (h *Hist) Add(d time.Duration) {
	h.counts[bucketOf(d.Microseconds())]++
	h.n++
}

// Merge adds the counts of o.
func (h *Hist) Merge(o *Hist) {
	for i, c := range o.counts {
		h.counts[i] += c
	}
	h.n += o.n
}

// Count is the number of latencies counted.
func (h *Hist) Count() uint64 { return h.n }

// Quantile is the latency below or at which a fraction q of the counted
// latencies fall, or 0 when the histogram is empty.
func (h *Hist) Quantile(q float64) time.Duration {
	if h.n == 0 {
		return 0
	}
	rank := max(1, uint64(math.Ceil(q*float64(h.n))))
	var seen uint64
	for i, c := range h.counts {
		seen += c
		if seen >= rank {
			return time.Duration(bucketUpper(i)) * time.Microsecond
		}
	}
	return time.Duration(bucketUpper(histBuckets-1)) * time.Microsecond
}

// atomicHist is the histogram of one second, written by several
// goroutines at once.
type atomicHist struct {
	counts [histBuckets]atomic.Uint32
}

func (h *atomicHist) add(d time.Duration) {
	h.counts[bucketOf(d.Microseconds())].Add(1)
}

func (h *atomicHist) load(into *Hist) {
	for i := range h.counts {
		c := uint64(h.counts[i].Load())
		into.counts[i] = c
		into.n += c
	}
}
