// Package metrics provides a tiny lock-free latency histogram. No external
// dependencies — the point is to show the mechanics, not to import them.
package metrics

import (
	"sync/atomic"
	"time"
)

// bucket upper bounds in microseconds; the last bucket is unbounded.
var bucketBoundsUs = [...]int64{1, 2, 5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000, 50000}

const numBuckets = len(bucketBoundsUs) + 1

// Histogram counts observations into fixed latency buckets using atomic
// adds, so writers (the engine hot path) never take a lock.
type Histogram struct {
	counts [numBuckets]atomic.Int64
	total  atomic.Int64
	sumUs  atomic.Int64
	maxUs  atomic.Int64
}

func NewHistogram() *Histogram { return &Histogram{} }

func (h *Histogram) Observe(d time.Duration) {
	us := d.Microseconds()
	i := 0
	for i < len(bucketBoundsUs) && us > bucketBoundsUs[i] {
		i++
	}
	h.counts[i].Add(1)
	h.total.Add(1)
	h.sumUs.Add(us)
	for {
		cur := h.maxUs.Load()
		if us <= cur || h.maxUs.CompareAndSwap(cur, us) {
			break
		}
	}
}

// Snapshot is a point-in-time copy safe for JSON serialization.
type Snapshot struct {
	Count   int64          `json:"count"`
	MeanUs  float64        `json:"meanUs"`
	MaxUs   int64          `json:"maxUs"`
	P50Us   int64          `json:"p50Us"`
	P99Us   int64          `json:"p99Us"`
	Buckets []BucketCount  `json:"buckets"`
}

type BucketCount struct {
	LeUs  int64 `json:"leUs"` // upper bound; -1 = +Inf
	Count int64 `json:"count"`
}

func (h *Histogram) Snapshot() Snapshot {
	s := Snapshot{Count: h.total.Load(), MaxUs: h.maxUs.Load()}
	if s.Count > 0 {
		s.MeanUs = float64(h.sumUs.Load()) / float64(s.Count)
	}
	cum := int64(0)
	p50t := (s.Count + 1) / 2
	p99t := s.Count - s.Count/100
	p50set, p99set := false, false
	for i := range h.counts {
		c := h.counts[i].Load()
		le := int64(-1)
		if i < len(bucketBoundsUs) {
			le = bucketBoundsUs[i]
		}
		s.Buckets = append(s.Buckets, BucketCount{LeUs: le, Count: c})
		cum += c
		bound := le
		if bound == -1 {
			bound = s.MaxUs
		}
		if !p50set && cum >= p50t && s.Count > 0 {
			s.P50Us = bound
			p50set = true
		}
		if !p99set && cum >= p99t && s.Count > 0 {
			s.P99Us = bound
			p99set = true
		}
	}
	return s
}
