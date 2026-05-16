# Nakama 客户端示例文档

> 以案例为主线端到端介绍各功能点,请阅读 [功能案例详解](case-studies.md)。

## 1. 概述

`examples/` 目录包含 5 个独立的 Go 客户端示例,演示如何通过 HTTP REST 和 WebSocket 协议调用 Nakama 的核心功能。每个示例是一个单文件 `main.go`,使用设备 ID 自动创建用户,无需手动注册。

### 1.1 示例总览

| 目录 | 功能 | 协议 | 额外依赖 | 前置条件 |
|------|------|------|---------|---------|
| `leaderboard/` | 排行榜(提交分数、轮询排名) | HTTP REST | — | `data/modules/leaderboard.lua` 在运行时路径 |
| `tournament/` | 锦标赛(通过 RPC 创建、加入、提交分数) | HTTP REST | — | `data/modules/tournament.lua` 在运行时路径 |
| `matchmaker/` | 匹配器 + 加入比赛 + 数据交换 | WebSocket (Protobuf) | gorilla/websocket, nakama-common | 无 |
| `party/` | 组队 + 组队匹配器 + 加入比赛 | WebSocket (Protobuf) | gorilla/websocket, nakama-common | 无 |
| `ping-pong/` | WebSocket 心跳 RTT 测量 | WebSocket (Protobuf) | gorilla/websocket, nakama-common | 无 |

### 1.2 架构模式

```mermaid
graph TB
    subgraph LeaderboardExample["leaderboard + tournament"]
        direction LR
        Auth1["设备认证<br/>HTTP Basic Auth"]
        API1["HTTP REST API<br/>提交分数/查询排名"]
        Display["终端显示循环<br/>ANSI 转义清屏"]

        Auth1 --> API1
        API1 --> Display
    end

    subgraph WSExamples["matchmaker + party + ping-pong"]
        direction LR
        Auth2["设备认证<br/>HTTP Basic Auth"]
        WS["WebSocket 连接<br/>Protobuf 格式"]
        ReadLoop["读协程<br/>proto.Unmarshal"]
        WriteOps["写操作<br/>proto.Marshal"]

        Auth2 --> WS
        WS --> ReadLoop
        WS --> WriteOps
    end
```

---

## 2. 共享模式

所有示例遵循一致的编程模式。

### 2.1 设备认证

每个示例通过设备 ID 认证获取会话令牌:

```go
func authenticate(deviceID string) (string, error) {
    payload, _ := json.Marshal(map[string]string{"id": deviceID})
    req, _ := http.NewRequest("POST",
        "http://localhost:7350/v2/account/authenticate/device",
        bytes.NewReader(payload))
    req.Header.Set("Content-Type", "application/json")
    req.SetBasicAuth("defaultkey", "")

    resp, err := http.DefaultClient.Do(req)
    // ... 解析 { "token": "eyJ..." }
}
```

**认证端点免 JWT 认证**,使用 Server Key (Basic Auth) 进行客户端认证。成功后返回的 `token` 用于后续请求的 `Authorization: Bearer <token>` 头。

### 2.2 int64 在 JSON 中的编码

Nakama 的 gRPC-Gateway 使用 proto3 JSON 映射: **int64 字段在线路上序列化为字符串**。

```go
// 构建请求体时: score/subscore 编码为字符串
payload, _ := json.Marshal(map[string]any{
    "score":    fmt.Sprintf("%d", score),
    "subscore": "0",
})

// 解析响应时: 使用 json:",string" 标签
type leaderboardRecord struct {
    Score    int64 `json:"score,string"`
    Subscore int64 `json:"subscore,string"`
    Rank     int64 `json:"rank,string"`
}
```

### 2.3 RPC 调用体编码

gRPC-Gateway 将 HTTP body 直接映射到 `Rpc` protobuf 消息的 `string payload` 字段(`body: "payload"`)。因此请求体必须是 **JSON 字符串**,而非裸 JSON 对象。

**正确写法 (双重编码):**
```go
argsJSON, _ := json.Marshal(map[string]any{...})  // 第一层: JSON 对象
payload, _ := json.Marshal(string(argsJSON))        // 第二层: 包装为 JSON 字符串
req, _ := http.NewRequest("POST", url, bytes.NewReader(payload))
```

**错误写法 (会失败):**
```go
payload, _ := json.Marshal(map[string]any{...})  // 裸 JSON 对象 → 解析失败
```

**接收 RPC 响应同样需要双重解码:**
```go
var rpcResp struct { Payload string `json:"payload"` }
json.NewDecoder(resp.Body).Decode(&rpcResp)           // 第一层: 提取 payload 字符串
var result MyResult
json.Unmarshal([]byte(rpcResp.Payload), &result)       // 第二层: 解析业务数据
```

### 2.4 其他 API 调用

对于非 RPC 端点(认证、写入排行榜/锦标赛记录、更新账户),HTTP body 是 inner request message 直接映射,无需外层封装。proto 注解 `body: "*"` 或字段绑定处理此映射。

### 2.5 WebSocket 消息模式

```go
// 发送: 构造 Envelope → proto.Marshal → WriteMessage(BinaryMessage)
env := &rtapi.Envelope{
    Message: &rtapi.Envelope_MatchmakerAdd{...},
}
data, _ := proto.Marshal(env)
conn.WriteMessage(websocket.BinaryMessage, data)

// 接收: ReadMessage → proto.Unmarshal → type switch
_, raw, _ := conn.ReadMessage()
var env rtapi.Envelope
proto.Unmarshal(raw, &env)
switch msg := env.Message.(type) {
case *rtapi.Envelope_MatchmakerMatched:
    // 处理匹配成功
case *rtapi.Envelope_Match:
    // 处理比赛创建
case *rtapi.Envelope_MatchData:
    // 处理比赛数据
}
```

### 2.6 生命周期管理

所有示例使用 `signal.NotifyContext` 实现优雅退出:

```go
ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
defer cancel()

// 所有 goroutine 通过 ctx.Done() 感知退出信号
select {
case <-ctx.Done():
    return
case result := <-someChannel:
    // 处理结果
}
```

---

## 3. leaderboard — 排行榜示例

### 3.1 功能说明

启动 10 个模拟客户端,每个客户端:
1. 设备认证 → 更新用户名
2. 提交初始随机分数(0-5000)
3. 每秒约 20% 概率增加分数(0-200)

独立线程每秒轮询排行榜并终端实时刷新 Top-10 排名。

### 3.2 前置条件

```bash
# 将内置模块放入运行时路径
cp data/modules/leaderboard.lua data/modules/
```

`leaderboard.lua` 在服务器启动时通过 `nk.run_once("init")` 自动创建名为 `"global"` 的排行榜。

### 3.3 运行

```bash
go run ./examples/leaderboard/
```

### 3.4 数据流

```mermaid
sequenceDiagram
    actor Client as 客户端 (×10)
    participant Auth as POST /v2/account/authenticate/device
    participant Update as PUT /v2/account
    participant Write as POST /v2/leaderboard/{id}
    participant Poll as GET /v2/leaderboard/{id}?limit=10

    Client->>Auth: {id: "<device_id>"}
    Auth-->>Client: {token}

    Client->>Update: {username: "p0-xxxx"}
    Update-->>Client: 200 OK

    loop 每秒 (20%概率)
        Client->>Write: {score: "1234", subscore: "0"}
        Write-->>Client: {rank: 3, ...}
    end

    par 独立显示线程
        loop 每秒
            Poll-->>Display: Top-10 排名 → 终端清屏刷新
        end
    end
```

### 3.5 关键代码路径

| 函数 | 说明 |
|------|------|
| `authenticate()` | 设备认证,返回 session token |
| `updateAccount()` | 设置用户名(PUT /v2/account) |
| `submitScore()` | 提交排行榜分数(POST /v2/leaderboard/{id}),score 编码为字符串 |
| `fetchLeaderboard()` | 获取 Top-10 排名(GET /v2/leaderboard/{id}?limit=10) |
| `printLeaderboard()` | 使用 ANSI 转义序列实时刷新终端(无闪烁) |

---

## 4. tournament — 锦标赛示例

### 4.1 功能说明

1. 一个"创建者"客户端通过 RPC 调用 `clientrpc.create_tournament` 创建 5 分钟锦标赛
2. 10 个玩家客户端认证 → 更新用户名 → 加入锦标赛 → 提交初始分数 → 持续随机加分
3. 显示线程每秒轮询排行榜,显示已用时间和最终排名

### 4.2 前置条件

```bash
cp data/modules/tournament.lua data/modules/
```

`tournament.lua` 注册了 `clientrpc.create_tournament` RPC 函数。

### 4.3 运行

```bash
go run ./examples/tournament/
```

### 4.4 数据流

```mermaid
sequenceDiagram
    actor Creator as 创建者
    actor Player as 玩家 (×10)
    participant RPC as POST /v2/rpc/clientrpc.create_tournament
    participant Join as POST /v2/tournament/{id}/join
    participant Write as POST /v2/tournament/{id}
    participant Poll as GET /v2/tournament/{id}?limit=10

    Creator->>RPC: RPC 双重编码 → create_tournament(args)
    RPC-->>Creator: {payload: '{"tournament_id":"..."'}

    par 所有玩家并发
        Player->>Player: 认证 + 设置用户名
        Player->>Join: join_required 锦标赛
        Join-->>Player: 200 OK
        Player->>Write: {score: "1234", subscore: "0"}
        Write-->>Player: {rank: 5, ...}
    end

    loop 每秒
        Poll-->>Display: Top-10 → 终端刷新
    end

    Note over Creator,Display: 5 分钟后锦标赛自动结束
```

### 4.5 关键代码路径

| 函数 | 说明 |
|------|------|
| `createTournament()` | 通过 RPC 双重编码创建锦标赛,双重解码提取 `tournament_id` |
| `joinTournament()` | POST /v2/tournament/{id}/join,发送空 JSON `{}` |
| `submitTournamentScore()` | POST /v2/tournament/{id},score 编码为字符串 |
| `fetchTournamentRecords()` | GET /v2/tournament/{id}?limit=10 |
| `printLeaderboard()` | 显示排名 + 已用时间 + 锦标赛结束提示 |

### 4.6 RPC 双重编解码详解

这是示例中**最复杂的序列化模式**:

```
客户端 Go 代码:
  argsJSON  = json.Marshal({authoritative, sort_order, duration, ...})  → {"duration":300,...}
  payload   = json.Marshal(string(argsJSON))                            → "\"{\\\"duration\\\":300,...}\""

HTTP 线上:
  Body: "\"{\\\"duration\\\":300,...}\""  (一个 JSON 字符串)

Nakama 解码:
  gRPC-Gateway: body → Rpc.payload (string) → "{\"duration\":300,...}"
  Runtime: json.decode(payload) → {duration=300, ...}  (Lua table)

Nakama 响应编码:
  Runtime: nk.json_encode({tournament_id="..."}) → "{\"tournament_id\":\"...\"}"
  gRPC-Gateway: {payload: "{\"tournament_id\":\"...\"}", id: "..."}

客户端解码:
  rpcResp  = json.Decode(body) → {Payload: "{\"tournament_id\":\"...\"}", ...}
  result   = json.Unmarshal(rpcResp.Payload) → {TournamentID: "..."}
```

---

## 5. matchmaker — 匹配器示例

### 5.1 功能说明

10 个玩家通过 WebSocket 连接到 Nakama,随机延迟后加入匹配器(3人一组),匹配成功后加入比赛,互相发送 hello 消息并监听比赛数据和 Presence 事件。

### 5.2 运行

```bash
go run ./examples/matchmaker/
```

### 5.3 数据流

```mermaid
sequenceDiagram
    actor P as 玩家 (×10)
    participant API as HTTP API
    participant WS as WebSocket
    participant MM as Matchmaker
    participant Match as Match

    P->>API: 设备认证 → token
    P->>WS: GET /ws?format=protobuf&token=...

    Note over P: 随机延迟 0-10s

    P->>WS: Envelope{MatchmakerAdd{min=3,max=3}}
    WS-->>P: Envelope{MatchmakerTicket{ticket}}

    Note over MM: 凑齐 3 人后匹配

    WS-->>P: Envelope{MatchmakerMatched{match_id/token,users}}

    P->>WS: Envelope{MatchJoin{match_id}}
    WS-->>P: Envelope{Match{match_id,presences,self}}

    P->>WS: Envelope{MatchDataSend{data}}
    WS-->>P: Envelope{MatchData{data,presence}}

    loop 持续监听
        WS-->>P: Envelope{MatchData} / Envelope{MatchPresenceEvent}
    end
```

### 5.4 关键设计

- **读协程分离:** `readLoop()` goroutine 持续读取 WebSocket 消息,通过 channel 将事件分发给主逻辑(`playerEvents` 结构体)
- **事件频道化:**
  ```go
  type playerEvents struct {
      ticket   chan string              // 容量 1
      matched  chan *rtapi.MatchmakerMatched  // 容量 1
      match    chan *rtapi.Match              // 容量 1
      data     chan *rtapi.MatchData          // 容量 5
      presence chan *rtapi.MatchPresenceEvent // 容量 5
  }
  ```
- **顺序等待:** 主逻辑通过 `select` 语句等待事件: `ticket → matched → match → data/presence`

---

## 6. party — 组队示例

### 6.1 功能说明

10 个玩家通过 WebSocket 连接,由 coordinator 分配为 3 人一组的 party。Leader 创建 party 后成员加入,满员后 Leader 发起 Party Matchmaker。匹配成功后全员加入比赛并交换消息。剩余玩家(不能凑成 3 人)通过 solo matchmaker 加入。

### 6.2 运行

```bash
go run ./examples/party/
```

### 6.3 数据流

```mermaid
sequenceDiagram
    actor L as Leader
    actor M as Member
    participant Nakama

    Note over L,M: Coordinator 分配角色

    L->>Nakama: Envelope{PartyCreate{open=true,maxSize=3}}
    Nakama-->>L: Envelope{Party{party_id,presences}}

    L->>M: (应用层) 分享 party_id

    M->>Nakama: Envelope{PartyJoin{party_id}}
    Nakama-->>M: Envelope{Party{party_id,presences}}

    Note over L: 等待所有成员加入

    L->>Nakama: Envelope{PartyMatchmakerAdd{party_id,min=3,max=3}}
    Nakama-->>L: Envelope{PartyMatchmakerTicket{ticket}}
    Nakama-->>M: Envelope{PartyMatchmakerTicket{ticket}}

    Nakama-->>L: Envelope{MatchmakerMatched{match_id,users}}
    Nakama-->>M: Envelope{MatchmakerMatched{match_id,users}}

    par Leader + Members
        L->>Nakama: Envelope{MatchJoin{match_id}}
        M->>Nakama: Envelope{MatchJoin{match_id}}
        Nakama-->>L: Envelope{Match{match_id,presences}}
        Nakama-->>M: Envelope{Match{match_id,presences}}
    end

    L->>Nakama: Envelope{MatchDataSend{data}}
    M->>Nakama: Envelope{MatchDataSend{data}}
```

### 6.4 Coordinator 设计

```go
type coordinator struct {
    mu    sync.Mutex
    seq   int                    // 全局序列号
    slots []*partySlot           // 每队一个 slot
}

type partySlot struct {
    mu      sync.Mutex
    partyID string              // Leader 创建后填充
    ready   chan struct{}       // Leader 关闭此通道通知成员
    joined  chan struct{}       // 成员加入后向此通道发送信号
}

// 分配算法
func (c *coordinator) assign() (slot, role, partyNum) {
    n := c.seq; c.seq++
    pn := n / partySize          // 队伍编号
    pos := n % partySize         // 队伍内位置 (0=Leader)

    if n >= numPlayers - numPlayers%partySize {
        return nil, roleSolo, 0  // 剩余玩家走 solo
    }

    if pos == 0 {
        return c.slots[pn], roleCreate, pn  // Leader
    }
    return c.slots[pn], roleJoin, pn        // Member
}
```

**同步机制:**
- `ready` channel: Leader 创建 party 后 `close(slot.ready)`,所有等待的 Member 被唤醒
- `joined` channel: 每个成员加入后向 `slot.joined` 发送信号,Leader 计数确认满员

---

## 7. ping-pong — 心跳示例

### 7.1 功能说明

演示 Nakama 的双层心跳系统:

- **Layer 1 (WebSocket 协议层):** 服务端自动发送 WS Ping Frame,客户端 gorilla 库自动回复 Pong。用于检测死连接。
- **Layer 2 (应用层):** 客户端每 5 秒发送 `Envelope_Ping`(带 `cid` 关联 ID),服务端立即回复 `Envelope_Pong`(回传相同 `cid`)。用于应用层 RTT 测量。

### 7.2 运行

```bash
go run ./examples/ping-pong/
```

### 7.3 数据流

```mermaid
sequenceDiagram
    actor Client as 客户端
    participant WS as gorilla/websocket
    participant Server as Nakama Server

    Client->>WS: connectWS → GET /ws?format=protobuf

    Note over Client,Server: Layer 1: WebSocket 协议层心跳(自动)
    loop 每 15s
        Server-->>WS: WS Ping Frame
        WS-->>Server: WS Pong Frame (自动响应)
    end

    Note over Client,Server: Layer 2: 应用层心跳(显式)
    loop 每 5s
        Client->>WS: WriteMessage(BinaryMessage, Envelope_Ping{cid})
        WS->>Server: Binary WebSocket Frame
        Server-->>Client: Envelope_Pong{cid}
        Client->>Client: RTT = time.Since(start)
    end
```

### 7.4 关键设计

- **关联 ID:** 通过 `Envelope.cid` 字段匹配请求-响应对
- **超时处理:** 10 秒未收到 Pong 则记录超时日志
- **并发安全:** `pending map[string]chan time.Duration` 由 `sync.Mutex` 保护

---

## 8. 运行环境

### 8.1 启动 Nakama

```bash
# 使用 Docker Compose (推荐)
docker compose up

# 或手动启动
nakama --database.address "postgresql://postgres:localdb@localhost:5432/nakama?sslmode=disable"
```

### 8.2 安装依赖

```bash
# WebSocket 示例需要额外依赖
go get github.com/gorilla/websocket
go get github.com/heroiclabs/nakama-common/rtapi
go get google.golang.org/protobuf/proto
```

### 8.3 运行所有示例

```bash
go run ./examples/leaderboard/
go run ./examples/tournament/
go run ./examples/matchmaker/
go run ./examples/party/
go run ./examples/ping-pong/
```

---

## 9. 示例与文档的对应关系

| 文档 | 相关示例 |
|------|---------|
| [API 设计文档](api.md) | leaderboard, tournament — HTTP REST API 调用模式、认证、RPC 双重编码 |
| [实时通信设计文档](realtime.md) | matchmaker, party, ping-pong — WebSocket 连接、Envelope 协议、Pipeline 消息流 |
| [排行榜与锦标赛设计文档](leaderboard.md) | leaderboard, tournament — 排行榜写入/查询、锦标赛 join/生命周期 |
| [安全设计文档](security.md) | 所有示例 — 设备认证、Server Key Basic Auth、Bearer Token |
| [调用链-后端](call-chains-backend.md) | 所有示例 — 完整请求链路追踪对照 |
