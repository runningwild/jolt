package stats

import (
	"github.com/HdrHistogram/hdrhistogram-go"
)

// Histogram wraps the HdrHistogram library to match Jolt's interface.
type Histogram struct {
	impl *hdrhistogram.Histogram
}

func NewHistogram() *Histogram {
	// Track from 1 microsecond to 1 hour (3600*1000*1000 us)
	// 3 significant figures means ~1% precision or better
	h := hdrhistogram.New(1, 3600*1000*1000, 3)
	return &Histogram{impl: h}
}

// Record records a latency in microseconds
func (h *Histogram) Record(valUs int64) {
	if valUs < 0 {
		return
	}
	// HdrHistogram requires value >= min. If we get 0, record as 1 or skip?
	// 0 latency is impossible in Jolt, but usually means <1us.
	if valUs < 1 {
		valUs = 1
	}
	
	if err := h.impl.RecordValue(valUs); err != nil {
		// If value is too large, it is dropped or we could clamp it.
		// For now, silently drop or clamp to max?
		// Given we set max to 1 hour, anything larger is probably an error or hung system.
		// Let's print once? No, avoiding noise.
	}
}

func (h *Histogram) Merge(other *Histogram) {
	// Merge other into h
	h.impl.Merge(other.impl)
}

// ValueAtQuantile returns the value at quantile q (0.0 - 1.0)
func (h *Histogram) ValueAtQuantile(q float64) int64 {
	// Lib takes 0-100
	return h.impl.ValueAtQuantile(q * 100.0)
}

func (h *Histogram) Mean() float64 {
	return h.impl.Mean()
}

// TotalCount returns the total number of recorded values
func (h *Histogram) TotalCount() int64 {
	return h.impl.TotalCount()
}

// Min returns the minimum recorded value
func (h *Histogram) Min() int64 {
	return h.impl.Min()
}

// Max returns the maximum recorded value
func (h *Histogram) Max() int64 {
	return h.impl.Max()
}

// StdDev returns the standard deviation
func (h *Histogram) StdDev() float64 {
	return h.impl.StdDev()
}

// Reset resets the histogram
func (h *Histogram) Reset() {
	h.impl.Reset()
}

// ByteSize returns an estimation of memory usage (optional helper)
func (h *Histogram) ByteSize() int {
	return h.impl.ByteSize()
}