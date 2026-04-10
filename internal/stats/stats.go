package stats

import (
	"math"
	"sort"
	"sync"
	"time"
)

type RequestRecord struct {
	Provider     string
	Model        string
	Status       int
	Latency      time.Duration
	Error        bool
	ErrorMsg     string
	Timestamp    time.Time
	InputTokens  int
	OutputTokens int
	Cost         float64
	TTFT         time.Duration
}

type Collector struct {
	mu      sync.RWMutex
	records []RequestRecord
	maxAge  time.Duration
	stop    chan struct{}
}

func NewCollector() *Collector {
	c := &Collector{
		maxAge: 5 * time.Minute,
		stop:   make(chan struct{}),
	}
	go c.backgroundCleanup()
	return c
}

func (c *Collector) Record(r RequestRecord) {
	r.Timestamp = time.Now()
	c.mu.Lock()
	c.records = append(c.records, r)
	c.mu.Unlock()
}

func (c *Collector) Stop() {
	close(c.stop)
}

func (c *Collector) backgroundCleanup() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			c.mu.Lock()
			c.cleanup()
			c.mu.Unlock()
		case <-c.stop:
			return
		}
	}
}

func (c *Collector) cleanup() {
	cutoff := time.Now().Add(-c.maxAge)
	i := 0
	for _, r := range c.records {
		if r.Timestamp.After(cutoff) {
			c.records[i] = r
			i++
		}
	}
	c.records = c.records[:i]
}

type ProviderStats struct {
	Name          string  `json:"name"`
	TotalReqs     int     `json:"total_requests"`
	ErrorCount    int     `json:"error_count"`
	ErrorRate     float64 `json:"error_rate"`
	LatencyP50ms  float64 `json:"latency_p50_ms"`
	LatencyP95ms  float64 `json:"latency_p95_ms"`
	RPM           float64 `json:"rpm"`
	InputTokens   int     `json:"input_tokens"`
	OutputTokens  int     `json:"output_tokens"`
	TotalCost     float64 `json:"total_cost"`
	AvgTTFTMs     float64 `json:"avg_ttft_ms"`
}

type ModelStats struct {
	Name      string  `json:"name"`
	TotalReqs int     `json:"total_requests"`
	RPM       float64 `json:"rpm"`
}

type ErrorEntry struct {
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	Status    int    `json:"status"`
	Message   string `json:"message"`
	Timestamp string `json:"timestamp"`
}

type Snapshot struct {
	TotalRequests    int                    `json:"total_requests"`
	ActiveProviders  int                    `json:"active_providers"`
	AvgLatencyMs     float64                `json:"avg_latency_ms"`
	ErrorRate        float64                `json:"error_rate"`
	TotalInputTokens  int                   `json:"total_input_tokens"`
	TotalOutputTokens int                   `json:"total_output_tokens"`
	TotalCost        float64                `json:"total_cost"`
	ByProvider       []ProviderStats        `json:"by_provider"`
	ByModel          []ModelStats           `json:"by_model"`
	RecentErrors     []ErrorEntry           `json:"recent_errors"`
	TimeSeries       []TimeSeriesPoint      `json:"time_series"`
}

type TimeSeriesPoint struct {
	Timestamp string             `json:"timestamp"`
	Providers map[string]float64 `json:"providers"` // provider -> request count in this bucket
	Latency   map[string]float64 `json:"latency"`   // provider -> avg latency ms
}

func (c *Collector) Snapshot() Snapshot {
	c.mu.Lock()
	c.cleanup()
	records := make([]RequestRecord, len(c.records))
	copy(records, c.records)
	c.mu.Unlock()

	snap := Snapshot{}
	snap.TotalRequests = len(records)

	if len(records) == 0 {
		return snap
	}

	// By provider
	provMap := map[string][]RequestRecord{}
	modelMap := map[string]int{}
	activeProviders := map[string]bool{}
	var totalLatency time.Duration
	var errorCount int

	for _, r := range records {
		provMap[r.Provider] = append(provMap[r.Provider], r)
		modelMap[r.Model]++
		activeProviders[r.Provider] = true
		totalLatency += r.Latency
		snap.TotalInputTokens += r.InputTokens
		snap.TotalOutputTokens += r.OutputTokens
		snap.TotalCost += r.Cost
		if r.Error {
			errorCount++
		}
	}

	snap.ActiveProviders = len(activeProviders)
	snap.AvgLatencyMs = float64(totalLatency.Milliseconds()) / float64(len(records))
	if len(records) > 0 {
		snap.ErrorRate = float64(errorCount) / float64(len(records)) * 100
	}

	window := time.Since(records[0].Timestamp)
	if window < time.Minute {
		window = time.Minute
	}

	for name, recs := range provMap {
		ps := ProviderStats{Name: name, TotalReqs: len(recs)}
		var latencies []float64
		var ttftSum float64
		var ttftCount int
		for _, r := range recs {
			if r.Error {
				ps.ErrorCount++
			}
			latencies = append(latencies, float64(r.Latency.Milliseconds()))
			ps.InputTokens += r.InputTokens
			ps.OutputTokens += r.OutputTokens
			ps.TotalCost += r.Cost
			if r.TTFT > 0 {
				ttftSum += float64(r.TTFT.Milliseconds())
				ttftCount++
			}
		}
		if ps.TotalReqs > 0 {
			ps.ErrorRate = float64(ps.ErrorCount) / float64(ps.TotalReqs) * 100
			ps.RPM = float64(ps.TotalReqs) / window.Minutes()
		}
		if ttftCount > 0 {
			ps.AvgTTFTMs = ttftSum / float64(ttftCount)
		}
		sort.Float64s(latencies)
		ps.LatencyP50ms = percentile(latencies, 0.50)
		ps.LatencyP95ms = percentile(latencies, 0.95)
		snap.ByProvider = append(snap.ByProvider, ps)
	}

	sort.Slice(snap.ByProvider, func(i, j int) bool {
		return snap.ByProvider[i].Name < snap.ByProvider[j].Name
	})

	for name, count := range modelMap {
		snap.ByModel = append(snap.ByModel, ModelStats{
			Name:      name,
			TotalReqs: count,
			RPM:       float64(count) / window.Minutes(),
		})
	}
	sort.Slice(snap.ByModel, func(i, j int) bool {
		return snap.ByModel[i].Name < snap.ByModel[j].Name
	})

	// Recent errors (last 20)
	for i := len(records) - 1; i >= 0 && len(snap.RecentErrors) < 20; i-- {
		r := records[i]
		if r.Error {
			snap.RecentErrors = append(snap.RecentErrors, ErrorEntry{
				Provider:  r.Provider,
				Model:     r.Model,
				Status:    r.Status,
				Message:   r.ErrorMsg,
				Timestamp: r.Timestamp.Format(time.RFC3339),
			})
		}
	}

	// Time series — 10-second buckets for the last 5 minutes
	snap.TimeSeries = buildTimeSeries(records, 10*time.Second)

	return snap
}

func buildTimeSeries(records []RequestRecord, bucketSize time.Duration) []TimeSeriesPoint {
	if len(records) == 0 {
		return nil
	}

	now := time.Now()
	start := now.Add(-5 * time.Minute)

	type bucket struct {
		counts    map[string]float64
		latencies map[string][]float64
	}

	numBuckets := int(5*time.Minute/bucketSize) + 1
	buckets := make([]bucket, numBuckets)
	for i := range buckets {
		buckets[i] = bucket{
			counts:    map[string]float64{},
			latencies: map[string][]float64{},
		}
	}

	for _, r := range records {
		if r.Timestamp.Before(start) {
			continue
		}
		idx := int(r.Timestamp.Sub(start) / bucketSize)
		if idx >= numBuckets {
			idx = numBuckets - 1
		}
		buckets[idx].counts[r.Provider]++
		buckets[idx].latencies[r.Provider] = append(buckets[idx].latencies[r.Provider], float64(r.Latency.Milliseconds()))
	}

	var points []TimeSeriesPoint
	for i, b := range buckets {
		if len(b.counts) == 0 {
			continue
		}
		ts := start.Add(time.Duration(i) * bucketSize)
		latAvg := map[string]float64{}
		for prov, lats := range b.latencies {
			var sum float64
			for _, l := range lats {
				sum += l
			}
			latAvg[prov] = sum / float64(len(lats))
		}
		points = append(points, TimeSeriesPoint{
			Timestamp: ts.Format(time.RFC3339),
			Providers: b.counts,
			Latency:   latAvg,
		})
	}

	return points
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := p * float64(len(sorted)-1)
	lower := int(math.Floor(idx))
	upper := int(math.Ceil(idx))
	if lower == upper {
		return sorted[lower]
	}
	frac := idx - float64(lower)
	return sorted[lower]*(1-frac) + sorted[upper]*frac
}
