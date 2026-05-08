# Nakama 架构设计文档

## 1. 系统总览

Nakama 是一个开源、分布式的实时游戏服务器,使用 Go 语言编写,后端数据库支持 **CockroachDB** 或 **PostgreSQL**(wire-compatible)。

### 1.1 部署架构

```mermaid
graph TB
    subgraph Clients["客户端层"]
        GameClient["Game Client<br/>(Unity/Unreal/Godot/...)"]
        WebBrowser["Web Browser<br/>(Console UI)"]
    end

    subgraph Protocols["协议层"]
        HTTP["HTTP/1.1 REST<br/>JSON"]
        gRPC["gRPC<br/>Protobuf"]
        WS["WebSocket<br/>JSON/Protobuf"]
    end

    subgraph Nakama["Nakama Server :7350/:7351"]
        API["API Server<br/>(HTTP + gRPC)"]
        Console["Console Server<br/>(Admin UI)"]
        Pipeline["Pipeline<br/>(Realtime Messages)"]
    end

    subgraph Storage["存储层"]
        DB[("PostgreSQL /<br/>CockroachDB")]
    end

    GameClient -->|"认证/存储/排行榜<br/>请求-响应"| HTTP
    GameClient -->|"聊天/匹配/状态<br/>实时推送"| WS
    GameClient -->|"请求-响应"| gRPC
    WebBrowser -->|"管理后台"| Console
    HTTP --> API
    gRPC --> API
    WS --> Pipeline
    API --> DB
    Pipeline --> DB
    Console --> DB
```

### 1.2 协议端口

| 端口 | 协议 | 用途 |
|------|------|------|
| 7350 | HTTP/1.1, gRPC, WebSocket | 面向客户端的业务 API |
| 7351 | HTTP/1.1 | 嵌入式 Console 管理后台 (Vue SPA) |

---

## 2. 核心组件架构

```mermaid
graph TB
    subgraph EntryPoint["入口 main.go"]
        Main["main()<br/>组件装配 + 生命周期"]
    end

    subgraph CoreComponents["核心组件"]
        direction TB
        SessionReg["SessionRegistry<br/>会话注册表"]
        SessionCache["SessionCache<br/>令牌缓存"]
        StatusReg["StatusRegistry<br/>状态注册表"]
        Tracker["Tracker<br/>在线状态跟踪"]
        MsgRouter["MessageRouter<br/>消息路由"]
        MatchReg["MatchRegistry<br/>比赛注册表"]
        Matchmaker["Matchmaker<br/>匹配器"]
        PartyReg["PartyRegistry<br/>组队注册表"]
        StreamMgr["StreamManager<br/>流管理"]
        LBCache["LeaderboardCache<br/>排行榜缓存"]
        LBRankCache["LeaderboardRankCache<br/>排行榜排名缓存"]
        LBScheduler["LeaderboardScheduler<br/>排行榜定时重置"]
        StorageIdx["StorageIndex<br/>存储索引(Bluge)"]
        Metrics["Metrics<br/>指标(Prometheus)"]
        LoginAttemptCache["LoginAttemptCache<br/>登录尝试缓存"]
        FmCallback["FmCallbackHandler<br/>Fleet Manager 回调"]
    end

    subgraph RuntimeLayer["Runtime 运行时"]
        Runtime["Runtime<br/>钩子调度器"]
        LuaRT["Lua Runtime<br/>(gopher-lua fork)"]
        JsRT["JavaScript Runtime<br/>(goja)"]
        GoRT["Go Runtime<br/>(plugin)"]
    end

    subgraph APILayer["API 层"]
        ApiServer["ApiServer<br/>gRPC + gRPC-Gateway"]
        Pipeline["Pipeline<br/>WebSocket 消息处理"]
    end

    subgraph ConsoleLayer["Console 层"]
        ConsoleServer["ConsoleServer<br/>管理后台 API"]
    end

    Main --> SessionReg
    Main --> SessionCache
    Main --> StatusReg
    Main --> Tracker
    Main --> MsgRouter
    Main --> MatchReg
    Main --> Matchmaker
    Main --> PartyReg
    Main --> StreamMgr
    Main --> LBCache
    Main --> LBRankCache
    Main --> LBScheduler
    Main --> StorageIdx
    Main --> Metrics
    Main --> LoginAttemptCache
    Main --> FmCallback
    Main --> Runtime
    Main --> ApiServer
    Main --> Pipeline
    Main --> ConsoleServer

    Runtime --> LuaRT
    Runtime --> JsRT
    Runtime --> GoRT

    ApiServer --> SessionReg
    ApiServer --> Tracker
    ApiServer --> MsgRouter
    ApiServer --> MatchReg
    ApiServer --> LBCache
    ApiServer --> LBRankCache
    ApiServer --> Runtime
    ApiServer --> Metrics

    Pipeline --> SessionReg
    Pipeline --> Tracker
    Pipeline --> MsgRouter
    Pipeline --> MatchReg
    Pipeline --> Matchmaker
    Pipeline --> PartyReg
    Pipeline --> Runtime
    Pipeline --> Metrics

    ConsoleServer --> Tracker
    ConsoleServer --> MsgRouter
    ConsoleServer --> MatchReg
    ConsoleServer --> Runtime
    ConsoleServer --> Metrics
```

---

## 3. 请求处理流程

### 3.1 HTTP/gRPC API 请求生命周期

```mermaid
sequenceDiagram
    actor Client as 客户端
    participant GW as gRPC-Gateway<br/>(HTTP→Protobuf)
    participant Interceptor as gRPC Interceptor<br/>(认证 + 追踪ID)
    participant API as ApiServer Handler<br/>(api_*.go)
    participant RT as Runtime<br/>(Before/After Hooks)
    participant Core as Core 函数<br/>(core_*.go)
    participant DB as PostgreSQL

    Client->>GW: HTTP JSON 请求
    GW->>GW: 转换为 Protobuf
    GW->>Interceptor: gRPC 请求
    Interceptor->>Interceptor: JWT 认证<br/>注入 TraceID
    Interceptor->>API: 转发请求

    API->>RT: Before Hook<br/>(如 before_authenticate_device)
    alt Hook 返回错误
        RT-->>API: 错误/拒绝
        API-->>Client: 错误响应
    else Hook 通过
        RT-->>API: (可能修改后的请求)
        API->>Core: 执行业务逻辑
        Core->>DB: SQL 查询
        DB-->>Core: 结果
        Core-->>API: 结果
        API->>RT: After Hook<br/>(如 after_authenticate_device)
        RT-->>API: (可能修改后的响应)
        API-->>Client: 响应
    end
```

### 3.2 WebSocket 实时消息流程

```mermaid
sequenceDiagram
    actor Client as 客户端
    participant WS as WebSocket Acceptor<br/>(socket_ws.go)
    participant Session as Session<br/>(session_ws.go)
    participant Pipeline as Pipeline<br/>(pipeline.go)
    participant RT as Runtime<br/>(Before/After Hooks)
    participant Handler as Pipeline Handler<br/>(pipeline_*.go)
    participant Router as MessageRouter<br/>(message_router.go)
    participant Target as 目标 Session(s)

    Client->>WS: HTTP Upgrade /ws
    WS->>WS: JWT 认证
    WS->>Session: 创建 Session
    Session->>Tracker: Track(session)

    loop 消息循环
        Client->>Session: WebSocket Frame
        Session->>Session: 反序列化 rtapi.Envelope
        Session->>Pipeline: ProcessRequest(session, envelope)

        Pipeline->>Pipeline: 按类型分发<br/>(channel/match/party/...)
        Pipeline->>RT: BeforeRt Hook

        alt Hook 拒绝
            RT-->>Pipeline: nil/error
            Pipeline-->>Session: Error Envelope
        else Hook 通过
            Pipeline->>Handler: 业务处理
            Handler->>Router: SendToStream/SendToPresenceIDs
            Router->>Target: 投递消息
            Handler-->>Pipeline: (success, out)
            Pipeline->>RT: AfterRt Hook
            Pipeline-->>Session: 响应 Envelope
        end
    end

    Client->>Session: 断开连接
    Session->>Tracker: Untrack(session)
```

---

## 4. Pipeline 消息分发

Pipeline 是 WebSocket 实时消息的核心路由器,根据 `rtapi.Envelope` 的消息类型分发到对应的处理函数:

```mermaid
graph LR
    subgraph EnvelopeTypes["rtapi.Envelope 消息类型"]
        Channel["Channel<br/>Join/Leave/Send/Update/Remove"]
        Match["Match<br/>Create/Join/Leave/DataSend"]
        Matchmaker["Matchmaker<br/>Add/Remove"]
        Party["Party<br/>Create/Join/Leave/Promote/..."]
        Status["Status<br/>Follow/Unfollow/Update"]
        RPC["RPC<br/>(自定义远程调用)"]
        PingPong["Ping/Pong<br/>(心跳)"]
    end

    subgraph Handlers["处理函数 (pipeline_*.go)"]
        PChannel["pipeline_channel.go"]
        PMatch["pipeline_match.go"]
        PMatchmaker["pipeline_matchmaker.go"]
        PParty["pipeline_party.go"]
        PStatus["pipeline_status.go"]
        PRPC["pipeline_rpc.go"]
        PPing["pipeline_ping.go"]
    end

    Channel --> PChannel
    Match --> PMatch
    Matchmaker --> PMatchmaker
    Party --> PParty
    Status --> PStatus
    RPC --> PRPC
    PingPong --> PPing
```

---

## 5. Runtime 运行时系统

### 5.1 三语言运行时架构

```mermaid
graph TB
    subgraph RuntimeCore["Runtime (runtime.go)"]
        Hooks["钩子注册表<br/>map[string]func"]
        RPCFuncs["RPC 函数注册表"]
        EventFuncs["事件函数注册表"]
    end

    subgraph Providers["RuntimeProvider 实现"]
        Lua["Lua Runtime<br/>(runtime_lua.go)<br/>VM: internal/gopher-lua"]
        JS["JavaScript Runtime<br/>(runtime_javascript.go)<br/>VM: goja (ES5.1)"]
        Go["Go Runtime<br/>(runtime_go.go)<br/>Native Plugin (.so)"]
    end

    subgraph HooksTypes["钩子类型"]
        BeforeAPI["Before API Hooks<br/>(60+ 对 API 的拦截)"]
        AfterAPI["After API Hooks<br/>(60+ 对 API 的响应处理)"]
        BeforeRT["Before Realtime Hooks<br/>(18 种消息类型的拦截)"]
        AfterRT["After Realtime Hooks<br/>(18 种消息类型的响应处理)"]
        Lifecycle["生命周期 Hooks<br/>(Match/Matchmaker/Tournament/Leaderboard)"]
        Events["事件 Hooks<br/>(SessionStart/SessionEnd/Custom)"]
    end

    RuntimeCore --> Lua
    RuntimeCore --> JS
    RuntimeCore --> Go

    Lua --> BeforeAPI
    Lua --> AfterAPI
    Lua --> BeforeRT
    Lua --> AfterRT
    Lua --> Lifecycle
    Lua --> Events

    JS --> BeforeAPI
    JS --> AfterAPI
    JS --> BeforeRT
    JS --> AfterRT
    JS --> Lifecycle
    JS --> Events

    Go --> BeforeAPI
    Go --> AfterAPI
    Go --> BeforeRT
    Go --> AfterRT
    Go --> Lifecycle
    Go --> Events
```

### 5.2 Hook 命名约定

Hook 函数按命名约定自动注册:

| 模式 | 示例 | 说明 |
|------|------|------|
| `before_<method>` | `before_authenticate_device` | API 方法前置拦截 |
| `after_<method>` | `after_write_leaderboard_record` | API 方法后置处理 |
| `before_rt_<type>` | `before_rt_channel_join` | 实时消息前置拦截 |
| `after_rt_<type>` | `after_rt_match_create` | 实时消息后置处理 |
| `rpc_<name>` | `rpc_calculate_damage` | 自定义 RPC 函数 |
| `match_<name>` | `match_deathmatch` | 自定义权威比赛 |

### 5.3 RuntimeExecutionMode

```mermaid
stateDiagram-v2
    [*] --> RunOnce: 启动时执行一次
    [*] --> Event: 事件监听
    [*] --> RPC: RPC 调用

    state API_Request {
        Before --> CoreLogic: 修改/拒绝请求
        CoreLogic --> After: 修改响应
    }

    state Match_Lifecycle {
        MatchCreate --> MatchInit
        MatchInit --> MatchLoop
        MatchLoop --> MatchJoinAttempt
        MatchLoop --> MatchJoin
        MatchLoop --> MatchLeave
        MatchLoop --> MatchSignal
        MatchLoop --> MatchTerminate
    }

    state Periodic {
        TournamentEnd
        TournamentReset
        LeaderboardReset
    }

    state Notifications {
        PurchaseNotificationApple
        SubscriptionNotificationApple
        PurchaseNotificationGoogle
        SubscriptionNotificationGoogle
    }

    RunOnce --> Shutdown
    Shutdown --> [*]
```

---

## 6. 实时功能核心系统

### 6.1 Presence & Stream 跟踪系统

```mermaid
graph TB
    subgraph TrackerSystem["Tracker 跟踪系统"]
        Tracker["Tracker<br/>维护在线状态"]
        Presences["Presence Map<br/>Session → Streams"]
        Streams["Stream Map<br/>Stream → Presences"]
    end

    subgraph StreamTypes["Stream 类型"]
        Notifications["Notifications<br/>通知流"]
        Status["Status<br/>状态流"]
        Channel["Channel<br/>聊天频道"]
        Group["Group<br/>群组"]
        DM["DM<br/>私聊"]
        MatchRelayed["MatchRelayed<br/>中继比赛"]
        MatchAuthoritative["MatchAuthoritative<br/>权威比赛"]
        Party["Party<br/>组队"]
    end

    subgraph Operations["操作"]
        Track["Track<br/>用户上线"]
        Untrack["Untrack<br/>用户下线"]
        StreamJoin["Join<br/>加入流"]
        StreamLeave["Leave<br/>离开流"]
        SendToStream["Send<br/>推送到流"]
        SendToPresence["Send<br/>推送到指定用户"]
    end

    Tracker --> Presences
    Tracker --> Streams
    StreamTypes --> Operations

    SessionRegistry["SessionRegistry"] --> Tracker
    Tracker --> MessageRouter["MessageRouter<br/>消息投递"]
```

### 6.2 Match 比赛系统

```mermaid
sequenceDiagram
    actor Client1 as 客户端1
    actor Client2 as 客户端2
    participant Pipeline as Pipeline
    participant Registry as MatchRegistry
    participant Match as Match Handler<br/>(Runtime/Lua/JS/Go)
    participant Tracker as Tracker
    participant Router as MessageRouter

    Note over Match: 权威比赛 (Authoritative Match)<br/>由服务端 Runtime 控制逻辑

    Client1->>Pipeline: MatchCreate
    Pipeline->>Registry: CreateMatch(handler, params)
    Registry->>Match: MatchInit(presences, params)
    Match-->>Registry: (state, tickRate)
    Registry-->>Pipeline: Match 创建成功
    Pipeline-->>Client1: Match 信息

    Client1->>Pipeline: MatchJoin(matchId)
    Pipeline->>Registry: Join(matchId, session)
    Registry->>Match: MatchJoinAttempt(tick, state, ...)
    Match-->>Registry: (allow/reject)
    Registry->>Tracker: Track match stream
    Registry->>Match: MatchJoin(tick, state, joins)
    Registry->>Router: 广播 Match Presence

    Client2->>Pipeline: MatchDataSend(data)
    Pipeline->>Match: inputCh <- data
    Match->>Match: MatchLoop 处理
    Match->>Registry: 广播结果
    Registry->>Router: SendToStream(matchStream, data)

    Client1->>Pipeline: MatchLeave
    Pipeline->>Registry: Leave(matchId, session)
    Registry->>Match: MatchLeave(tick, state, leaves)
    Registry->>Tracker: Untrack match stream
```

### 6.3 Matchmaker 匹配系统

```mermaid
sequenceDiagram
    actor P1 as 玩家1
    actor P2 as 玩家2
    actor P3 as 玩家3
    participant Pipeline as Pipeline
    participant MM as Matchmaker
    participant RT as Runtime
    participant Registry as MatchRegistry
    participant Router as MessageRouter

    P1->>Pipeline: MatchmakerAdd(properties)
    Pipeline->>MM: Add(entry)
    MM->>MM: 加入匹配队列

    P2->>Pipeline: MatchmakerAdd(properties)
    Pipeline->>MM: Add(entry)

    P3->>Pipeline: MatchmakerAdd(properties)
    Pipeline->>MM: Add(entry)

    Note over MM: 定时 Tick 触发匹配

    MM->>MM: process()
    MM->>RT: MatchmakerProcessor<br/>(entries) → matches
    RT-->>MM: [[P1,P2], [P3]]

    loop 每个匹配组
        MM->>RT: MatchmakerMatched(entries)
        RT-->>MM: (matchId, token)
        MM->>Router: 通知匹配成功的玩家<br/>(MatchmakerMatched Envelope)
        MM->>Router: 通知未匹配的玩家<br/>(超时)
    end

    Note over P1,P2: 玩家收到匹配成功消息后<br/>使用 token 加入比赛

    P1->>Pipeline: MatchJoin(matchId, token)
    P2->>Pipeline: MatchJoin(matchId, token)
```

### 6.4 Party 组队系统

```mermaid
stateDiagram-v2
    [*] --> Idle

    Idle --> Leader: PartyCreate(open)
    Leader --> WaitingMembers: 邀请/等待加入

    WaitingMembers --> Full: 成员加入
    WaitingMembers --> Leader: 成员离开

    Full --> Matchmaking: PartyMatchmakerAdd
    Matchmaking --> InMatch: 匹配成功
    InMatch --> Full: 比赛结束

    Leader --> Idle: PartyClose
    Full --> Idle: PartyClose

    note right of Leader: Leader 可以:<br/>- PartyPromote(转让)<br/>- PartyRemove(踢人)<br/>- PartyAccept(审批加入)<br/>- PartyUpdate(更新设置)
```

---

## 7. 数据持久化

### 7.1 数据库迁移

```mermaid
graph LR
    subgraph Migrations["migrate/sql/"]
        M1["20180103142001<br/>initial_schema"]
        M2["20180805174141<br/>tournaments"]
        M3["20200116134800<br/>facebook_instant_games"]
        M4["20200615102232<br/>apple"]
        M5["20201005180855<br/>console"]
        M6["20210416090601<br/>purchase"]
        M7["20220426122825<br/>subscription"]
        M8["20221027145900<br/>subscription_raw"]
        M9["20230222172540<br/>purchases_not_null"]
        M10["20230706095800<br/>leaderboard_index"]
        M11["20240715155039<br/>leaderboard_rank"]
        M12["20240725115145<br/>purchase_index"]
        M13["20240801100859<br/>console_mfa"]
        M14["20250113112512<br/>user_edge_metadata"]
        M15["20250703094700<br/>console_settings"]
        M16["20250926112031<br/>fine_grained_acl"]
        M17["20251015150737<br/>user_notes_audit"]
        M18["20260319134532<br/>display_name_index"]

        M1 --> M2 --> M3 --> M4 --> M5 --> M6 --> M7 --> M8 --> M9
        M9 --> M10 --> M11 --> M12 --> M13 --> M14 --> M15 --> M16 --> M17 --> M18
    end
```

迁移使用 [sql-migrate](https://github.com/heroiclabs/sql-migrate) 库。启动时执行 `migrate up`,运行时会 `migrate check` 验证状态。

### 7.2 核心数据表

```mermaid
erDiagram
    users ||--o{ storage : "owns"
    users ||--o{ user_edge : "has"
    users ||--o{ user_device : "has"
    users ||--o{ leaderboard_record : "has"
    users ||--o{ purchase : "makes"
    users ||--o{ subscription : "subscribes"
    users ||--o{ wallet_ledger : "has"
    users ||--o{ notification : "receives"
    users ||--o{ message : "sends"
    users }o--o{ users : "friends"
    users }o--o{ user_group : "membership"
    user_group }o--o{ groups : "belongs_to"

    users {
        uuid id PK
        string username
        string display_name
        string email
        string edge_ids
        jsonb metadata
        timestamptz create_time
        timestamptz update_time
    }

    storage {
        string collection PK
        string key PK
        uuid user_id PK
        jsonb value
        text version
    }

    leaderboard_record {
        uuid leaderboard_id PK
        uuid owner_id PK
        bigint score
        int num_score
        timestamptz expiry_time
    }

    groups {
        uuid id PK
        uuid creator_id
        string name
        string description
        string lang_tag
        jsonb metadata
        int edge_count
    }
```

---

## 8. 排行榜系统

```mermaid
graph TB
    subgraph LeaderboardSystem["Leaderboard 系统"]
        LBCache["LeaderboardCache<br/>排行榜元数据缓存"]
        LBRankCache["LeaderboardRankCache<br/>排名缓存"]
        LBScheduler["LeaderboardScheduler<br/>定时重置调度"]
    end

    subgraph Operations["操作"]
        Write["WriteLeaderboardRecord<br/>写入分数"]
        Read["ListLeaderboardRecords<br/>查询排名"]
        Delete["DeleteLeaderboardRecord<br/>删除记录"]
        Reset["定时重置<br/>(cron 表达式)"]
    end

    subgraph Runtime["Runtime 扩展点"]
        BeforeWrite["before_write_leaderboard_record"]
        AfterWrite["after_write_leaderboard_record"]
        BeforeRead["before_list_leaderboard_records"]
        AfterRead["after_list_leaderboard_records"]
        ResetHook["leaderboard_reset"]
    end

    LBCache --> LBScheduler
    LBScheduler --> Reset
    LBScheduler --> Runtime

    Write --> LBRankCache
    Read --> LBRankCache

    Write --> BeforeWrite
    Write --> AfterWrite
    Read --> BeforeRead
    Read --> AfterRead
    Reset --> ResetHook
```

排行榜支持:
- **排序方式**: 升序 / 降序
- **操作符**: Best(取最佳) / Set(设值) / Increment(增量) / Decrement(减量)
- **定时重置**: 基于 Cron 表达式的周期性重置
- **权威模式**: 可配置为权威排行榜,由服务端完全控制

---

## 9. 存储索引

```mermaid
graph TB
    subgraph StorageIndexSystem["StorageIndex 存储索引系统"]
        SI["StorageIndex<br/>(基于 Bluge 全文搜索)"]
        Filters["自定义过滤器<br/>(Runtime StorageIndexFilter)"]
    end

    Write["WriteStorageObjects"] --> SI
    SI --> Filters
    Filters -->|"return false"| Reject["拒绝写入"]
    Filters -->|"return true"| Index["写入索引"]

    Read["ReadStorageObjects<br/>ListStorageObjects<br/>(搜索查询)"] --> SI
    SI --> Results["Bluge 搜索结果<br/>(排序/分页)"]
```

---

## 10. 配置系统

```mermaid
graph TB
    subgraph ConfigSource["配置来源"]
        YAML["YAML 配置文件"]
        CLI["命令行参数"]
        Env["环境变量"]
    end

    subgraph ConfigStruct["Config 结构体 (config.go)"]
        direction TB
        Root["Config Interface"]
        Name["Name"]
        DataDir["DataDir"]
        Logger["LoggerConfig"]
        Metrics["MetricsConfig"]
        Session["SessionConfig"]
        Socket["SocketConfig"]
        Database["DatabaseConfig"]
        Social["SocialConfig"]
        RuntimeCfg["RuntimeConfig"]
        Match["MatchConfig"]
        TrackerCfg["TrackerConfig"]
        Console["ConsoleConfig"]
        Leaderboard["LeaderboardConfig"]
        Matchmaker["MatchmakerConfig"]
        IAP["IAPConfig"]
        Satori["SatoriConfig"]
        Storage["StorageConfig"]
        MFA["MFAConfig"]
        Party["PartyConfig"]
    end

    subgraph FlagGen["flags 包"]
        FlagMaker["FlagMaker<br/>自动从 struct 生成 flag"]
    end

    YAML --> ConfigStruct
    CLI --> FlagGen
    FlagGen --> ConfigStruct

    Root --> Name
    Root --> DataDir
    Root --> Logger
    Root --> Metrics
    Root --> Session
    Root --> Socket
    Root --> Database
    Root --> Social
    Root --> RuntimeCfg
    Root --> Match
    Root --> TrackerCfg
    Root --> Console
    Root --> Leaderboard
    Root --> Matchmaker
    Root --> IAP
    Root --> Satori
    Root --> Storage
    Root --> MFA
    Root --> Party
```

`flags` 包通过反射从 Config 结构体自动生成命令行参数,支持嵌套结构体、切片类型、YAML tag 别名。这是 Uber 开发的配置库的内部 fork。

---

## 11. 指标系统

```mermaid
graph TB
    subgraph MetricsSystem["Metrics 指标系统"]
        API["ApiServer Metrics<br/>gRPC stats handler"]
        WS["WebSocket Metrics<br/>消息/字节统计"]
        Runtime["Runtime Metrics<br/>RPC 调用统计"]
        Custom["自定义指标<br/>计数器/仪表/直方图"]
    end

    subgraph Backend["后端"]
        Tally["tally/v4<br/>指标抽象层"]
        Prometheus["Prometheus<br/>暴露 /metrics"]
    end

    subgraph Cardinality["基数限制"]
        Limit["限制 label 维度<br/>防止指标爆炸"]
    end

    API --> Tally
    WS --> Tally
    Runtime --> Tally
    Custom --> Tally
    Tally --> Prometheus
    Prometheus --> Limit
```

---

## 12. 包依赖关系

```mermaid
graph TB
    subgraph Entry["入口"]
        Main["main.go"]
    end

    subgraph Core["核心包"]
        Server["server/"]
        Console["console/"]
        ApiGrpc["apigrpc/"]
    end

    subgraph Support["支撑包"]
        Migrate["migrate/"]
        Social["social/"]
        IAP["iap/"]
        Flags["flags/"]
        SE["se/"]
    end

    subgraph Internal["内部包"]
        GopherLua["internal/gopher-lua"]
        Satori["internal/satori"]
        Cronexpr["internal/cronexpr"]
        Ctxkeys["internal/ctxkeys"]
        SkipList["internal/skiplist"]
    end

    subgraph External["外部依赖"]
        NakamaCommon["nakama-common<br/>(api + rtapi + runtime)"]
        PGX["pgx/v5"]
        Zap["zap (日志)"]
        Gorilla["gorilla<br/>(mux + websocket)"]
        Tally["tally/v4<br/>(指标)"]
        Goja["goja<br/>(JS 引擎)"]
        Bluge["bluge<br/>(全文搜索)"]
    end

    Main --> Server
    Main --> Console
    Main --> Migrate
    Main --> Social
    Main --> SE

    Server --> ApiGrpc
    Server --> Social
    Server --> Internal
    Server --> NakamaCommon
    Server --> PGX
    Server --> Zap
    Server --> Gorilla
    Server --> Tally
    Server --> Goja
    Server --> Bluge

    Console --> ApiGrpc

    Migrate --> PGX
```

---

## 13. 启动与关闭流程

```mermaid
sequenceDiagram
    participant Main as main()
    participant Config as Config/Parser
    participant DB as PostgreSQL
    participant Components as Core Components
    participant API as ApiServer
    participant Console as ConsoleServer
    participant OS as OS Signals

    Main->>Config: ParseArgs + ValidateConfig
    Main->>DB: DbConnect + migrate check
    Main->>Components: 初始化全部组件<br/>(Metrics → Session → Tracker →<br/>Match → Leaderboard → Runtime → ...)
    Main->>API: StartApiServer(:7350)
    Main->>Console: StartConsoleServer(:7351)
    Main->>Main: startupLogger.Info("Startup done")

    Note over Main,OS: 服务器运行中...

    OS-->>Main: SIGINT / SIGTERM
    Main->>Main: HandleShutdown<br/>(优雅关闭匹配)
    Main->>Main: ctxCancelFn()
    Main->>API: Stop()
    Main->>Console: Stop()
    Main->>Components: 逆序停止组件<br/>(Matchmaker → Scheduler →<br/>Tracker → Status → Session → Metrics)
    Main->>Main: "Shutdown complete"
```

关闭顺序很重要:
1. 先 `HandleShutdown` - 通知所有比赛优雅结束
2. 停止 API 和 Console 接收新请求
3. 停止 Matchmaker
4. 停止 LeaderboardScheduler
5. 停止 Tracker
6. 停止 Session/Status
7. 最后停止 Metrics
