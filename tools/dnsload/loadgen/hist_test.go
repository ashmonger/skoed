package loadgen

import (
	"testing"
	"time"
)

func TestHistEmpty(t *testing.T) {
	var h Hist
	if h.Count() != 0 || h.Quantile(0.99) != 0 {
		t.Errorf("empty histogram: Count %d, p99 %v, want 0 and 0", h.Count(), h.Quantile(0.99))
	}
}

func TestHistBucketsCoverEveryValue(t *testing.T) {
	// Every value falls in a bucket whose upper bound is at or above it
	// and at most 1/32 above it.
	for us := int64(0); us < 50_000_000; us = us*103/100 + 1 {
		i := bucketOf(us)
		up := bucketUpper(i)
		if up < us {
			t.Fatalf("%d µs: bucket %d upper bound %d is below it", us, i, up)
		}
		if float64(up-us) > float64(us)/32+1 {
			t.Fatalf("%d µs: bucket %d upper bound %d is more than 1/32 above", us, i, up)
		}
		if i > 0 && bucketUpper(i-1) >= us {
			t.Fatalf("%d µs: previous bucket %d already covers it", us, i-1)
		}
	}
}

func TestHistQuantiles(t *testing.T) {
	var h Hist
	// 1,000 values: 1 ms … 1000 ms.
	for i := 1; i <= 1000; i++ {
		h.Add(time.Duration(i) * time.Millisecond)
	}
	for _, c := range []struct {
		q    float64
		want time.Duration
	}{
		{0.5, 500 * time.Millisecond},
		{0.99, 990 * time.Millisecond},
		{0.999, 999 * time.Millisecond},
		{1, 1000 * time.Millisecond},
	} {
		got := h.Quantile(c.q)
		if got < c.want || got > c.want+c.want/32 {
			t.Errorf("q%.3f = %v, want %v to %v", c.q, got, c.want, c.want+c.want/32)
		}
	}
}

func TestHistMerge(t *testing.T) {
	var a, b Hist
	for range 990 {
		a.Add(time.Millisecond)
	}
	for range 10 {
		b.Add(time.Second)
	}
	a.Merge(&b)
	if a.Count() != 1000 {
		t.Fatalf("Count = %d, want 1000", a.Count())
	}
	if p := a.Quantile(0.99); p > 2*time.Millisecond {
		t.Errorf("p99 = %v, want about 1ms", p)
	}
	if p := a.Quantile(0.999); p < time.Second {
		t.Errorf("p99.9 = %v, want about 1s", p)
	}
}

func TestHistClampsLargeValues(t *testing.T) {
	var h Hist
	h.Add(time.Hour)
	if h.Count() != 1 || h.Quantile(1) < 30*time.Second {
		t.Errorf("1h: Count %d, max %v, want 1 and at least 30s", h.Count(), h.Quantile(1))
	}
}
