package main

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	v1beta2 "github.com/project-kessel/kessel-sdk-go/kessel/inventory/v1beta2"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"
)

type reportResourceClient interface {
	ReportResource(context.Context, *v1beta2.ReportResourceRequest, ...grpc.CallOption) (*v1beta2.ReportResourceResponse, error)
}

type workload string

const (
	workloadCreate workload = "create"
	workloadUpdate workload = "update"
)

type benchmarkConfig struct {
	Requests     int
	Concurrency  int
	Warmup       int
	ResourcePool int
	RPCTimeout   time.Duration
	Workload     workload
	RunID        string
}

func (c benchmarkConfig) validate() error {
	if c.Requests < 1 {
		return fmt.Errorf("requests must be at least 1")
	}
	if c.Concurrency < 1 {
		return fmt.Errorf("concurrency must be at least 1")
	}
	if c.Warmup < 0 {
		return fmt.Errorf("warmup must not be negative")
	}
	if c.ResourcePool < 1 {
		return fmt.Errorf("resource pool must be at least 1")
	}
	if c.RPCTimeout <= 0 {
		return fmt.Errorf("RPC timeout must be positive")
	}
	if c.Workload != workloadCreate && c.Workload != workloadUpdate {
		return fmt.Errorf("workload must be %q or %q", workloadCreate, workloadUpdate)
	}
	if c.RunID == "" {
		return fmt.Errorf("run ID must not be empty")
	}
	return nil
}

type benchmarkResult struct {
	RunID             string
	Workload          workload
	Requests          int
	Successes         int
	Failures          int
	Concurrency       int
	ResourcePool      int
	Elapsed           time.Duration
	RequestsPerSecond float64
	Mean              time.Duration
	P50               time.Duration
	P90               time.Duration
	P95               time.Duration
	P99               time.Duration
	Max               time.Duration
}

func runBenchmark(ctx context.Context, client reportResourceClient, cfg benchmarkConfig) (benchmarkResult, error) {
	result := benchmarkResult{
		RunID:        cfg.RunID,
		Workload:     cfg.Workload,
		Requests:     cfg.Requests,
		Concurrency:  min(cfg.Concurrency, cfg.Requests),
		ResourcePool: cfg.ResourcePool,
	}
	if err := cfg.validate(); err != nil {
		return result, err
	}

	if cfg.Workload == workloadUpdate {
		for i := 0; i < cfg.ResourcePool; i++ {
			request := newReportResourceRequest(cfg, "seed", i)
			if _, err := callReportResource(ctx, client, request, cfg.RPCTimeout); err != nil {
				return result, fmt.Errorf("seed update resource %d: %w", i, err)
			}
		}
	}

	for i := 0; i < cfg.Warmup; i++ {
		request := newReportResourceRequest(cfg, "warmup", i)
		if _, err := callReportResource(ctx, client, request, cfg.RPCTimeout); err != nil {
			return result, fmt.Errorf("warmup request %d: %w", i, err)
		}
	}

	jobs := make(chan int)
	durations := make([]time.Duration, cfg.Requests)
	requestErrors := make([]error, cfg.Requests)

	var workers sync.WaitGroup
	workers.Add(result.Concurrency)
	for range result.Concurrency {
		go func() {
			defer workers.Done()
			for requestNumber := range jobs {
				request := newReportResourceRequest(cfg, "measure", requestNumber)
				durations[requestNumber], requestErrors[requestNumber] = callReportResource(
					ctx,
					client,
					request,
					cfg.RPCTimeout,
				)
			}
		}()
	}

	started := time.Now()
	for requestNumber := 0; requestNumber < cfg.Requests; requestNumber++ {
		jobs <- requestNumber
	}
	close(jobs)
	workers.Wait()
	result.Elapsed = time.Since(started)

	successfulDurations := make([]time.Duration, 0, cfg.Requests)
	var firstError error
	for requestNumber, err := range requestErrors {
		if err != nil {
			result.Failures++
			if firstError == nil {
				firstError = fmt.Errorf("request %d: %w", requestNumber, err)
			}
			continue
		}
		successfulDurations = append(successfulDurations, durations[requestNumber])
	}
	result.Successes = len(successfulDurations)
	populateLatencySummary(&result, successfulDurations)

	if result.Elapsed > 0 {
		result.RequestsPerSecond = float64(result.Successes) / result.Elapsed.Seconds()
	}
	if firstError != nil {
		return result, fmt.Errorf("%d of %d requests failed; first failure: %w", result.Failures, cfg.Requests, firstError)
	}
	return result, nil
}

func callReportResource(
	ctx context.Context,
	client reportResourceClient,
	request *v1beta2.ReportResourceRequest,
	timeout time.Duration,
) (time.Duration, error) {
	rpcContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	started := time.Now()
	_, err := client.ReportResource(rpcContext, request)
	return time.Since(started), err
}

func newReportResourceRequest(cfg benchmarkConfig, phase string, requestNumber int) *v1beta2.ReportResourceRequest {
	resourceNumber := requestNumber
	identityPhase := phase
	if cfg.Workload == workloadUpdate {
		resourceNumber = requestNumber % cfg.ResourcePool
		identityPhase = string(workloadUpdate)
	}

	localResourceID := stableUUID(cfg.RunID, identityPhase, "resource", strconv.Itoa(resourceNumber))
	workspaceID := stableUUID(cfg.RunID, "workspace")
	reporterInstanceID := stableUUID(cfg.RunID, "reporter-instance")
	insightsID := stableUUID(cfg.RunID, "insights", strconv.Itoa(resourceNumber))
	subscriptionManagerID := stableUUID(cfg.RunID, "subscription-manager", strconv.Itoa(resourceNumber))
	consoleHref := "https://console.example.test/insights/inventory/" + localResourceID
	reporterVersion := "1.0.0"

	return &v1beta2.ReportResourceRequest{
		Type:               "host",
		ReporterType:       "hbi",
		ReporterInstanceId: reporterInstanceID,
		Representations: &v1beta2.ResourceRepresentations{
			Metadata: &v1beta2.RepresentationMetadata{
				LocalResourceId: localResourceID,
				ApiHref:         "https://api.example.test/hosts/" + localResourceID,
				ConsoleHref:     &consoleHref,
				ReporterVersion: &reporterVersion,
			},
			Common: &structpb.Struct{Fields: map[string]*structpb.Value{
				"workspace_id": structpb.NewStringValue(workspaceID),
			}},
			Reporter: &structpb.Struct{Fields: map[string]*structpb.Value{
				"subscription_manager_id": structpb.NewStringValue(subscriptionManagerID),
				"insights_id":             structpb.NewStringValue(insightsID),
				"ansible_host":            structpb.NewStringValue(fmt.Sprintf("host-%d-%s", requestNumber, phase)),
			}},
		},
		WriteVisibility: v1beta2.WriteVisibility_MINIMIZE_LATENCY,
	}
}

func stableUUID(parts ...string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(strings.Join(parts, ":"))).String()
}

func populateLatencySummary(result *benchmarkResult, durations []time.Duration) {
	if len(durations) == 0 {
		return
	}

	sorted := append([]time.Duration(nil), durations...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	var total time.Duration
	for _, duration := range sorted {
		total += duration
	}
	result.Mean = total / time.Duration(len(sorted))
	result.P50 = percentile(sorted, 0.50)
	result.P90 = percentile(sorted, 0.90)
	result.P95 = percentile(sorted, 0.95)
	result.P99 = percentile(sorted, 0.99)
	result.Max = sorted[len(sorted)-1]
}

func percentile(sorted []time.Duration, quantile float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	index := int(math.Ceil(quantile*float64(len(sorted)))) - 1
	index = max(0, min(index, len(sorted)-1))
	return sorted[index]
}
