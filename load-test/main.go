package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	total := flag.Int("n", 500, "number of requests")
	concurrency := flag.Int("c", 50, "concurrency")
	url := flag.String("url", "http://localhost:8080/jobs", "target URL")
	body := flag.String("body", `{"type":"resize_image","payload":{"image_url":"http://api:8080/sample/image.png"}}`, "JSON request body")
	flag.Parse()

	jobs := make(chan int)
	latencies := make([]time.Duration, 0, *total)
	var latencyMu sync.Mutex
	var ok, failed atomic.Int64
	start := time.Now()
	client := &http.Client{Timeout: 30 * time.Second}

	var wg sync.WaitGroup
	for i := 0; i < *concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range jobs {
				req, err := http.NewRequest(http.MethodPost, *url, bytes.NewBufferString(*body))
				if err != nil {
					failed.Add(1)
					continue
				}
				req.Header.Set("Content-Type", "application/json")
				t0 := time.Now()
				resp, err := client.Do(req)
				elapsed := time.Since(t0)
				latencyMu.Lock()
				latencies = append(latencies, elapsed)
				latencyMu.Unlock()
				if err != nil {
					failed.Add(1)
					continue
				}
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				if resp.StatusCode >= 200 && resp.StatusCode < 300 {
					ok.Add(1)
				} else {
					failed.Add(1)
				}
			}
		}()
	}

	for i := 0; i < *total; i++ {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	duration := time.Since(start)
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	p50 := percentile(latencies, 0.50)
	p95 := percentile(latencies, 0.95)
	fmt.Printf("Requests: %d\n", *total)
	fmt.Printf("Concurrency: %d\n", *concurrency)
	fmt.Printf("Duration: %s\n", duration.Round(time.Millisecond))
	fmt.Printf("Requests/sec: %.2f\n", float64(*total)/duration.Seconds())
	fmt.Printf("Success: %d\n", ok.Load())
	fmt.Printf("Errors: %d\n", failed.Load())
	fmt.Printf("Error rate: %.2f%%\n", float64(failed.Load())/float64(*total)*100)
	fmt.Printf("p50 latency: %s\n", p50.Round(time.Millisecond))
	fmt.Printf("p95 latency: %s\n", p95.Round(time.Millisecond))
}

func percentile(values []time.Duration, p float64) time.Duration {
	if len(values) == 0 {
		return 0
	}
	idx := int(float64(len(values)-1) * p)
	return values[idx]
}
