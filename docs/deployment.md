# Nakama 部署运维设计文档

## 1. 概述

Nakama 以 Go 单体二进制方式部署,内置 Docker 多架构构建支持。提供完整的健康检查、Prometheus 指标暴露、结构化日志、数据库迁移和优雅关闭能力。

---

## 2. 启动与关闭流程

### 2.1 完整启动序列

```mermaid
sequenceDiagram
    participant Main as main()
    participant Config as Config System
    participant Log as Logger
    participant DB as PostgreSQL
    participant Components as Core Components
    participant API as ApiServer
    participant Console as ConsoleServer
    participant OS as OS Signals

    Main->>Main: 1. 检查子命令(version/migrate/check/healthcheck)
    Main->>Config: 2. ParseArgs() 三层配置加载
    Main->>Log: 3. SetupLogging() 创建结构化日志
    Main->>Config: 4. ValidateConfig() 配置校验
    Main->>Log: 5. 输出启动信息(node/version/go版本/CPU数)
    Main->>DB: 6. DbConnect() 建立数据库连接
    Main->>DB: 7. migrate.Check() 验证迁移状态
    Main->>Main: 8. 初始化 social.SocialClient

    Note over Main,Components: 9. 初始化全部核心组件(按序)
    Main->>Components: Metrics → SessionRegistry
    Main->>Components: SessionCache → StatusRegistry
    Main->>Components: Tracker → MessageRouter
    Main->>Components: LeaderboardCache → LeaderboardRankCache
    Main->>Components: LeaderboardScheduler → MatchRegistry
    Main->>Components: StreamManager → StorageIndex
    Main->>Components: PartyRegistry → Runtime(加载Lua/JS/Go)
    Main->>Components: Matchmaker → Pipeline

    Main->>API: 10. StartApiServer(:7350)
    Main->>Console: 11. StartConsoleServer(:7351)

    Note over Main,OS: 12. 服务器运行中...
    OS-->>Main: 13. SIGINT / SIGTERM

    Main->>Main: 14. HandleShutdown(graceSeconds)
    Main->>Components: 15. 逆序停止组件
```

### 2.2 组件停止顺序

```
1. ctxCancelFn()              -- 取消全局上下文
2. apiServer.Stop()           -- HTTP 网关关闭 → gRPC 优雅停止
3. consoleServer.Stop()       -- 同上
4. matchmaker.Stop()          -- 停止匹配处理
5. leaderboardScheduler.Stop() -- 停止定时器
6. tracker.Stop()             -- 停止 Presence 事件处理
7. statusRegistry.Stop()      -- 停止状态事件队列
8. sessionCache.Stop()        -- 停止会话缓存清理
9. sessionRegistry.Stop()     -- 停止会话注册表
10. metrics.Stop()            -- Prometheus HTTP 关闭 → tally 关闭
11. loginAttemptCache.Stop()  -- 停止登录限流缓存清理
```

### 2.3 优雅关闭

```mermaid
sequenceDiagram
    participant OS as OS Signal
    participant Main as main()
    participant Shutdown as HandleShutdown
    participant MatchReg as MatchRegistry
    participant Runtime as Runtime

    OS-->>Main: SIGINT/SIGTERM

    Main->>Shutdown: HandleShutdown(logger, graceSeconds, runtime, matchRegistry)

    alt graceSeconds > 0
        Shutdown->>Runtime: 启动 goroutine 执行 Shutdown Hook
        Shutdown->>Shutdown: 启动 gracePeriod Timer
        Shutdown->>MatchReg: Stop(graceSeconds) 等待比赛结束

        alt 收到第二次信号 或 Timer 超时
            Shutdown->>MatchReg: Stop(0) 强制停止
        end

        Shutdown->>Runtime: 等待 Shutdown Hook 完成(最多 gracePeriod)
    else graceSeconds == 0
        Shutdown->>MatchReg: Stop(0) 立即停止所有比赛
    end
```

**关闭测试验证:**
- 无宽限期: Shutdown 函数不执行
- 有宽限期: Shutdown 函数执行
- Runtime 超时: 不会被无限等待(硬超时)
- Match 超时: 先尝试优雅,再强制停止

---

## 3. Docker 部署

### 3.1 生产镜像

**Dockerfile:** `build/Dockerfile`

```mermaid
graph TB
    subgraph Builder["构建阶段"]
        Golang["golang:1.26.1-bookworm<br/>CGO_ENABLED=1"]
        Cross["交叉编译<br/>TARGETOS/TARGETARCH"]
        Build["go build -trimpath -mod=vendor<br/>-ldflags '-s -w'"]
        Version["注入版本号<br/>-X main.version=$VERSION<br/>-X main.commitID=$COMMIT"]
    end

    subgraph Runtime["运行阶段"]
        Base["debian:bookworm-slim<br/>+ ca-certificates tzdata<br/>+ iproute2 tini"]
        Dir["创建 /nakama/data/modules"]
        Ports["EXPOSE 7349 7350 7351"]
        Entry["ENTRYPOINT ['/usr/bin/tini', '--']<br/>CMD ['/nakama/nakama']"]
        Health["HEALTHCHECK<br/>nakama healthcheck<br/>间隔30s 超时10s"]
    end

    Builder --> Runtime
```

### 3.2 多架构构建

`build/build.sh` 使用 `docker buildx build`:

```bash
docker buildx build \
    --platform linux/amd64,linux/arm64 \
    -t heroiclabs/nakama:${VERSION} \
    -t heroiclabs/nakama:latest
```

同时构建三个镜像:
- `nakama` — 生产镜像
- `nakama-dsym` — 调试符号变体(`-gcflags "all=-N -l"`, 保留符号表)
- `nakama-pluginbuilder` — Go Runtime Plugin 编译环境(基于 `golang:1.26.1-bookworm`)

### 3.3 Docker Compose

```yaml
# docker-compose.yml (CockroachDB)
services:
  cockroachdb:
    image: cockroachdb/cockroach:latest-v24.1
  nakama:
    build: .
    entrypoint:
      - "/bin/sh"
      - "-ecx"
      - "/nakama/nakama migrate up --database.address root@cockroachdb:26257 &&
         exec /nakama/nakama --name nakama1 --database.address root@cockroachdb:26257"

# docker-compose-postgres.yml (PostgreSQL 16.8)
# docker-compose-tests.yml (测试环境: PostgreSQL + Go 1.26.1 测试运行器)
```

所有 Compose 文件启动前执行 `nakama migrate up`。

---

## 4. 数据库迁移

### 4.1 迁移命令

| 命令 | 说明 |
|------|------|
| `nakama migrate up [--limit N]` | 执行 N 个待处理的迁移(不加参数=全部) |
| `nakama migrate down [--limit N]` | 回滚 N 个迁移(默认 1) |
| `nakama migrate redo` | 回滚1个 + 重新执行 |
| `nakama migrate status` | 列出所有迁移及其应用状态 |

### 4.2 启动时检查

```go
// 启动时自动检查
migrate.Check(ctx, startupLogger, pgxConn)

if embedded > applied:
    Fatal: "DB schema outdated, run 'nakama migrate up'"
if applied > embedded:
    Warn:  "DB schema newer, update Nakama"
```

### 4.3 迁移文件

- 位置: `migrate/sql/`
- 命名: `YYYYMMDDHHmmSS-description.sql`
- 嵌入: `//go:embed sql/*`
- 格式: `-- +migrate Up` / `-- +migrate Down`
- 追踪表: `migration_info`(id, applied_at)
- 共 18 个迁移文件,从 2018-01-03 到 2026-03-19

---

## 5. 健康检查

### 5.1 CLI 健康检查

```bash
nakama healthcheck [port]
# GET http://localhost:{port}/ → 期望 200
# Docker HEALTHCHECK 使用此命令(间隔30s, 超时10s)
```

### 5.2 gRPC 健康检查

`/nakama.api.Nakama/Healthcheck` — 返回 `Empty`,**免认证**。

### 5.3 状态端点

`/v2/console/status` 返回:

| 字段 | 说明 |
|------|------|
| `node` | 节点名称 |
| `health` | 健康状态(始终 OK) |
| `session_count` | 活跃会话数 |
| `presence_count` | 在线 Presence 数 |
| `match_count` | 活跃比赛数 |
| `goroutine_count` | Go 协程数 |
| `avg_latency_ms` | 平均延迟 |
| `avg_rate_sec` | 平均请求速率(/秒) |
| `avg_input_kbs` / `avg_output_kbs` | 平均输入/输出( KB/s) |
| `party_count` | 活跃组队数 |
| `server_create_time` | 服务器启动时间 |

---

## 6. Prometheus 指标

### 6.1 指标架构

```mermaid
graph TB
    subgraph Collection["指标采集"]
        Tally["Uber tally/v4<br/>指标抽象层"]
        PromReporter["Prometheus Reporter<br/>暴露 /metrics"]
    end

    subgraph Metrics["关键指标"]
        DBStats["数据库连接池<br/>db_max_open_conns<br/>db_in_use_conns<br/>db_total_wait_count"]
        APIStats["API 指标<br/>overall_count<br/>overall_latency_ms<br/>overall_errors<br/>per-endpoint 细粒度"]
        WSStats["WebSocket 指标<br/>socket_ws_opened<br/>socket_ws_closed<br/>overall_message_*"]
        RuntimeStats["Runtime 指标<br/>lua_runtimes<br/>javascript_runtimes<br/>authoritative_matches"]
        MatchmakerStats["Matchmaker 指标<br/>matchmaker_tickets<br/>matchmaker_process_time"]
        CustomMetrics["自定义指标<br/>Counter/Gauge/Timer<br/>基数限制(max 10000)"]
    end

    Tally --> PromReporter
    PromReporter --> Metrics
```

### 6.2 指标前缀

- 系统指标: `nakama_*`
- 自定义指标: `custom_*`(由 runtime 代码创建)
- Label 基数限制: 默认 10000 个唯一标签组合

### 6.3 快照系统

5 秒间隔的定时器计算滚动平均值:
- `avg_latency_ms` — 平均延迟
- `avg_rate_sec` — 平均请求速率
- `avg_input_kbs` / `avg_output_kbs` — 平均吞吐

### 6.4 自定义指标安全

`metrics_limited_scope.go` 包装 tally scope,限制唯一标签子 Scope 数量:
- 防止 Runtime 代码通过无限标签组合造成指标爆炸
- 超出限制时记录警告并丢弃

---

## 7. 日志系统

### 7.1 日志架构

```go
// 默认: stdout JSON 格式,info 级别
logger, _ := config.NewLogger()

// 输出格式
type LoggerConfig struct {
    Level      string // debug, info, warn, error
    Stdout     bool   // 输出到 stdout
    File       string // 输出到文件(可选)
    Rotation   bool   // 是否轮转(lumberjack)
    MaxSize    int    // 轮转大小(MB)
    Format     string // json 或 stackdriver(Google Cloud)
}
```

### 7.2 Trace ID 传播

```go
func LoggerWithTraceId(ctx context.Context, logger *zap.Logger) (*zap.Logger, string) {
    traceID := ctx.Value(ctxTraceId{}).(string) // UUID v4
    return logger.With(zap.String("trace_id", traceID)), traceID
}
```

每个 API 请求都有独立的 Trace ID,通过日志关联。

### 7.3 重定向标准库日志

```go
// 将所有 log.Print* 重定向到 zap
log.SetOutput(zap.RedirectStdLogWriter(logger))
```

---

## 8. 匿名遥测

**包:** `se/se.go`

- 默认启用,通过 `NAKAMA_TELEMETRY=0` 环境变量禁用
- 向 Segment HTTP API 发送匿名的 `identify` + `track "start"` / `track "end"` 事件
- Cookie UUID 持久化到 `{data_dir}/.cookie`
- 发送的信息: Go 版本、Nakama 版本、OS 信息
- HTTP 超时 5 秒,错误静默忽略

---

## 9. 数据库连接管理

### 9.1 连接池配置

```go
db.SetConnMaxLifetime(config.Database.ConnMaxLifetimeMs * time.Millisecond) // 默认 1h
db.SetMaxOpenConns(config.Database.MaxOpenConns)                             // 默认 100
db.SetMaxIdleConns(config.Database.MaxIdleConns)                             // 默认 100
```

### 9.2 CockroachDB HA 支持

DNS 地址变化检测:
```go
go func() {
    ticker := time.NewTicker(DnsScanIntervalSec * time.Second) // 默认 60 秒
    for range ticker.C {
        lookupHosts()  // 检测 CockroachDB 节点上下线
    }
}()
```

### 9.3 数据库自动创建

迁移模式(`DbConnect` 的 `create=true`)会在连接后检查数据库是否存在,不存在则执行 `CREATE DATABASE`。

---

## 10. pprof 性能分析

Console 端口提供 Go pprof 端点,受 Basic Auth 保护:

```
/debug/pprof/
/debug/pprof/cmdline
/debug/pprof/profile
/debug/pprof/heap
/debug/pprof/goroutine
/debug/pprof/threadcreate
/debug/pprof/block
```

---

## 11. 生产部署清单

### 11.1 安全配置

| 项目 | 要求 |
|------|------|
| `socket.server_key` | 生成随机字符串(≥32字节) |
| `session.encryption_key` | 生成随机字符串(≥32字节) |
| `session.refresh_encryption_key` | 生成随机字符串,必须与 encryption_key 不同 |
| `console.signing_key` | 生成随机字符串(≥32字节) |
| `runtime.http_key` | 生成随机字符串(≥32字节) |
| `mfa.storage_encryption_key` | 恰好 32 字节的随机密钥 |
| `console.password` | 强密码(≥16字符) |
| SSL | 使用反向代理(HAProxy/Nginx)处理,不直接启用 Nakama SSL |

### 11.2 数据库

| 项目 | 建议 |
|------|------|
| 连接池 | 根据实例数调整 max_open_conns |
| 备份 | 定期备份 PostgreSQL/CockroachDB |
| 迁移 | CI/CD 中集成 `nakama migrate check` |

### 11.3 监控

| 项目 | 说明 |
|------|------|
| Prometheus | 启用 `metrics.prometheus_port`,接入 Grafana |
| 日志 | 使用 JSON 格式,接入集中式日志(ELK/Loki) |
| 健康检查 | 配置 Load Balancer 的健康检查端点 |

### 11.4 Docker 运行命令

```bash
docker run -d \
    --name nakama \
    -p 7350:7350 -p 7351:7351 \
    -v /path/to/data:/nakama/data \
    heroiclabs/nakama:latest \
    --config /nakama/data/config.yml \
    --database.address "postgresql://user:pass@host:5432/nakama?sslmode=require"
```
