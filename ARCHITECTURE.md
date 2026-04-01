# Architecture — cloudsql-migrator

> Last updated: 2026-04-01 by repo-scout

---

## 1. Project Purpose

`cloudsql-migrator` is a CLI tool for migrating PostgreSQL databases between Cloud SQL instances on the NAIS platform (nais.io). It automates a multi-phase, zero-downtime-minimising migration using Google Cloud Database Migration Service (DMS) with pglogical continuous replication. The tool is designed to be called by `nais-cli` and reports numbered progress steps so the CLI can render a progress bar.

The four phases are:
- **setup** — create target instance, configure replication, start DMS migration job
- **promote** — verify lag, scale app to zero, promote replica, update app config, scale back up
- **finalize** — delete source instance, DMS job, connection profiles, and leftover K8s resources
- **rollback** — undo a setup/promote that went wrong

---

## 2. Detected Stack

| Concern           | Detail                                                                                                  | Evidence                                          |
|-------------------|---------------------------------------------------------------------------------------------------------|---------------------------------------------------|
| Language          | **Go 1.25**                                                                                             | `go.mod:3`                                        |
| Module path       | `github.com/nais/cloudsql-migrator`                                                                     | `go.mod:1`                                        |
| K8s client        | `k8s.io/client-go`, `k8s.io/apimachinery`, dynamic client                                               | `go.mod`, `internal/pkg/k8s/generic_client.go`    |
| NAIS CRDs         | `github.com/nais/liberator` (Application CRD types)                                                     | `go.mod:18`, `internal/pkg/common_main/main.go`   |
| CNRM CRDs         | `github.com/GoogleCloudPlatform/k8s-config-connector` (SQLInstance, SQLUser, …)                         | `go.mod:15`, `internal/pkg/k8s/generic_client.go` |
| GCP APIs          | `cloud.google.com/go/clouddms`, `google.golang.org/api/sqladmin`, `google.golang.org/api/datamigration` | `go.mod`, `internal/pkg/common_main/main.go`      |
| GCP Monitoring    | `cloud.google.com/go/monitoring` (replication lag checks)                                               | `go.mod:14`, `internal/pkg/promote/promote.go`    |
| Retry             | `github.com/sethvargo/go-retry`                                                                         | `go.mod:22`                                       |
| Config from env   | `github.com/sethvargo/go-envconfig`                                                                     | `go.mod:21`, all `cmd/*/main.go`                  |
| Logging           | stdlib `log/slog` (text or JSON, configurable)                                                          | `internal/pkg/config/logging.go`                  |
| PostgreSQL driver | `github.com/lib/pq`                                                                                     | `go.mod:17`                                       |
| Concurrency       | `golang.org/x/sync/errgroup`                                                                            | `internal/pkg/instance/instance.go`               |
| Testing framework | **Ginkgo v2 + Gomega**                                                                                  | `go.mod:19-20`, `*_suite_test.go`                 |
| Static analysis   | `honnef.co/go/tools` (staticcheck)                                                                      | `tools.go`, `Makefile`                            |
| Vuln scan         | `golang.org/x/vuln` (govulncheck)                                                                       | `tools.go`, `Makefile`                            |
| Container base    | `gcr.io/distroless/static-debian11`                                                                     | `Dockerfile:28`                                   |
| Registry          | `europe-north1-docker.pkg.dev`                                                                          | `.github/workflows/main.yml`                      |

---

## 3. Directory Structure

```
.
├── cmd/                    # One main package per binary / phase
│   ├── setup/main.go       # Phase 1 entry point
│   ├── promote/main.go     # Phase 2 entry point
│   ├── finalize/main.go    # Phase 3 entry point
│   └── rollback/main.go    # Rollback entry point
├── internal/pkg/           # All shared library code
│   ├── application/        # Scale, update, delete NAIS Application resources
│   ├── backup/             # Create Cloud SQL backups via SQL Admin API
│   ├── common_main/        # Manager struct + shared init (K8s clients, GCP clients)
│   ├── config/             # Config structs (env-tag driven), logging setup, dev flags
│   ├── database/           # SQL-level operations (passwords, pglogical, ownership)
│   ├── instance/           # Cloud SQL instance CRUD, auth-networks, flags, SSL certs
│   ├── k8s/                # Generic typed Kubernetes dynamic client wrapper
│   ├── migration/          # DMS migration job lifecycle
│   ├── netpol/             # Kubernetes NetworkPolicy management
│   ├── promote/            # Promotion readiness checks, lag monitoring, promote call
│   └── resolved/           # Runtime-resolved types (GcpProject, Instance) via K8s lookup
├── hack/                   # Developer helper scripts (local run, cleanup, env, deploy_test_app)
├── img/                    # Architecture diagrams (PNG)
├── bin/                    # Build output (gitignored)
├── tools.go                # go:build tools — pins staticcheck, govulncheck, ginkgo CLI
├── go.mod / go.sum
├── Makefile
├── Dockerfile
└── .github/
    ├── workflows/main.yml          # Build, push, sign, release
    ├── workflows/codeql.yml        # CodeQL security scanning
    └── dependabot.yml              # Daily gomod + Actions updates
```

---

## 4. Build System and Tooling

### Makefile targets

| Target          | Command                                                                                              |
|-----------------|------------------------------------------------------------------------------------------------------|
| `make test`     | `go test ./... -v -count=1 -coverprofile cover.out`                                                  |
| `make check`    | `go run honnef.co/go/tools/cmd/staticcheck ./...` + `go run golang.org/x/vuln/cmd/govulncheck ./...` |
| `make setup`    | `go build -installsuffix cgo -o bin/setup cmd/setup/main.go`                                         |
| `make promote`  | `go build -installsuffix cgo -o bin/promote cmd/promote/main.go`                                     |
| `make finalize` | `go build -installsuffix cgo -o bin/finalize cmd/finalize/main.go`                                   |
| `make rollback` | `go build -installsuffix cgo -o bin/rollback cmd/rollback/main.go`                                   |
| `make all`      | All four build targets above                                                                         |

### Docker build (in CI and for final images)

The Dockerfile is multi-stage:
1. `golang:1.25` builder: downloads deps, builds stdlib, runs `make test && make check`, then `CGO_ENABLED=0 make all`
2. `gcr.io/distroless/static-debian11`: copies the four binaries — no shell, no runtime libs

### CI (GitHub Actions — `.github/workflows/main.yml`)

- Runs on push (not `*.md`), weekly schedule, and `workflow_dispatch`
- Uses `nais/platform-build-push-sign` to build, push, and cosign-sign the image
- On `main` branch: creates a GitHub release with auto-generated notes
- Multi-platform build with GHA layer cache
- Dependabot: daily updates for `gomod` (with k8s group) and `github-actions`
- CodeQL: weekly Go security analysis

### Static analysis / security

- `staticcheck` (honnef.co) — style and correctness
- `govulncheck` — dependency vulnerability scanning
- CodeQL — GitHub code scanning

---

## 5. Key Conventions

### Configuration
All configuration comes from **environment variables** via `github.com/sethvargo/go-envconfig` struct tags. No flags or config files. Nested structs use `prefix=` tags (e.g. `TARGET_INSTANCE_` prefix). Optional booleans use `*bool` with `noinit` so unset is distinguishable from false.

### Error handling
- Every error is wrapped with `fmt.Errorf("context: %w", err)` before returning.
- No broad error swallowing — errors propagate up and cause `os.Exit(N)` with a unique non-zero code per step (so the caller can identify exactly which step failed).
- Transient errors (API conflicts, not-found during wait loops) use `retry.RetryableError()`.

### Retry pattern
All GCP and K8s API calls that might transiently fail use `github.com/sethvargo/go-retry` with `retry.NewConstant(duration)` + `retry.WithMaxDuration(...)`. Both fire-and-forget (`retry.Do`) and value-returning (`retry.DoValue`) forms are used consistently.

### Logging
stdlib `log/slog` with structured key-value pairs. Format (text/JSON) and level configurable via env. Every operation logs `migrationStep` as a numeric key so `nais-cli` can render a progress bar. The logger is enriched with `migrationApp`, `migrationTarget`, `migrationPhase` in `common_main.Main`.

### K8s client pattern
A generic typed wrapper (`internal/pkg/k8s/generic_client.go`) over the dynamic Kubernetes client uses Go generics to provide typed `Get`, `Create`, `Update`, `UpdateStatus`, `Patch`, `Delete`, `DeleteCollection`, `ExistsByLabel` methods for each CRD kind. Type aliases (`AppClient`, `SqlInstanceClient`, …) are defined for each resource type.

### Naming
- Packages named after their domain (`application`, `backup`, `database`, `instance`, `migration`, `promote`, `resolved`, …).
- Functions named imperatively: `CreateInstance`, `PrepareSourceDatabase`, `StartMigrationJob`, etc.
- Test files follow `*_test.go` with external test packages (`package instance_test`).

### Labels
Resources created during migration use label `migrator.nais.io/finalize=<appName>` so finalize/rollback can bulk-delete them with `LabelSelector`.

---

## 6. Dependency Management

- Standard Go modules (`go.mod` / `go.sum`).
- `tools.go` with `//go:build tools` pattern pins tool executables (staticcheck, govulncheck, ginkgo) in the module graph.
- Two `replace` directives for invalid transitive deps pulled in by `k8s-config-connector` (redirected to non-existent local paths to suppress go mod errors).
- Dependabot opens daily PRs for both gomod and Actions, capped at 5 open PRs each, k8s packages grouped.

---

## 7. Testing

### Framework
**Ginkgo v2** (BDD-style `Describe`/`When`/`Context`/`It`) + **Gomega** matchers. Each tested package has a `*_suite_test.go` that registers the fail handler and runs the suite.

### Test locations
```
internal/pkg/config/common_test.go          # Config parsing (env var mapping, optional bool)
internal/pkg/config/config_suite_test.go    # Suite bootstrap
internal/pkg/instance/instance_test.go      # DefineInstance, StripPgAuditFlags, HasPgAuditFlags
internal/pkg/instance/instance_suite_test.go # Suite bootstrap
```

Tests are unit tests; no integration tests require a live cluster or GCP project.

### Run tests
```bash
make test
# Equivalent: go test ./... -v -count=1 -coverprofile cover.out
```

---

## 8. How to Build

```bash
# All four binaries into ./bin/
make all

# Individual binaries
make setup    # → bin/setup
make promote  # → bin/promote
make finalize # → bin/finalize
make rollback # → bin/rollback

# Docker image (also runs tests and static analysis)
docker build .
```

---

## 9. Notable Configuration Files

| File                           | Purpose                                                                              |
|--------------------------------|--------------------------------------------------------------------------------------|
| `go.mod`                       | Module, Go version (1.25), direct/indirect deps, two `replace` workarounds           |
| `Makefile`                     | Build (`all`/each binary), `test`, `check` targets                                   |
| `Dockerfile`                   | Multi-stage: builder (test+check+build) → distroless runtime                         |
| `.github/workflows/main.yml`   | CI: build, push, sign image; create GitHub release                                   |
| `.github/workflows/codeql.yml` | Weekly + PR CodeQL Go security scan                                                  |
| `.github/dependabot.yml`       | Daily gomod + Actions updates, k8s group                                             |
| `tools.go`                     | Tool dependency pinning (staticcheck, govulncheck, ginkgo)                           |
| `.env`                         | Local dev defaults (gitignored); sets LOG_LEVEL, APP_NAME, NAMESPACE, dev mode flags |
| `hack/`                        | Shell scripts for local dev: `run`, `cleanup`, `deploy_test_app`, `update_env`       |

---

## 10. Key Abstractions and Architectural Patterns

### `Manager` (central service locator / context object)
`internal/pkg/common_main.Manager` holds all client handles:
- `AppClient` — NAIS Application CRUD
- `SqlInstanceClient`, `SqlSslCertClient`, `SqlDatabaseClient`, `SqlUserClient` — CNRM resource CRUD
- `SqlAdminService` — Google SQL Admin REST API
- `DatamigrationService` — Google DMS REST API
- `DBMigrationClient` — Google Cloud DMS gRPC client
- `K8sClient` — raw `kubernetes.Interface`
- `Logger` — pre-enriched `*slog.Logger`

All internal package functions accept `*common_main.Manager` as a parameter (not a receiver). No global state.

### `resolved.Instance` and `resolved.GcpProject` (runtime-resolved values)
The resolved package contains types built at runtime by inspecting the live cluster: primary IP, outgoing IPs, credentials from secrets, region. Functions use `retry` loops to wait for resources to become ready. `GcpProject` provides helpers like `GcpParentURI()` and `GcpComponentURI()` to build GCP resource paths.

### `k8s.GenericClient[T, P]` (typed dynamic K8s client)
A Go-generics wrapper over `dynamic.Interface` so code works with concrete CRD types (no manual unstructured/structured conversion at call sites).

### Phased sequential CLI (no controller loop)
Each binary is a standalone sequential script:
1. Parse env → build `Manager` → run numbered steps → exit
Each step is an independent function call. Failures exit with a unique code. There is no reconciliation loop, no HTTP server, no long-running process.

### Retry-everywhere pattern
Essentially all GCP/K8s API calls are wrapped in `retry.Do`/`retry.DoValue` with constant-interval backoff and a maximum duration timeout. Non-retryable errors (e.g. invalid config) propagate immediately.

### Helper (dummy) Application pattern
During setup, a temporary NAIS `Application` resource (`migrator-<appname>`) is created with a dummy image. Its sole purpose is to cause naiserator/sqeletor to create the target Cloud SQL instance and associated K8s resources. The migrator watches the helper app's `Status.SynchronizationState` until `RolloutComplete`, then resolves the target instance from it. The helper app is deleted during promote/rollback/finalize.

### Progress step labelling
Every `mgr.Logger.Info(...)` call at a migration step includes `"migrationStep", N` so that `nais-cli` can parse stdout and display a progress bar. The total (`migrationStepsTotal`) is logged at phase start.

---

## Open Questions

None at this time — the codebase is self-consistent and well-commented.
