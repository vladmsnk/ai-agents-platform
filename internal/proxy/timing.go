package proxy

import (
	"bytes"
	"io"
	"sync"
	"time"
)

// TimingReader wraps an io.Reader to measure TTFT (time to first byte read)
// and count SSE chunks for TPOT calculation.
type TimingReader struct {
	src       io.Reader
	start     time.Time
	firstByte time.Time
	once      sync.Once
	chunks    int
	usage     Usage // populated if found in stream
}

func NewTimingReader(src io.Reader, start time.Time) *TimingReader {
	return &TimingReader{
		src:   src,
		start: start,
	}
}

func (t *TimingReader) Read(p []byte) (int, error) {
	n, err := t.src.Read(p)
	if n > 0 {
		t.once.Do(func() {
			t.firstByte = time.Now()
		})
		// Count SSE data lines for chunk tracking
		t.chunks += bytes.Count(p[:n], []byte("data: "))

		// Try to extract usage from the last SSE chunk
		if idx := bytes.LastIndex(p[:n], []byte("data: ")); idx >= 0 {
			dataStart := idx + 6
			end := bytes.Index(p[dataStart:n], []byte("\n"))
			if end < 0 {
				end = n - dataStart
			}
			chunk := p[dataStart : dataStart+end]
			if !bytes.Equal(chunk, []byte("[DONE]")) {
				if u := parseUsageFromSSEChunk(chunk); u.TotalTokens > 0 {
					t.usage = u
				}
			}
		}
	}
	return n, err
}

// TTFT returns the time to first token (first byte received from upstream).
func (t *TimingReader) TTFT() time.Duration {
	if t.firstByte.IsZero() {
		return 0
	}
	return t.firstByte.Sub(t.start)
}

// ChunkCount returns the number of SSE data chunks observed.
func (t *TimingReader) ChunkCount() int {
	return t.chunks
}

// StreamUsage returns any usage data found in the stream.
func (t *TimingReader) StreamUsage() Usage {
	return t.usage
}

// TPOT returns time per output token for streaming.
// Calculated as (total_time - TTFT) / output_tokens.
func (t *TimingReader) TPOT(totalDuration time.Duration, outputTokens int) time.Duration {
	if outputTokens <= 1 {
		return 0
	}
	ttft := t.TTFT()
	if ttft == 0 {
		return 0
	}
	generationTime := totalDuration - ttft
	return generationTime / time.Duration(outputTokens-1)
}
