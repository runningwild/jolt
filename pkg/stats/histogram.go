package stats

import (
	"math"
)

// Histogram is a mergeable, log-linear histogram for latency tracking.
// It avoids OOM by using fixed buckets instead of storing every sample.
type Histogram struct {
	// Config
	MinVal int64 // Min tracked value (e.g., 1 us)
	MaxVal int64 // Max tracked value (e.g., 60 s)
	
	// Data
	Counts    []int64
	TotalCount int64
	MinSeen    int64
	MaxSeen    int64
	Sum        int64
}

const (
	// Configuration constants
	// We use a hybrid approach:
	// 0 to Threshold: Linear buckets (High precision for typical latency)
	// Threshold to Max: Exponential buckets (Good precision for tail)
	
	linearThreshold = 1000 // up to 1000us (1ms) with 1us precision
	expFactor       = 1.1  // 10% error margin for tails (compact)
)

var (
	bucketLimits []int64
)

func init() {
	// Pre-calculate bucket boundaries to avoid math in hot path
	// 1. Linear part
	for i := int64(0); i <= linearThreshold; i++ {
		bucketLimits = append(bucketLimits, i)
	}
	
	// 2. Exponential part
	curr := float64(linearThreshold)
	limit := float64(60 * 1000 * 1000) // 60 seconds in us
	for curr < limit {
		curr *= expFactor
		bucketLimits = append(bucketLimits, int64(curr))
	}
}

func NewHistogram() *Histogram {
	return &Histogram{
		Counts:  make([]int64, len(bucketLimits)),
		MinSeen: math.MaxInt64,
		MaxSeen: 0,
	}
}

// Record records a latency in microseconds
func (h *Histogram) Record(valUs int64) {
	if valUs < 0 { return }
	
	h.TotalCount++
	h.Sum += valUs
	
	if valUs < h.MinSeen { h.MinSeen = valUs }
	if valUs > h.MaxSeen { h.MaxSeen = valUs }

	idx := h.findBucket(valUs)
	h.Counts[idx]++
}

// findBucket maps a value to a bucket index.
// This needs to be fast.
func (h *Histogram) findBucket(val int64) int {
	// Linear optimization
	if val < linearThreshold {
		return int(val)
	}
	
	// Binary search for upper buckets
	// In a real HDR lib this would be bit-hacks (leading zeros), 
	// but binary search over ~150 buckets is fast enough (nanoseconds).
	low := int(linearThreshold)
	high := len(bucketLimits) - 1

	for low <= high {
		mid := (low + high) / 2
		if bucketLimits[mid] < val {
			low = mid + 1
		} else {
			high = mid - 1
		}
	}
	if low >= len(bucketLimits) {
		return len(bucketLimits) - 1
	}
	return low
}

func (h *Histogram) Merge(other *Histogram) {
	if other.TotalCount == 0 { return }
	
	if h.TotalCount == 0 {
		*h = *other
		// Deep copy counts
		h.Counts = make([]int64, len(other.Counts))
		copy(h.Counts, other.Counts)
		return
	}

	h.TotalCount += other.TotalCount
	h.Sum += other.Sum
	if other.MinSeen < h.MinSeen { h.MinSeen = other.MinSeen }
	if other.MaxSeen > h.MaxSeen { h.MaxSeen = other.MaxSeen }

	for i, c := range other.Counts {
		h.Counts[i] += c
	}
}

func (h *Histogram) ValueAtQuantile(q float64) int64 {
	if h.TotalCount == 0 { return 0 }
	
	target := int64(float64(h.TotalCount) * q)
	var current int64
	
	for i, count := range h.Counts {
		current += count
		if current >= target {
			return bucketLimits[i]
		}
	}
	return h.MaxSeen
}

func (h *Histogram) Mean() float64 {
	if h.TotalCount == 0 { return 0 }
	return float64(h.Sum) / float64(h.TotalCount)
}
