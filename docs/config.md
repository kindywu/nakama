# Nakama 配置系统设计文档

## 1. 概述

Nakama 配置系统采用**三层优先级**架构: 默认值 → YAML 文件 → CLI 参数。配置结构体通过反射自动生成命令行 Flag(基于 Uber fx flags 的内部 fork)。所有配置通过 `Config` 接口暴露,包含 20 个子配置域。

### 1.1 配置加载流程

```mermaid
graph TB
    subgraph Layer1["第一层: 默认值"]
        Defaults["NewConfig(logger)<br/>填充所有默认值"]
    end

    subgraph Layer2["第二层: YAML 文件"]
        Pass1["第一轮 CLI 解析<br/>提取 --config 路径"]
        YAML["yaml.Unmarshal()<br/>覆盖默认值"]
    end

    subgraph Layer3["第三层: CLI 参数"]
        Pass2["第二轮 CLI 解析<br/>覆盖 YAML 值"]
    end

    subgraph PostProcess["后处理"]
        EnvMerge["合并 runtime.env<br/>(所有来源累加)"]
        GoogleAuth["解析 Google OAuth<br/>credentials_json"]
        Validate["ValidateConfig()<br/>硬校验 + 软警告"]
    end

    Defaults --> Pass1
    Pass1 --> YAML
    YAML --> Pass2
    Pass2 --> EnvMerge
    EnvMerge --> GoogleAuth
    GoogleAuth --> Validate
```

### 1.2 Config 接口

```go
type Config interface {
    GetName() string
    GetDataDir() string
    GetShutdownGraceSec() int
    GetLogger() *LoggerConfig
    GetMetrics() *MetricsConfig
    GetSession() *SessionConfig
    GetSocket() *SocketConfig
    GetDatabase() *DatabaseConfig
    GetSocial() *SocialConfig
    GetRuntime() *RuntimeConfig
    GetMatch() *MatchConfig
    GetTracker() *TrackerConfig
    GetConsole() *ConsoleConfig
    GetLeaderboard() *LeaderboardConfig
    GetMatchmaker() *MatchmakerConfig
    GetIAP() *IAPConfig
    GetGoogleAuth() *GoogleAuthConfig
    GetSatori() *SatoriConfig
    GetStorage() *StorageConfig
    GetMFA() *MFAConfig
    GetParty() *PartyConfig
}
```

---

## 2. Flag 自动生成机制

### 2.1 反射驱动

`flags` 包通过反射递归遍历配置结构体,自动生成 `flag.FlagSet`:

```go
type FlagMakingOptions struct {
    UseLowerCase bool   // 小写化 Flag 名称
    Flatten      bool   // false = 点分隔命名空间
    TagName      string // 从哪个 struct tag 读取名称(如 "yaml")
    TagUsage     string // 从哪个 struct tag 读帮助文本(如 "usage")
}
```

**Nakama 使用:** `UseLowerCase: true`, `Flatten: false`, `TagName: "yaml"`, `TagUsage: "usage"`

### 2.2 Flag 命名规则

```
顶级字段: --socket.port
嵌套字段: --session.token_expiry_sec
切片字段: --database.address (可重复)
匿名嵌入: 扁平化到父级命名空间
```

**支持的类型:** `string`, `bool`, `float32/64`, `int/int8-64`, `uint/uint8-64`, `time.Duration`(int64), `[]string`, `[]int`, `[]float64`。

### 2.3 环境变量

Nakama **不**自动映射环境变量到配置字段。仅使用:
- `NAKAMA_TELEMETRY=0` → 禁用匿名遥测
- `TEST_DB_URL` → 测试数据库连接(仅测试环境)

Runtime 环境变量通过 YAML/CLI 的 `runtime.env` 配置注入到脚本中。

---

## 3. 配置校验

### 3.1 硬校验 (Fatal on Failure)

**数据库:**
- 至少一个 `database.address` 必须指定
- 地址必须为有效的 PostgreSQL URL
- `dns_scan_interval_sec` >= 1

**Socket:**
- `server_key` 不能为空
- `protocol` 必须是 "tcp", "tcp4", 或 "tcp6"
- 所有 `*_ms` 参数 >= 1
- `ping_period_ms` < `pong_wait_ms`

**Session:**
- `encryption_key` 不能为空
- `refresh_encryption_key` 不能为空
- `encryption_key` != `refresh_encryption_key`
- `single_match` / `single_party` 需 `single_socket`

**Runtime:**
- VM 池 min/max 数量的互序和最小值约束
- `event_queue_size` >= 1, `event_queue_workers` >= 1

**Console:**
- `username`, `password`, `signing_key` 不能为空
- 各种 timeout >= 1

**SSL:**
- 证书和私钥必须同时设置或都不设置
- 文件必须存在且可解析为 X509 密钥对

**MFA:**
- `storage_encryption_key` 必须恰好 32 字节

### 3.2 软警告 (Logged, Returned as configWarnings)

| 条件 | 警告 |
|------|------|
| `console.username == "admin"` | 不安全的默认用户名 |
| `console.password == "password"` | 不安全的默认密码 |
| `console.signing_key == "defaultsigningkey"` | 不安全的默认签名密钥 |
| `socket.server_key == "defaultkey"` | 不安全的默认服务端密钥 |
| `session.encryption_key == "defaultencryptionkey"` | 不安全的默认加密密钥 |
| `runtime.http_key == "defaulthttpkey"` | 不安全的默认 HTTP 密钥 |
| 启用 TLS 直接终端 | 不推荐用于生产环境 |
| 使用废弃的 runtime 配置键 | 迁移提示 |

---

## 4. 完整配置参考

### 4.1 顶级配置

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `name` | string | `"nakama"` | 节点名称(1-16字符) |
| `data_dir` | string | `$CWD/data` | 数据目录 |
| `shutdown_grace_sec` | int | 0 | 优雅关闭宽限期(秒) |
| `config` | []string | nil | YAML 配置文件路径 |

### 4.2 LoggerConfig (`logger`)

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `level` | string | `"info"` | 日志级别(debug/info/warn/error) |
| `stdout` | bool | true | 输出到标准输出 |
| `file` | string | `""` | 日志文件路径(空=不写文件) |
| `rotation` | bool | false | 是否轮转 |
| `max_size` | int | 100 | 轮转文件最大大小(MB) |
| `max_age` | int | 0 | 轮转文件最大天数 |
| `max_backups` | int | 0 | 最大备份数 |
| `local_time` | bool | false | 是否使用本地时间(默认UTC) |
| `compress` | bool | false | 是否压缩轮转文件 |
| `format` | string | `"json"` | 输出格式(json/stackdriver) |

### 4.3 MetricsConfig (`metrics`)

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `reporting_freq_sec` | int | 60 | 快照报告频率 |
| `namespace` | string | `""` | 指标命名空间(作为 tag) |
| `prometheus_port` | int | 0 | Prometheus 暴露端口(0=禁用) |
| `prefix` | string | `"nakama"` | 指标前缀 |
| `custom_prefix` | string | `"custom"` | 自定义指标前缀 |
| `custom_scope_limit` | int | 10000 | 自定义标签基数上限(0=无限制) |

### 4.4 SessionConfig (`session`)

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `encryption_key` | string | `"defaultencryptionkey"` | 会话令牌 JWT 签名密钥 |
| `token_expiry_sec` | int | 60 | 会话令牌过期时间 |
| `refresh_encryption_key` | string | `"defaultrefreshencryptionkey"` | 刷新令牌 JWT 密钥 |
| `refresh_token_expiry_sec` | int | 3600 | 刷新令牌过期时间 |
| `single_socket` | bool | false | 单套接字模式 |
| `single_match` | bool | false | 单比赛模式(需 single_socket) |
| `single_party` | bool | false | 单组队模式(需 single_socket) |
| `single_session` | bool | false | 单会话模式(新登录踢旧会话) |

### 4.5 SocketConfig (`socket`)

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `server_key` | string | `"defaultkey"` | 服务端密钥 |
| `port` | int | 7350 | API 端口 |
| `address` | string | `""` | 绑定地址(空=所有接口) |
| `protocol` | string | `"tcp"` | 协议(tcp/tcp4/tcp6) |
| `max_message_size_bytes` | int | 4096 | WebSocket 单消息最大字节 |
| `max_request_size_bytes` | int | 262144 | HTTP 请求体最大字节 |
| `read_timeout_ms` | int | 10000 | 读超时 |
| `write_timeout_ms` | int | 10000 | 写超时 |
| `idle_timeout_ms` | int | 60000 | 空闲超时 |
| `ping_period_ms` | int | 15000 | WebSocket Ping 间隔 |
| `pong_wait_ms` | int | 25000 | WebSocket Pong 等待超时 |
| `write_wait_ms` | int | 5000 | WebSocket 写等待超时 |
| `close_ack_wait_ms` | int | 2000 | WebSocket 关闭确认等待 |
| `outgoing_queue_size` | int | 64 | WebSocket 出站消息缓冲 |
| `ping_backoff_threshold` | int | 20 | Ping 检测延迟的消息数阈值 |

### 4.6 DatabaseConfig (`database`)

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `address`(YAML: addresses) | []string | `["root@localhost:26257"]` | 数据库地址列表 |
| `conn_max_lifetime_ms` | int | 3600000 | 连接最大生命周期(ms) |
| `max_open_conns` | int | 100 | 最大打开连接数 |
| `max_idle_conns` | int | 100 | 最大空闲连接数 |
| `dns_scan_interval_sec` | int | 60 | DNS 重扫间隔(秒) |

### 4.7 RuntimeConfig (`runtime`)

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `path` | string | `{data_dir}/modules` | 模块路径 |
| `http_key` | string | `"defaulthttpkey"` | HTTP RPC 密钥 |
| `lua_min_count` | int | 16 | Lua VM 池最小数量 |
| `lua_max_count` | int | 48 | Lua VM 池最大数量 |
| `js_min_count` | int | 16 | JavaScript VM 池最小数量 |
| `js_max_count` | int | 32 | JavaScript VM 池最大数量 |
| `lua_call_stack_size` | int | 128 | Lua 调用栈大小 |
| `lua_registry_size` | int | 512 | Lua 注册表大小 |
| `event_queue_size` | int | 65536 | 事件队列大小 |
| `event_queue_workers` | int | 8 | 事件处理 worker 数 |
| `lua_read_only_globals` | bool | true | Lua 只读全局变量 |
| `js_read_only_globals` | bool | true | JS 只读全局变量 |
| `lua_api_stacktrace` | bool | false | Lua API 错误时输出堆栈 |
| `env` | []string | `[]` | 传递给 Runtime 的环境变量(key=value) |

### 4.8 MatchConfig (`match`)

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `input_queue_size` | int | 128 | 输入消息队列大小 |
| `call_queue_size` | int | 128 | 调用队列大小 |
| `signal_queue_size` | int | 10 | 信号队列大小 |
| `join_attempt_queue_size` | int | 128 | 加入尝试队列大小 |
| `deferred_queue_size` | int | 128 | 延迟广播队列大小 |
| `join_marker_deadline_ms` | int | 15000 | 加入标记截止时间 |
| `max_empty_sec` | int | 0 | 空闲比赛最大存活时间(0=不限) |
| `label_update_interval_ms` | int | 1000 | 标签更新间隔 |

### 4.9 ConsoleConfig (`console`)

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `port` | int | 7351 | Console 端口 |
| `max_message_size_bytes` | int64 | 4194304 | 最大消息大小(4MB) |
| `read_timeout_ms` | int | 10000 | 读超时 |
| `write_timeout_ms` | int | 60000 | 写超时 |
| `idle_timeout_ms` | int | 300000 | 空闲超时 |
| `username` | string | `"admin"` | 内置管理员用户名 |
| `password` | string | `"password"` | 内置管理员密码 |
| `token_expiry_sec` | int64 | 86400 | Console Token 过期时间(24h) |
| `signing_key` | string | `"defaultsigningkey"` | Console JWT 签名密钥 |

### 4.10 LeaderboardConfig (`leaderboard`)

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `blacklist_rank_cache` | []string | `[]` | 不缓存的排行榜 ID(`*`=全部禁用) |
| `callback_queue_size` | int | 65536 | 回调队列大小 |
| `callback_queue_workers` | int | 8 | 回调处理 worker 数 |
| `rank_cache_workers` | int | 1 | 排名缓存预加载 worker 数 |

### 4.11 MatchmakerConfig (`matchmaker`)

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `max_tickets` | int | 3 | 每个 Session/Party 最大并发匹配票 |
| `interval_sec` | int | 15 | 匹配处理间隔(秒) |
| `max_intervals` | int | 2 | 最大间隔数 |
| `rev_precision` | bool | false | 精确反向匹配验证 |
| `rev_threshold` | int | 1 | 反向匹配阈值 |

### 4.12 MFAConfig (`mfa`)

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `storage_encryption_key` | string | `"the-key-has-to-be-32-bytes-long!"` | MFA 机密存储加密密钥(32字节) |
| `admin_account_enabled` | bool | false | 管理员账户是否启用 MFA |

### 4.13 其他配置

| 配置域 | 关键参数 |
|--------|---------|
| `SocialConfig` | `steam.publisher_key`, `steam.app_id`, `facebook_instant_game.app_secret`, `facebook_limited_login.app_id`, `apple.bundle_id` |
| `IAPConfig` | `apple.shared_password`, `google.client_email/private_key`, `huawei.public_key/client_id/client_secret` |
| `GoogleAuthConfig` | `credentials_json`(自动解析为 OAuth2 Config) |
| `SatoriConfig` | `url`, `api_key_name`, `api_key`, `signing_key`, `server_key`, `cache_mode`, `cache_ttl_sec` |
| `StorageConfig` | `disable_index_only`(禁用仅索引模式) |
| `TrackerConfig` | `event_queue_size`(默认 1024) |
| `PartyConfig` | `label_update_interval_ms`(默认 1000), `idle_check_interval_ms`(默认 30000) |

---

## 5. 配置文件示例

```yaml
name: "nakama-prod-1"
data_dir: "/var/lib/nakama/data"

logger:
  level: "info"
  stdout: true
  format: "json"

metrics:
  prometheus_port: 9100
  prefix: "nakama"

session:
  encryption_key: "your-production-encryption-key"
  refresh_encryption_key: "your-production-refresh-key"
  token_expiry_sec: 300

socket:
  server_key: "your-production-server-key"
  port: 7350
  max_request_size_bytes: 524288

database:
  address:
    - "postgresql://nakama:password@db-host:5432/nakama?sslmode=require"
  max_open_conns: 200

runtime:
  http_key: "your-production-http-key"
  lua_min_count: 32
  lua_max_count: 64
  env:
    - "GAME_VERSION=1.0"
    - "REGION=us-west"

console:
  port: 7351
  username: "admin-console"
  password: "strong-password"
  signing_key: "your-production-console-signing-key"

matchmaker:
  interval_sec: 10
  max_intervals: 3
```
