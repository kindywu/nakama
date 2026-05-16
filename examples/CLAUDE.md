# CLAUDE.md

This directory contains standalone Go client examples demonstrating Nakama's HTTP and WebSocket APIs. Each example is a single `main.go` that authenticates via device ID, then exercises a specific Nakama feature.

## Examples

| Directory | Feature | Transport | Prerequisites |
|-----------|---------|-----------|---------------|
| `leaderboard/` | Leaderboard (submit scores, poll ranking) | HTTP REST | `data/modules/leaderboard.lua` in runtime path |
| `tournament/` | Tournament (create via RPC, join, submit scores) | HTTP REST | `data/modules/tournament.lua` in runtime path |
| `matchmaker/` | Matchmaker + match join + data exchange | WebSocket (protobuf) | None |
| `ping-pong/` | WebSocket ping/pong RTT measurement | WebSocket (protobuf) | None |

## Running

```bash
go run ./examples/leaderboard/
go run ./examples/tournament/
go run ./examples/matchmaker/
go run ./examples/ping-pong/
```

All examples assume Nakama is running on `localhost:7350` with the default server key (`defaultkey`). No manual account registration needed — device authentication creates users automatically.

## Shared patterns

### Device authentication

All examples authenticate by POSTing `{"id": "<random-device-id>"}` to `/v2/account/authenticate/device` with HTTP basic auth (`serverKey:`). The response contains a session `token` used as `Bearer` for subsequent requests.

### Int64 in JSON

Nakama's gRPC-Gateway uses proto3 JSON mapping where int64 fields are serialized as **strings** on the wire. When building request bodies, encode score/subscore values as strings (`fmt.Sprintf("%d", val)`) rather than bare numbers. When decoding responses, use the `json:",string"` struct tag.

Example:
```go
type leaderboardRecord struct {
    Score    int64 `json:"score,string"`
    Subscore int64 `json:"subscore,string"`
    Rank     int64 `json:"rank,string"`
}
```

### RPC call body encoding

The gRPC-Gateway maps the HTTP body directly to the `string payload` field of the `Rpc` protobuf message (`body: "payload"` in the proto annotation). This means the request body must be a **JSON string** — not a bare JSON object.

Correct:
```go
argsJSON, _ := json.Marshal(map[string]any{...})
payload, _ := json.Marshal(string(argsJSON))   // double-encode → JSON string
req, _ := http.NewRequest("POST", url, bytes.NewReader(payload))
```

Incorrect (will fail with "cannot unmarshal object into Go value of type string"):
```go
payload, _ := json.Marshal(map[string]any{...}) // bare JSON object
```

### Other API calls

For non-RPC endpoints (authenticate, write leaderboard/tournament records, update account), the HTTP body is the inner request message directly — no outer envelope needed. The proto annotation `body: "*"` or field-specific bindings handle this.
