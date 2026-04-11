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

type A2ARecord struct {
	Agent       string
	Method      string
	RoutingType string // "auto" or "explicit"
	Status      string // "completed", "failed", etc.
	Latency     time.Duration
	Score       float64
	Timestamp   time.Time
}

type Collector struct {
	mu         sync.RWMutex
	records    []RequestRecord
	a2aRecords []A2ARecord
	maxAge     time.Duration
	stop       chan struct{}
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

func (c *Collector) RecordA2A(r A2ARecord) {
	r.Timestamp = time.Now()
	c.mu.Lock()
	c.a2aRecords = append(c.a2aRecords, r)
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

	j := 0
	for _, r := range c.a2aRecords {
		if r.Timestamp.After(cutoff) {
			c.a2aRecords[j] = r
			j++
		}
	}
	c.a2aRecords = c.a2aRecords[:j]
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

type ModelProviderStats struct {
	Name         string  `json:"name"`
	TotalReqs    int     `json:"total_requests"`
	ErrorRate    float64 `json:"error_rate"`
	AvgLatencyMs float64 `json:"avg_latency_ms"`
	TrafficShare float64 `json:"traffic_share"`
}

type ModelStats struct {
	Name              string               `json:"name"`
	TotalReqs         int                  `json:"total_requests"`
	RPM               float64              `json:"rpm"`
	ErrorCount        int                  `json:"error_count"`
	ErrorRate         float64              `json:"error_rate"`
	AvgLatencyMs      float64              `json:"avg_latency_ms"`
	LatencyP95Ms      float64              `json:"latency_p95_ms"`
	TotalInputTokens  int                  `json:"total_input_tokens"`
	TotalOutputTokens int                  `json:"total_output_tokens"`
	TotalCost         float64              `json:"total_cost"`
	AvgTTFTMs         float64              `json:"avg_ttft_ms"`
	Providers         []ModelProviderStats  `json:"providers"`
}

type ErrorEntry struct {
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	Status    int    `json:"status"`
	Message   string `json:"message"`
	Timestamp string `json:"timestamp"`
}

type AgentStats struct {
	Name           string  `json:"name"`
	TotalTasks     int     `json:"total_tasks"`
	CompletedTasks int     `json:"completed_tasks"`
	FailedTasks    int     `json:"failed_tasks"`
	AvgLatencyMs   float64 `json:"avg_latency_ms"`
	LatencyP95Ms   float64 `json:"latency_p95_ms"`
	AvgScore       float64 `json:"avg_score"`
	Status         string  `json:"status"`
}

type Snapshot struct {
	TotalRequests     int               `json:"total_requests"`
	ActiveProviders   int               `json:"active_providers"`
	AvgLatencyMs      float64           `json:"avg_latency_ms"`
	ErrorRate         float64           `json:"error_rate"`
	TotalInputTokens  int               `json:"total_input_tokens"`
	TotalOutputTokens int               `json:"total_output_tokens"`
	TotalCost         float64           `json:"total_cost"`
	ByProvider        []ProviderStats   `json:"by_provider"`
	ByModel           []ModelStats      `json:"by_model"`
	RecentErrors      []ErrorEntry      `json:"recent_errors"`
	TimeSeries        []TimeSeriesPoint `json:"time_series"`

	// A2A stats
	A2ATotalTasks   int          `json:"a2a_total_tasks"`
	A2AErrorRate    float64      `json:"a2a_error_rate"`
	A2AAvgLatencyMs float64     `json:"a2a_avg_latency_ms"`
	ByAgent         []AgentStats `json:"by_agent"`
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
	a2aRecordsCopy := make([]A2ARecord, len(c.a2aRecords))
	copy(a2aRecordsCopy, c.a2aRecords)
	c.mu.Unlock()

	snap := Snapshot{}
	snap.TotalRequests = len(records)

	if len(records) == 0 {
		return snap
	}

	// By provider
	provMap := map[string][]RequestRecord{}
	modelMap := map[string][]RequestRecord{}
	activeProviders := map[string]bool{}
	var totalLatency time.Duration
	var errorCount int

	for _, r := range records {
		provMap[r.Provider] = append(provMap[r.Provider], r)
		modelMap[r.Model] = append(modelMap[r.Model], r)
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
	snap.ErrorRate = float64(errorCount) / float64(len(records)) * 100

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

	for name, recs := range modelMap {
		ms := ModelStats{Name: name, TotalReqs: len(recs)}
		var latencies []float64
		var latSum float64
		var ttftSum float64
		var ttftCount int
		modelProvMap := map[string][]RequestRecord{}
		for _, r := range recs {
			if r.Error {
				ms.ErrorCount++
			}
			latMs := float64(r.Latency.Milliseconds())
			latencies = append(latencies, latMs)
			latSum += latMs
			ms.TotalInputTokens += r.InputTokens
			ms.TotalOutputTokens += r.OutputTokens
			ms.TotalCost += r.Cost
			if r.TTFT > 0 {
				ttftSum += float64(r.TTFT.Milliseconds())
				ttftCount++
			}
			modelProvMap[r.Provider] = append(modelProvMap[r.Provider], r)
		}
		if ms.TotalReqs > 0 {
			ms.ErrorRate = float64(ms.ErrorCount) / float64(ms.TotalReqs) * 100
			ms.RPM = float64(ms.TotalReqs) / window.Minutes()
			ms.AvgLatencyMs = latSum / float64(len(latencies))
		}
		if ttftCount > 0 {
			ms.AvgTTFTMs = ttftSum / float64(ttftCount)
		}
		sort.Float64s(latencies)
		ms.LatencyP95Ms = percentile(latencies, 0.95)

		for provName, provRecs := range modelProvMap {
			mps := ModelProviderStats{
				Name:      provName,
				TotalReqs: len(provRecs),
			}
			var provErrCount int
			var provLatSum float64
			for _, r := range provRecs {
				if r.Error {
					provErrCount++
				}
				provLatSum += float64(r.Latency.Milliseconds())
			}
			if mps.TotalReqs > 0 {
				mps.ErrorRate = float64(provErrCount) / float64(mps.TotalReqs) * 100
				mps.AvgLatencyMs = provLatSum / float64(mps.TotalReqs)
				mps.TrafficShare = float64(mps.TotalReqs) / float64(ms.TotalReqs) * 100
			}
			ms.Providers = append(ms.Providers, mps)
		}
		sort.Slice(ms.Providers, func(i, j int) bool {
			return ms.Providers[i].TotalReqs > ms.Providers[j].TotalReqs
		})

		snap.ByModel = append(snap.ByModel, ms)
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

	// A2A stats
	a2aRecords := a2aRecordsCopy
	snap.A2ATotalTasks = len(a2aRecords)
	if len(a2aRecords) > 0 {
		agentMap := map[string][]A2ARecord{}
		var a2aErrors int
		var a2aLatSum float64
		for _, r := range a2aRecords {
			agentMap[r.Agent] = append(agentMap[r.Agent], r)
			a2aLatSum += float64(r.Latency.Milliseconds())
			if r.Status == "failed" {
				a2aErrors++
			}
		}
		snap.A2AErrorRate = float64(a2aErrors) / float64(len(a2aRecords)) * 100
		snap.A2AAvgLatencyMs = a2aLatSum / float64(len(a2aRecords))

		for name, recs := range agentMap {
			as := AgentStats{Name: name, TotalTasks: len(recs)}
			var latencies []float64
			var scoreSum float64
			var scoreCount int
			for _, r := range recs {
				latencies = append(latencies, float64(r.Latency.Milliseconds()))
				if r.Status == "completed" {
					as.CompletedTasks++
				} else if r.Status == "failed" {
					as.FailedTasks++
				}
				if r.Score > 0 {
					scoreSum += r.Score
					scoreCount++
				}
			}
			if len(latencies) > 0 {
				var sum float64
				for _, l := range latencies {
					sum += l
				}
				as.AvgLatencyMs = sum / float64(len(latencies))
				sort.Float64s(latencies)
				as.LatencyP95Ms = percentile(latencies, 0.95)
			}
			if scoreCount > 0 {
				as.AvgScore = scoreSum / float64(scoreCount)
			}
			snap.ByAgent = append(snap.ByAgent, as)
		}
		sort.Slice(snap.ByAgent, func(i, j int) bool {
			return snap.ByAgent[i].Name < snap.ByAgent[j].Name
		})
	}

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
