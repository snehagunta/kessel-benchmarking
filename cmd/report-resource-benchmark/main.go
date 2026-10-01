package main

import (
	"context"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/google/uuid"
	_ "github.com/joho/godotenv/autoload"
	v1beta2 "github.com/project-kessel/kessel-sdk-go/kessel/inventory/v1beta2"
)

type commandOptions struct {
	Endpoint     string
	Insecure     bool
	Requests     int
	Concurrency  int
	Warmup       int
	Runs         int
	ResourcePool int
	RPCTimeout   time.Duration
	Workload     string
	Output       string
}

func main() {
	if err := runCommand(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "report-resource benchmark:", err)
		os.Exit(1)
	}
}

func runCommand(args []string, stdout, stderr io.Writer) error {
	options, err := parseFlags(args, stderr)
	if err != nil {
		return err
	}
	if options.Runs < 1 {
		return fmt.Errorf("runs must be at least 1")
	}

	builder := v1beta2.NewClientBuilder(options.Endpoint)
	if options.Insecure {
		builder.Insecure()
	} else {
		builder.Unauthenticated(nil)
	}
	client, connection, err := builder.Build()
	if err != nil {
		return fmt.Errorf("build Inventory SDK client: %w", err)
	}
	defer func() {
		if closeErr := connection.Close(); closeErr != nil {
			fmt.Fprintln(stderr, "close Inventory SDK connection:", closeErr)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var firstRunError error
	for runNumber := 1; runNumber <= options.Runs; runNumber++ {
		cfg := benchmarkConfig{
			Requests:     options.Requests,
			Concurrency:  options.Concurrency,
			Warmup:       options.Warmup,
			ResourcePool: options.ResourcePool,
			RPCTimeout:   options.RPCTimeout,
			Workload:     workload(options.Workload),
			RunID:        uuid.NewString(),
		}
		result, runErr := runBenchmark(ctx, client, cfg)
		if runErr != nil && result.Elapsed == 0 {
			return fmt.Errorf("run %d setup: %w", runNumber, runErr)
		}

		printResult(stdout, runNumber, options.Endpoint, result)
		if options.Output != "" {
			if err := appendCSV(options.Output, runNumber, options.Endpoint, result); err != nil {
				return err
			}
		}
		if runErr != nil && firstRunError == nil {
			firstRunError = fmt.Errorf("run %d: %w", runNumber, runErr)
		}
	}

	return firstRunError
}

func parseFlags(args []string, stderr io.Writer) (commandOptions, error) {
	options := commandOptions{}
	flags := flag.NewFlagSet("report-resource-benchmark", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&options.Endpoint, "endpoint", envOrDefault("KESSEL_GRPC_ENDPOINT", "localhost:9000"), "Inventory gRPC endpoint")
	flags.BoolVar(&options.Insecure, "insecure", envBoolOrDefault("KESSEL_INSECURE", true), "use a plaintext, unauthenticated gRPC connection")
	flags.IntVar(&options.Requests, "requests", 1000, "measured requests per run")
	flags.IntVar(&options.Concurrency, "concurrency", 1, "number of concurrent request workers")
	flags.IntVar(&options.Warmup, "warmup", 10, "untimed requests before each run")
	flags.IntVar(&options.Runs, "runs", 1, "number of benchmark runs")
	flags.IntVar(&options.ResourcePool, "resource-pool", 1, "number of pre-seeded resources used by update workloads")
	flags.DurationVar(&options.RPCTimeout, "rpc-timeout", 5*time.Second, "timeout for each ReportResource RPC")
	flags.StringVar(&options.Workload, "workload", string(workloadCreate), "workload: create or update")
	flags.StringVar(&options.Output, "output", "", "optional CSV summary path")
	if err := flags.Parse(args); err != nil {
		return commandOptions{}, err
	}
	if flags.NArg() != 0 {
		return commandOptions{}, fmt.Errorf("unexpected positional arguments: %v", flags.Args())
	}
	return options, nil
}

func printResult(writer io.Writer, runNumber int, endpoint string, result benchmarkResult) {
	fmt.Fprintf(
		writer,
		"run=%d endpoint=%s workload=%s requests=%d successes=%d failures=%d concurrency=%d resource_pool=%d\n",
		runNumber,
		endpoint,
		result.Workload,
		result.Requests,
		result.Successes,
		result.Failures,
		result.Concurrency,
		result.ResourcePool,
	)
	fmt.Fprintf(writer, "elapsed=%s throughput=%.2f requests/s\n", result.Elapsed, result.RequestsPerSecond)
	fmt.Fprintf(
		writer,
		"latency mean=%s p50=%s p90=%s p95=%s p99=%s max=%s\n",
		result.Mean,
		result.P50,
		result.P90,
		result.P95,
		result.P99,
		result.Max,
	)
}

func appendCSV(path string, runNumber int, endpoint string, result benchmarkResult) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create output CSV directory: %w", err)
	}

	fileInfo, statErr := os.Stat(path)
	writeHeader := os.IsNotExist(statErr) || statErr == nil && fileInfo.Size() == 0
	if statErr != nil && !os.IsNotExist(statErr) {
		return fmt.Errorf("stat output CSV: %w", statErr)
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open output CSV: %w", err)
	}
	defer func() { _ = file.Close() }()

	writer := csv.NewWriter(file)
	if writeHeader {
		if err := writer.Write([]string{
			"timestamp",
			"run",
			"run_id",
			"endpoint",
			"workload",
			"requests",
			"successes",
			"failures",
			"concurrency",
			"resource_pool",
			"elapsed_ms",
			"requests_per_second",
			"mean_ms",
			"p50_ms",
			"p90_ms",
			"p95_ms",
			"p99_ms",
			"max_ms",
		}); err != nil {
			return fmt.Errorf("write output CSV header: %w", err)
		}
	}

	if err := writer.Write([]string{
		time.Now().UTC().Format(time.RFC3339Nano),
		strconv.Itoa(runNumber),
		result.RunID,
		endpoint,
		string(result.Workload),
		strconv.Itoa(result.Requests),
		strconv.Itoa(result.Successes),
		strconv.Itoa(result.Failures),
		strconv.Itoa(result.Concurrency),
		strconv.Itoa(result.ResourcePool),
		milliseconds(result.Elapsed),
		strconv.FormatFloat(result.RequestsPerSecond, 'f', 2, 64),
		milliseconds(result.Mean),
		milliseconds(result.P50),
		milliseconds(result.P90),
		milliseconds(result.P95),
		milliseconds(result.P99),
		milliseconds(result.Max),
	}); err != nil {
		return fmt.Errorf("write output CSV row: %w", err)
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return fmt.Errorf("flush output CSV: %w", err)
	}
	return nil
}

func milliseconds(duration time.Duration) string {
	return strconv.FormatFloat(float64(duration)/float64(time.Millisecond), 'f', 3, 64)
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envBoolOrDefault(key string, fallback bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}
