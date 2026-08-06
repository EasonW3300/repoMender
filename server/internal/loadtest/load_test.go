package loadtest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/EasonW3300/repoMender/server/internal/httpserver"
)

type checker struct{}

func (checker) Ping(context.Context) error { return nil }

// TestM10LoadHealthEndpoint exercises the complete hardened HTTP stack with a
// bounded concurrent burst. The threshold is intentionally generous so this
// remains a regression signal, not a machine-specific performance benchmark.
func TestM10LoadHealthEndpoint(t *testing.T) {
	server := httpserver.New(checker{}).WithM10Hardening(httpserver.M10Options{
		ServiceVersion: "m10-load-test", RateLimitPerMinute: 1000,
	})
	testServer := httptest.NewServer(server.Handler())
	defer testServer.Close()
	client := testServer.Client()
	const requests = 512
	const workers = 32
	durations := make([]time.Duration, 0, requests)
	var mu sync.Mutex
	var wg sync.WaitGroup
	jobs := make(chan struct{}, requests)
	for i := 0; i < requests; i++ {
		jobs <- struct{}{}
	}
	close(jobs)
	started := time.Now()
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range jobs {
				requestStarted := time.Now()
				response, err := client.Get(testServer.URL + "/health/live")
				if err != nil {
					t.Errorf("health request failed: %v", err)
					continue
				}
				if response.StatusCode != http.StatusOK {
					t.Errorf("health status = %d", response.StatusCode)
				}
				response.Body.Close()
				mu.Lock()
				durations = append(durations, time.Since(requestStarted))
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(durations) != requests {
		t.Fatalf("completed requests = %d, want %d", len(durations), requests)
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	p95 := durations[(len(durations)*95)/100]
	if elapsed := time.Since(started); elapsed > 5*time.Second || p95 > time.Second {
		t.Fatalf("load regression: total=%s p95=%s", elapsed, p95)
	}
}
