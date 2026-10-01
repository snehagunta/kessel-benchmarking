package main

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	v1beta2 "github.com/project-kessel/kessel-sdk-go/kessel/inventory/v1beta2"
	"google.golang.org/grpc"
)

func TestNewReportResourceRequestCreateUsesCanonicalPayload(t *testing.T) {
	cfg := validBenchmarkConfig()
	first := newReportResourceRequest(cfg, "measure", 1)
	second := newReportResourceRequest(cfg, "measure", 2)

	if first.GetType() != "host" || first.GetReporterType() != "hbi" {
		t.Fatalf("unexpected type/reporter: %s/%s", first.GetType(), first.GetReporterType())
	}
	if first.GetRepresentations().GetMetadata().GetLocalResourceId() == second.GetRepresentations().GetMetadata().GetLocalResourceId() {
		t.Fatal("create workload reused a local resource ID")
	}
	if got := first.GetRepresentations().GetCommon().GetFields()["workspace_id"].GetStringValue(); got == "" {
		t.Fatal("workspace_id is empty")
	}
	reporterFields := first.GetRepresentations().GetReporter().GetFields()
	for _, field := range []string{"subscription_manager_id", "insights_id", "ansible_host"} {
		if _, ok := reporterFields[field]; !ok {
			t.Fatalf("canonical reporter field %q is missing", field)
		}
	}
	if got := first.GetRepresentations().GetMetadata().GetTransactionId(); got != "" {
		t.Fatalf("transaction ID must be omitted to avoid idempotent no-ops, got %q", got)
	}
	if first.GetWriteVisibility() != v1beta2.WriteVisibility_MINIMIZE_LATENCY {
		t.Fatalf("unexpected write visibility: %s", first.GetWriteVisibility())
	}
}

func TestNewReportResourceRequestUpdateUsesConfiguredPool(t *testing.T) {
	cfg := validBenchmarkConfig()
	cfg.Workload = workloadUpdate
	cfg.ResourcePool = 3

	first := newReportResourceRequest(cfg, "measure", 0)
	reused := newReportResourceRequest(cfg, "measure", 3)
	other := newReportResourceRequest(cfg, "measure", 1)

	firstID := first.GetRepresentations().GetMetadata().GetLocalResourceId()
	if got := reused.GetRepresentations().GetMetadata().GetLocalResourceId(); got != firstID {
		t.Fatalf("pooled update ID = %q, want %q", got, firstID)
	}
	if got := other.GetRepresentations().GetMetadata().GetLocalResourceId(); got == firstID {
		t.Fatal("different pool entries shared a local resource ID")
	}
	firstHost := first.GetRepresentations().GetReporter().GetFields()["ansible_host"].GetStringValue()
	reusedHost := reused.GetRepresentations().GetReporter().GetFields()["ansible_host"].GetStringValue()
	if firstHost == reusedHost {
		t.Fatal("update workload did not change representation data")
	}
}

func TestRunBenchmarkSeedsWarmsAndRunsConcurrently(t *testing.T) {
	client := &recordingClient{delay: 2 * time.Millisecond}
	cfg := validBenchmarkConfig()
	cfg.Workload = workloadUpdate
	cfg.ResourcePool = 3
	cfg.Warmup = 2
	cfg.Requests = 12
	cfg.Concurrency = 4

	result, err := runBenchmark(context.Background(), client, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if result.Successes != cfg.Requests || result.Failures != 0 {
		t.Fatalf("successes/failures = %d/%d", result.Successes, result.Failures)
	}
	if got, want := client.calls.Load(), int64(cfg.ResourcePool+cfg.Warmup+cfg.Requests); got != want {
		t.Fatalf("calls = %d, want %d", got, want)
	}
	if client.maxActive.Load() < 2 {
		t.Fatalf("maximum active calls = %d, want concurrent calls", client.maxActive.Load())
	}
	if result.RequestsPerSecond <= 0 || result.P50 <= 0 || result.Max < result.P50 {
		t.Fatalf("invalid benchmark summary: %+v", result)
	}
}

func TestRunBenchmarkCapsReportedConcurrency(t *testing.T) {
	cfg := validBenchmarkConfig()
	cfg.Requests = 2
	cfg.Concurrency = 20

	result, err := runBenchmark(context.Background(), &recordingClient{}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if result.Concurrency != cfg.Requests {
		t.Fatalf("concurrency = %d, want %d", result.Concurrency, cfg.Requests)
	}
}

func TestRunBenchmarkReturnsMeasuredFailures(t *testing.T) {
	client := &recordingClient{failCall: 2}
	cfg := validBenchmarkConfig()
	cfg.Requests = 4
	cfg.Warmup = 0

	result, err := runBenchmark(context.Background(), client, cfg)
	if err == nil {
		t.Fatal("expected benchmark error")
	}
	if result.Failures != 1 || result.Successes != 3 {
		t.Fatalf("successes/failures = %d/%d, want 3/1", result.Successes, result.Failures)
	}
}

func TestPercentileUsesNearestRank(t *testing.T) {
	durations := []time.Duration{time.Millisecond, 2 * time.Millisecond, 3 * time.Millisecond, 4 * time.Millisecond}
	if got := percentile(durations, 0.50); got != 2*time.Millisecond {
		t.Fatalf("p50 = %s, want 2ms", got)
	}
	if got := percentile(durations, 0.99); got != 4*time.Millisecond {
		t.Fatalf("p99 = %s, want 4ms", got)
	}
}

func validBenchmarkConfig() benchmarkConfig {
	return benchmarkConfig{
		Requests:     1,
		Concurrency:  1,
		Warmup:       0,
		ResourcePool: 1,
		RPCTimeout:   time.Second,
		Workload:     workloadCreate,
		RunID:        "test-run",
	}
}

type recordingClient struct {
	delay     time.Duration
	failCall  int64
	calls     atomic.Int64
	active    atomic.Int64
	maxActive atomic.Int64
	mu        sync.Mutex
	requests  []*v1beta2.ReportResourceRequest
}

func (c *recordingClient) ReportResource(
	ctx context.Context,
	request *v1beta2.ReportResourceRequest,
	_ ...grpc.CallOption,
) (*v1beta2.ReportResourceResponse, error) {
	callNumber := c.calls.Add(1)
	active := c.active.Add(1)
	defer c.active.Add(-1)
	for {
		maximum := c.maxActive.Load()
		if active <= maximum || c.maxActive.CompareAndSwap(maximum, active) {
			break
		}
	}

	c.mu.Lock()
	c.requests = append(c.requests, request)
	c.mu.Unlock()

	if c.delay > 0 {
		select {
		case <-time.After(c.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if c.failCall > 0 && callNumber == c.failCall {
		return nil, errors.New("injected failure")
	}
	return &v1beta2.ReportResourceResponse{}, nil
}
