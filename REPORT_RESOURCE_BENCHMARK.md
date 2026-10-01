# ReportResource benchmark

This benchmark drives the Inventory API through the official Go SDK and its
`v1beta2` gRPC client. It reuses one SDK connection per process and reports
successful-request throughput plus mean, p50, p90, p95, p99, and maximum RPC
latency.

The command requires Go 1.25 or newer because it uses `kessel-sdk-go` v1.12.0.

The generated payload is a valid `host`/`hbi` resource. It uses the canonical
`workspace_id`, `subscription_manager_id`, `insights_id`, and `ansible_host`
field names expected by Inventory. Requests use `MINIMIZE_LATENCY` and omit
`transaction_id`, so repeated calls are not mistaken for idempotent replays.

The existing JSONL and Zipf data generators remain under
`benchmark/input_files`. The SDK command currently generates canonical
synthetic `host`/`hbi` requests directly; it does not consume those JSONL files.

## Start Inventory with a local PostgreSQL database

From the sibling `inventory-api` checkout:

```sh
cd ../inventory-api
make db/setup
make migrate
INVENTORY_API_LOG_LEVEL=error make run
```

This uses PostgreSQL on `localhost:5435`, Inventory gRPC on `localhost:9000`,
the local unauthenticated gRPC configuration, `allow-all` authorization, and
the no-op outbox. It intentionally excludes Kafka and Relations so the first
measurements isolate Inventory and PostgreSQL.

If another local stack owns the default server ports, Inventory's ports can be
overridden without changing source. After the setup above, for example:

```sh
INVENTORY_API_LOG_LEVEL=error ./bin/inventory-api serve \
  --config .inventory-api.yaml \
  --server.http.address localhost:8001 \
  --server.grpc.address localhost:9001 \
  --server.public_url http://localhost:8001
```

Pass `-endpoint=localhost:9001` to the benchmark in that configuration.

Before starting a benchmark, verify that the readiness response body contains
`"code": 200`. Use port `8001` instead for the alternate configuration above:

```sh
curl -sS http://localhost:8000/api/kessel/v1/readyz
```

## Run create workloads

From this repository:

```sh
go run ./cmd/report-resource-benchmark \
  -workload=create \
  -requests=10000 \
  -concurrency=16 \
  -warmup=20 \
  -runs=3 \
  -output=results/report-resource-create.csv
```

Every measured create request has a unique natural key. Warmup requests also
create resources, but are excluded from the reported sample. With a positive
`-warmup`, the lazy gRPC connection is established before measurement. Set
`-warmup=0` when an exact total call count matters; the first measured request
will then include connection setup.

Each run uses a fresh run ID, so `-runs` accumulates resources in the database.
Use a fresh database or reset it between runs when comparing equal starting
cardinalities.

## Run update workloads

A pool of resources is seeded before timing. A pool of one creates a hot-key
contention test:

```sh
go run ./cmd/report-resource-benchmark \
  -workload=update \
  -resource-pool=1 \
  -requests=10000 \
  -concurrency=16 \
  -output=results/report-resource-hot-update.csv
```

A larger pool distributes updates across keys:

```sh
go run ./cmd/report-resource-benchmark \
  -workload=update \
  -resource-pool=1000 \
  -requests=10000 \
  -concurrency=16 \
  -output=results/report-resource-distributed-update.csv
```

Seed and warmup requests are excluded from measurement. Each timed update
changes `ansible_host`, while the resource natural key remains stable.

## Useful flags

- `-endpoint`: Inventory gRPC endpoint; defaults to `KESSEL_GRPC_ENDPOINT` or
  `localhost:9000`.
- `-insecure`: use plaintext unauthenticated gRPC; enabled by default for the
  local Inventory configuration and configurable with `KESSEL_INSECURE`.
- `-rpc-timeout`: deadline for each RPC; defaults to five seconds.
- `-concurrency`: number of workers sharing one multiplexed SDK connection.
- `-output`: append one summary row per run to a CSV file.

The command loads `.env` automatically before reading those environment
defaults. Explicit flags take precedence. The `/results` directory is ignored
by Git so generated measurements do not enter commits accidentally.

Per-request latency covers the gRPC call only. Throughput covers the complete
concurrent request loop, including client-side request construction and worker
scheduling. Any failed request makes the command exit nonzero; failed requests
are counted but excluded from latency percentiles and successful-request
throughput.

For a second, more production-shaped comparison, stop the minimal stack and
run `LOG_LEVEL=error make inventory-up` in `inventory-api`. That keeps gRPC on
port `9000` but adds the WAL outbox, Kafka, Connect, and the Inventory consumer.
