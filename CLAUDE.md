# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build / Test Commands

```bash
# Build the server binary (vendored dependencies, no GOPATH needed)
go build -trimpath -mod=vendor

# Run all tests (requires PostgreSQL accessible at TEST_DB_URL)
export TEST_DB_URL=postgresql://postgres:localdb@localhost:5432/nakama?sslmode=disable
go test -v -race ./...

# Run a single test
go test -v -race -run TestFoo ./server/

# Run all tests via Docker (full integration environment)
docker compose -f ./docker-compose-tests.yml up --build --abort-on-container-exit; docker compose -f ./docker-compose-tests.yml down -v
```

- Go 1.26+, module `github.com/heroiclabs/nakama/v3`, all deps vendored.
- Use `-mod=vendor` for builds; the vendored tree is the source of truth.
- The test entrypoint in `docker-compose-tests.yml` is: `go test -vet=off -v -race ./...`
- If you change `.proto` files, run `./buf.sh` to regenerate protobuf/gRPC/gRPC-Gateway/OpenAPI code (requires buf CLI and the protoc plugins listed in README).

## Architecture

Nakama is a realtime game server (Go monolith) backed by **CockroachDB or PostgreSQL** (wire-compatible). It exposes three protocol surfaces:

- **HTTP REST + gRPC** on port 7350 (request/response API)
- **WebSocket** on port 7350 (realtime messaging — sessions, chat, matches, parties, matchmaker)
- **Console** web UI on port 7351 (admin dashboard, embedded Vue SPA)

### Package Map

| Package | Role |
|---------|------|
| `main.go` | Entry point. Parses subcommands (`migrate`, `check`, `healthcheck`), wires all server components, starts API + console servers, handles OS signals |
| `server/` | **Core.** All business logic: API handlers (`api_*.go`), realtime pipeline (`pipeline*.go`), matchmaker, leaderboards, matches, parties, tracker, storage index, session/status registries, metrics, runtime system |
| `console/` | Protobuf definitions & generated code for the console API, plus the embedded Vue UI |
| `apigrpc/` | Protobuf definitions & generated code for the public gRPC/gRPC-Gateway API |
| `migrate/` | Database migration runner using [sql-migrate](https://github.com/heroiclabs/sql-migrate). SQL files in `migrate/sql/` are timestamp-sequenced |
| `social/` | Social login provider integration (Google OAuth) |
| `iap/` | In-app purchase validation |
| `flags/` | Command-line flag generation from Go structs (Uber-derived) |
| `se/` | Anonymous telemetry (Segment) |
| `internal/` | Vendored/internal forks: `gopher-lua` (Lua VM), `satori` (UUID), `cronexpr`, `ctxkeys`, `skiplist` |
| `build/` | Dockerfiles and multi-arch build scripts |
| `data/modules/` | Bundled Lua scripts shipped with the server (client RPC, match handler, tournament, IAP verifier, etc.) |
| `sample_go_module/` | Example Go plugin module for runtime extensions |

### Request Lifecycle

1. **HTTP REST** → gRPC-Gateway (`grpc-gateway/v2`) translates JSON to protobuf → hits `ApiServer` (which implements the generated `NakamaServer` gRPC interface) → core functions in `core_*.go` handle DB queries and business logic.

2. **WebSocket** → `socket_ws.go` upgrades HTTP → establishes a `Session` → the `Pipeline` reads `rtapi.Envelope` messages and dispatches by type to `pipeline_*.go` files (channel, match, party, matchmaker, ping, rpc, status).

3. **Runtime hooks** — Before/after hooks are registered for every API call and realtime message. The `Runtime` (`runtime.go`) dispatches to `RuntimeProvider` implementations: Lua (custom gopher-lua fork), JavaScript (goja), or Go (native plugins via `plugin` package). All three share the `nakama-common/runtime` interface.

### Key Internal Interfaces

All component contracts are defined as Go interfaces in their respective files. The main ones:

- `SessionRegistry` / `SessionCache` — user session lifecycle and token management
- `Tracker` — presence tracking (who's online, in which stream)
- `MessageRouter` — delivers realtime envelopes to sessions by presence or stream
- `MatchRegistry` — manages authoritative multiplayer match instances
- `Matchmaker` — matches players together based on criteria
- `PartyRegistry` — manages ad-hoc player parties
- `LeaderboardCache` / `LeaderboardRankCache` / `LeaderboardScheduler` — leaderboard ranking and periodic resets
- `StatusRegistry` — tracks user status (online/offline) across the cluster
- `Metrics` — Prometheus metrics via tally

### Config

Configuration is driven by YAML struct tags and command-line flags. The `Config` interface (`server/config.go`) exposes typed sub-configs (database, socket, session, runtime, matchmaker, etc.). Flags are auto-generated from the config structs in `flags/`. The `ParseArgs` function handles CLI parsing; unknown args fall through.

### Database

- Driver: `pgx/v5` via `database/sql` compatibility layer (`stdlib`).
- Migrations run via `nakama migrate up` or automatically checked on startup.
- Postgres wire-compatible: CockroachDB or Postgres 16+.
- `db_error.go` maps Postgres error codes to gRPC status codes.

### Runtime (Server-Side Custom Logic)

Three language runtimes, all implementing the `nakama-common/runtime` interfaces:

- **Lua** — Default, uses the vendored gopher-lua fork at `internal/gopher-lua/`. Entry point: `*.lua` files in the runtime path. The module is loaded and functions are registered by name convention (e.g., `before_authenticate_device`).
- **JavaScript** — Uses [goja](https://github.com/dop251/goja). Entry point: `index.js`.
- **Go** — Native plugins via Go's `plugin` package. The plugin must export a function that matches the `RuntimeGoInitializer` signature. `sample_go_module/` provides an example.
