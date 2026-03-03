package main

import (
	"bufio"
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type demoClient struct {
	id   int
	key  string
	http *http.Client
}

type runtimeSample struct {
	maxThreads    int
	maxGoroutines int
}

type opResult struct {
	latency time.Duration
	err     error
}

type benchStats struct {
	launched   int
	success    int
	failed     int
	elapsed    time.Duration
	throughput float64
	avg        time.Duration
	p50        time.Duration
	p95        time.Duration
	p99        time.Duration
	min        time.Duration
	max        time.Duration
	errors     []string
}

func main() {
	var (
		endpoint      string
		bucket        string
		keyPrefix     string
		objectSize    int
		clientCount   int
		targetRPS     float64
		duration      time.Duration
		reqTimeout    time.Duration
		statsInterval time.Duration
		verifyResult  bool
		maxConns      int
	)

	flag.StringVar(&endpoint, "endpoint", "http://127.0.0.1:10090", "TCP S3 endpoint URL")
	flag.StringVar(&bucket, "bucket", "bench-bucket", "bucket name")
	flag.StringVar(&keyPrefix, "key-prefix", "tcp-demo", "object key prefix")
	flag.IntVar(&objectSize, "object-size", 256*1024, "object payload size in bytes")
	flag.IntVar(&clientCount, "client-count", 140, "number of logical clients")
	flag.Float64Var(&targetRPS, "target-rps", 500, "global target requests per second")
	flag.DurationVar(&duration, "duration", 20*time.Second, "benchmark duration")
	flag.DurationVar(&reqTimeout, "request-timeout", 10*time.Second, "per-request timeout")
	flag.DurationVar(&statsInterval, "stats-interval", time.Second, "runtime stats sampling interval (<=0 disables)")
	flag.BoolVar(&verifyResult, "verify-result", true, "verify GET payload")
	flag.IntVar(&maxConns, "max-conns-per-host", 1, "max tcp connections per host for each logical client")
	flag.Parse()

	if strings.TrimSpace(endpoint) == "" {
		fatalf("endpoint must not be empty")
	}
	if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
		endpoint = "http://" + endpoint
	}
	endpoint = strings.TrimRight(endpoint, "/")
	if strings.TrimSpace(bucket) == "" {
		fatalf("bucket must not be empty")
	}
	if strings.TrimSpace(keyPrefix) == "" {
		fatalf("key-prefix must not be empty")
	}
	if objectSize < 0 {
		fatalf("object-size must be >= 0")
	}
	if clientCount <= 0 {
		fatalf("client-count must be > 0")
	}
	if targetRPS <= 0 {
		fatalf("target-rps must be > 0")
	}
	if duration <= 0 {
		fatalf("duration must be > 0")
	}
	if reqTimeout <= 0 {
		fatalf("request-timeout must be > 0")
	}
	if maxConns <= 0 {
		fatalf("max-conns-per-host must be > 0")
	}

	startGoroutines := runtime.NumGoroutine()
	startCGOCalls := runtime.NumCgoCall()
	startThreads, startThreadsErr := readThreadCount()
	if startThreadsErr != nil {
		startThreads = -1
	}

	stopStats := make(chan struct{})
	statsDone := make(chan runtimeSample, 1)
	if statsInterval > 0 {
		go monitorRuntime(statsInterval, stopStats, statsDone)
	}

	payload := makePayload(objectSize)
	clients := make([]demoClient, 0, clientCount)
	transports := make([]*http.Transport, 0, clientCount)
	defer func() {
		for _, tr := range transports {
			if tr != nil {
				tr.CloseIdleConnections()
			}
		}
	}()

	for i := 0; i < clientCount; i++ {
		key := objectKey(keyPrefix, i, clientCount)
		tr := newHTTPTransport(maxConns)
		hc := &http.Client{
			Transport: tr,
		}
		transports = append(transports, tr)
		clients = append(clients, demoClient{
			id:   i,
			key:  key,
			http: hc,
		})
	}

	if err := ensureBucket(clients[0].http, endpoint, bucket, reqTimeout); err != nil {
		fatalf("ensure bucket failed: %v", err)
	}
	for _, c := range clients {
		if err := putObject(c.http, endpoint, bucket, c.key, payload, reqTimeout); err != nil {
			fatalf("put object failed (client=%d): %v", c.id, err)
		}
	}

	stats := runOpenLoop(clients, endpoint, bucket, targetRPS, duration, reqTimeout, payload, verifyResult)
	if stats.failed > 0 {
		for _, msg := range stats.errors {
			fmt.Fprintf(os.Stderr, "sample error: %s\n", msg)
		}
		fatalf("benchmark failed success=%d failed=%d", stats.success, stats.failed)
	}

	maxThreads := startThreads
	maxGoroutines := startGoroutines
	if statsInterval > 0 {
		close(stopStats)
		sample := <-statsDone
		if sample.maxThreads > maxThreads {
			maxThreads = sample.maxThreads
		}
		if sample.maxGoroutines > maxGoroutines {
			maxGoroutines = sample.maxGoroutines
		}
	}
	endThreads, endThreadsErr := readThreadCount()
	if endThreadsErr != nil {
		endThreads = -1
	}
	endGoroutines := runtime.NumGoroutine()
	endCGOCalls := runtime.NumCgoCall()
	if endThreads > maxThreads {
		maxThreads = endThreads
	}
	if endGoroutines > maxGoroutines {
		maxGoroutines = endGoroutines
	}

	fmt.Printf(
		"tcp demo ok endpoint=%s bucket=%s key_prefix=%s clients=%d object_size=%d\n",
		endpoint,
		bucket,
		keyPrefix,
		clientCount,
		objectSize,
	)
	printBenchStats(stats)
	fmt.Printf(
		"runtime stats: goroutines start=%d peak=%d end=%d threads start=%d peak=%d end=%d cgo_calls_delta=%d\n",
		startGoroutines,
		maxGoroutines,
		endGoroutines,
		startThreads,
		maxThreads,
		endThreads,
		endCGOCalls-startCGOCalls,
	)
	if startThreadsErr != nil || endThreadsErr != nil {
		fmt.Printf("thread stat warning: start_err=%v end_err=%v\n", startThreadsErr, endThreadsErr)
	}
}

func runOpenLoop(
	clients []demoClient,
	endpoint string,
	bucket string,
	targetRPS float64,
	duration time.Duration,
	reqTimeout time.Duration,
	expected []byte,
	verify bool,
) benchStats {
	interval := openLoopInterval(targetRPS)
	results := make(chan opResult, 4096)

	var (
		durations = make([]time.Duration, 0, 1024)
		errs      = make([]string, 0, 8)
		failed    = 0
	)
	var collectMu sync.Mutex
	var collectWG sync.WaitGroup
	collectWG.Add(1)
	go func() {
		defer collectWG.Done()
		for res := range results {
			collectMu.Lock()
			if res.err != nil {
				failed++
				if len(errs) < cap(errs) {
					errs = append(errs, res.err.Error())
				}
				collectMu.Unlock()
				continue
			}
			durations = append(durations, res.latency)
			collectMu.Unlock()
		}
	}()

	var runWG sync.WaitGroup
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	timer := time.NewTimer(duration)
	defer timer.Stop()

	start := time.Now()
	launched := 0
	scheduling := true
	for scheduling {
		select {
		case <-timer.C:
			scheduling = false
		case <-ticker.C:
			jobIdx := launched
			launched++
			client := clients[jobIdx%len(clients)]
			runWG.Add(1)
			go func(c demoClient) {
				defer runWG.Done()
				opStart := time.Now()
				err := getObject(c.http, endpoint, bucket, c.key, expected, reqTimeout, verify)
				results <- opResult{
					latency: time.Since(opStart),
					err:     err,
				}
			}(client)
		}
	}

	runWG.Wait()
	close(results)
	collectWG.Wait()

	elapsed := time.Since(start)
	return summarizeBench(launched, durations, failed, elapsed, errs)
}

func summarizeBench(launched int, durations []time.Duration, failed int, elapsed time.Duration, errors []string) benchStats {
	success := len(durations)
	stats := benchStats{
		launched: launched,
		success:  success,
		failed:   failed,
		elapsed:  elapsed,
		errors:   errors,
	}
	if elapsed > 0 {
		stats.throughput = float64(success) / elapsed.Seconds()
	}
	if success == 0 {
		return stats
	}
	sort.Slice(durations, func(i, j int) bool {
		return durations[i] < durations[j]
	})
	var total time.Duration
	for _, d := range durations {
		total += d
	}
	stats.avg = total / time.Duration(success)
	stats.min = durations[0]
	stats.max = durations[success-1]
	stats.p50 = percentile(durations, 0.50)
	stats.p95 = percentile(durations, 0.95)
	stats.p99 = percentile(durations, 0.99)
	return stats
}

func printBenchStats(stats benchStats) {
	fmt.Printf("benchmark mode=open-loop launched=%d success=%d failed=%d elapsed=%s throughput=%.2f req/s\n",
		stats.launched,
		stats.success,
		stats.failed,
		stats.elapsed,
		stats.throughput,
	)
	fmt.Printf("latency avg=%s p50=%s p95=%s p99=%s min=%s max=%s\n",
		stats.avg,
		stats.p50,
		stats.p95,
		stats.p99,
		stats.min,
		stats.max,
	)
}

func ensureBucket(client *http.Client, endpoint, bucket string, reqTimeout time.Duration) error {
	u := endpoint + "/" + url.PathEscape(bucket)
	reqCtx, cancel := context.WithTimeout(context.Background(), reqTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPut, u, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("unexpected status=%d", resp.StatusCode)
	}
	return nil
}

func putObject(client *http.Client, endpoint, bucket, key string, payload []byte, reqTimeout time.Duration) error {
	u := endpoint + "/" + url.PathEscape(bucket) + "/" + url.PathEscape(key)
	reqCtx, cancel := context.WithTimeout(context.Background(), reqTimeout)
	defer cancel()

	body := bytes.NewReader(payload)
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPut, u, body)
	if err != nil {
		return err
	}
	req.ContentLength = int64(len(payload))
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("unexpected status=%d", resp.StatusCode)
	}
	return nil
}

func getObject(
	client *http.Client,
	endpoint, bucket, key string,
	expected []byte,
	reqTimeout time.Duration,
	verify bool,
) error {
	u := endpoint + "/" + url.PathEscape(bucket) + "/" + url.PathEscape(key)
	reqCtx, cancel := context.WithTimeout(context.Background(), reqTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("unexpected status=%d body=%q", resp.StatusCode, string(body))
	}
	if !verify {
		_, err = io.Copy(io.Discard, resp.Body)
		return err
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if len(body) != len(expected) {
		return fmt.Errorf("size mismatch expected=%d got=%d", len(expected), len(body))
	}
	if !bytes.Equal(body, expected) {
		return fmt.Errorf("payload mismatch")
	}
	return nil
}

func objectKey(prefix string, i int, clientCount int) string {
	if clientCount <= 1 {
		return prefix
	}
	return fmt.Sprintf("%s-c%03d", prefix, i)
}

func makePayload(n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = byte((i*31 + 17) & 0xff)
	}
	return out
}

func newHTTPTransport(maxConns int) *http.Transport {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	tr.MaxConnsPerHost = maxConns
	tr.MaxIdleConnsPerHost = maxConns
	if tr.MaxIdleConns < maxConns {
		tr.MaxIdleConns = maxConns
	}
	return tr
}

func openLoopInterval(targetRPS float64) time.Duration {
	interval := time.Duration(float64(time.Second) / targetRPS)
	if interval < time.Nanosecond {
		return time.Nanosecond
	}
	return interval
}

func percentile(durations []time.Duration, p float64) time.Duration {
	if len(durations) == 0 {
		return 0
	}
	if p <= 0 {
		return durations[0]
	}
	if p >= 1 {
		return durations[len(durations)-1]
	}
	idx := int(float64(len(durations)-1) * p)
	return durations[idx]
}

func monitorRuntime(interval time.Duration, stop <-chan struct{}, done chan<- runtimeSample) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	maxThreads, err := readThreadCount()
	if err != nil {
		maxThreads = -1
	}
	maxGoroutines := runtime.NumGoroutine()

	update := func() {
		if g := runtime.NumGoroutine(); g > maxGoroutines {
			maxGoroutines = g
		}
		if t, err := readThreadCount(); err == nil && t > maxThreads {
			maxThreads = t
		}
	}

	for {
		select {
		case <-ticker.C:
			update()
		case <-stop:
			update()
			done <- runtimeSample{
				maxThreads:    maxThreads,
				maxGoroutines: maxGoroutines,
			}
			return
		}
	}
}

func readThreadCount() (int, error) {
	f, err := os.Open("/proc/self/status")
	if err == nil {
		defer f.Close()
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "Threads:") {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) < 2 {
				break
			}
			n, parseErr := strconv.Atoi(fields[1])
			if parseErr != nil {
				return 0, parseErr
			}
			return n, nil
		}
		if scanErr := scanner.Err(); scanErr != nil {
			return 0, scanErr
		}
	}

	entries, dirErr := os.ReadDir("/proc/self/task")
	if dirErr != nil {
		if err != nil {
			return 0, fmt.Errorf("status=%v task=%w", err, dirErr)
		}
		return 0, dirErr
	}
	return len(entries), nil
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
