package analyze

import (
	"container/heap"
	"math"
	"sort"

	"github.com/runningwild/jolt/pkg/engine"
)

type EventType int

const (
	EventStart EventType = 1
	EventEnd   EventType = -1
)

type Event struct {
	Time int64
	Type EventType
	Rate float64 // The rate contribution (1/Duration)
}

// Priority Queue for Events
type EventPQ []*Event

func (pq EventPQ) Len() int { return len(pq) }
func (pq EventPQ) Less(i, j int) bool {
	if pq[i].Time == pq[j].Time {
		return pq[i].Type < pq[j].Type // Process Ends before Starts if same time? Or Start before End?
		// If Start before End: Rate goes up then down.
		// If End before Start: Rate goes down then up.
		// Usually End before Start is safer to avoid spikes if they abut exactly.
	}
	return pq[i].Time < pq[j].Time
}
func (pq EventPQ) Swap(i, j int) { pq[i], pq[j] = pq[j], pq[i] }
func (pq *EventPQ) Push(x interface{}) { *pq = append(*pq, x.(*Event)) }
func (pq *EventPQ) Pop() interface{} {
	old := *pq
	n := len(old)
	item := old[n-1]
	*pq = old[0 : n-1]
	return item
}

type SustainAnalyzer struct {
	traceCh         chan engine.TraceMsg
	expectedWorkers int
	workerMinStarts map[int]int64

	eventPQ     EventPQ
	currentRate float64
	lastTime    int64

	histogram map[int]int64 // IOPS -> Nanoseconds
	
	initialized bool
}

func NewSustainAnalyzer(ch chan engine.TraceMsg, workers int) *SustainAnalyzer {
	return &SustainAnalyzer{
		traceCh:         ch,
		expectedWorkers: workers,
		workerMinStarts: make(map[int]int64),
		eventPQ:         make(EventPQ, 0),
		histogram:       make(map[int]int64),
	}
}

func (a *SustainAnalyzer) Run() {
	heap.Init(&a.eventPQ)
	
	for msg := range a.traceCh {
		a.processMsg(msg)
	}
	a.flush()
}

func (a *SustainAnalyzer) processMsg(msg engine.TraceMsg) {
	// 1. Update MinStart for this worker
	a.workerMinStarts[msg.WorkerID] = msg.MinStart
	
	// 2. Add Spans to PQ
	for _, s := range msg.Spans {
		dur := s.End - s.Start
		if dur <= 0 { continue }
		rate := 1e9 / float64(dur) // IOPS contribution

		heap.Push(&a.eventPQ, &Event{Time: s.Start, Type: EventStart, Rate: rate})
		heap.Push(&a.eventPQ, &Event{Time: s.End, Type: EventEnd, Rate: rate})
	}

	// 3. Determine Safe Horizon
	// We can only process events up to the MINIMUM of all workers' MinStart.
	// Because a worker might yet report a request starting at that time.
	if len(a.workerMinStarts) < a.expectedWorkers {
		return // Not all workers reported yet
	}

	safeHorizon := int64(math.MaxInt64)
	for _, t := range a.workerMinStarts {
		if t < safeHorizon {
			safeHorizon = t
		}
	}
	
	// 4. Process events up to SafeHorizon
	a.processEventsUntil(safeHorizon)
}

func (a *SustainAnalyzer) flush() {
	// Process everything remaining in PQ
	a.processEventsUntil(math.MaxInt64)
}

func (a *SustainAnalyzer) processEventsUntil(limit int64) {
	for a.eventPQ.Len() > 0 {
		evt := a.eventPQ[0] // Peek
		if evt.Time > limit {
			break
		}
		heap.Pop(&a.eventPQ)

		// Advance time
		if evt.Time > a.lastTime {
			delta := evt.Time - a.lastTime
			if delta > 0 {
				// Bin the current rate
				bin := int(math.Round(a.currentRate))
				a.histogram[bin] += delta
			}
			a.lastTime = evt.Time
		}

		// Apply rate change
		if evt.Type == EventStart {
			a.currentRate += evt.Rate
		} else {
			a.currentRate -= evt.Rate
		}
		
		// Fix floating point drift near zero
		if a.currentRate < 0.001 {
			a.currentRate = 0
		}
	}
}

// GetProfile returns the stability curve: (Duration -> MinIOPS)
// But actually we want (Time -> MaxIOPS) as described:
// "at x=1s 7 IOPS, at x=2s 6 IOPS..."
// This means "For X duration, we sustained at least Y IOPS".
// This is exactly the Inverse Cumulative Distribution Function (1 - CDF).
func (a *SustainAnalyzer) GetProfile() []Point {
	// 1. Sort bins descending
	var bins []int
	for b := range a.histogram {
		bins = append(bins, b)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(bins)))

	var points []Point
	var accumDuration int64
	
	// We want to plot Duration vs IOPS.
	// Or IOPS vs Duration?
	// User said: "at x=1s 7 IOPS". X is Duration. Y is IOPS.
	// This means "We spent at least 1s at >= 7 IOPS".
	
	for _, b := range bins {
		dur := a.histogram[b]
		accumDuration += dur
		
		// Convert duration to seconds
		durSec := float64(accumDuration) / 1e9
		points = append(points, Point{X: durSec, Y: float64(b)})
	}
	
	return points
}
