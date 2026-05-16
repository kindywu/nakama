# Nakama 技术栈与工程规范

## 1. 技术栈总览

### 1.1 后端技术栈

| 层面 | 技术选型 | 版本 | 用途 | 应用场景 |
|------|---------|------|------|---------|
| 语言 | Go | 1.26+ | 服务端主体语言 | 整个游戏服务器由 Go 编写,编译为单一静态二进制 |
| 数据库 | PostgreSQL | 16.8 (Alpine) | 主数据库 | 存储玩家账户、排行榜、群组、钱包、比赛记录等所有持久化数据 |
| 数据库 | CockroachDB | v24.1 | 可选分布式 SQL 数据库 | 需要水平扩展、多区域部署时替代 PostgreSQL |
| 数据库驱动 | pgx/v5 | v5.9.2 | PostgreSQL/CockroachDB 原生驱动 | 高性能连接池,支持预处理语句和批量查询 |
| DB 迁移 | sql-migrate (heroiclabs fork) | — | 数据库 schema 迁移 | 启动时自动将数据库升级到目标版本,无需手动执行 SQL |
| RPC 框架 | gRPC | v1.81.0 | 服务端 API 定义与服务实现 | 客户端 SDK (Unity/Unreal/Godot 等) 通过 gRPC 调用游戏后端 API |
| HTTP 网关 | gRPC-Gateway (grpc-ecosystem) | v2.29.0 | REST JSON → gRPC Protobuf 自动转换 | 让不支持 gRPC 的客户端 (如 Web 浏览器) 也能用 REST JSON 调用 API |
| WebSocket | gorilla/websocket | v1.5.3 | 实时通信长连接 | 游戏内实时聊天、比赛帧同步、频道消息、在线状态推送 |
| HTTP 路由 | gorilla/mux | v1.8.1 | HTTP 请求路由 | 将 HTTP 请求按路径分发到 gRPC-Gateway 或 WebSocket 处理器 |
| HTTP 中间件 | gorilla/handlers | — | CORS、日志、压缩中间件 | Web 前端跨域访问控制,HTTP 访问日志记录,响应体压缩 |
| 序列化 | google.golang.org/protobuf | v1.36.11 | Protocol Buffers 消息序列化 | 客户端与服务器之间的所有消息均使用 Protobuf 编码,比 JSON 体积更小速度更快 |
| YAML 配置 | yaml.v3 | v3.0.1 | 配置文件解析 | 服务器启动时读取 YAML 配置文件或命令行参数配置运行参数 |
| JWT | golang-jwt/jwt/v5 | v5.3.1 | 会话令牌签发与验证 (HS256) | 玩家登录后签发 JWT,后续请求携带 Token 完成身份验证;Console 管理员登录同理 |
| OAuth2 | golang.org/x/oauth2 | v0.36.0 | 社交登录 (Google, Facebook, Apple, Steam) | 玩家使用社交账号一键登录游戏,无需注册用户名密码 |
| 密码哈希 | golang.org/x/crypto | v0.50.0 | bcrypt 密码存储 | 玩家密码和 Console 管理员密码的哈希存储,防止拖库后明文泄露 |
| TOTP MFA | dgryski/dgoogauth | — | 基于时间的一次性密码多因素认证 | Console 管理员可开启二次验证,登录时除密码外还需输入动态验证码 |
| 静态加密 | crypto/aes, crypto/cipher (标准库) | — | AES-256-GCM 加密静态数据 | 对数据库中的敏感字段 (如 OAuth Token) 进行加密存储 |
| 传输安全 | crypto/tls, crypto/x509 (标准库) | — | TLS/SSL 传输安全 | gRPC 和 HTTP API 均可配置 TLS 证书,加密所有网络通信 |
| UUID | gofrs/uuid/v5 | v5.4.0 | 全局唯一标识符 | 为每个玩家、比赛、群组、通知等实体生成全局唯一 ID,无需数据库自增 |
| 结构化日志 | zap (uber) | v1.28.0 | 高性能结构化日志 | 服务器所有运行日志,支持 JSON 格式输出,便于日志采集系统 (如 ELK) 分析 |
| 日志轮转 | lumberjack | v2 | 日志文件自动轮转 | 按文件大小自动切割日志,防止磁盘写满;保留最近 N 个旧文件 |
| 指标 | Tally v4 + tally/prometheus | — | 指标抽象层 | 业务代码通过 Tally 接口上报指标,底层自动转换为 Prometheus 格式 |
| 指标暴露 | Prometheus (client_golang) | v1.23.2 | Metrics 端点 (端口 9100) | 运维人员配置 Prometheus 定时拉取 9100 端口获取 API 延迟、并发连接数等指标 |
| 全文搜索 | blugelabs/bluge | v0.2.2 | 存储对象全文索引与搜索 | Console 管理员可按玩家 ID、物品名等关键词模糊搜索 Storage 中的 JSON 对象 |
| 文本分析 | blevesearch 系列 (porter stemmer, snowball stem, vellum FST) | — | 词干提取、分词、FST 索引 | 支撑 Bluge 的全文搜索能力,支持英文词干还原 ("running" → "run") |
| 位图索引 | RoaringBitmap/roaring | — | 压缩位图索引 | 加速 Bluge 的倒排索引查找,减少内存占用 |
| 并发安全 | uber-go/atomic | — | 类型安全的原子操作 | 在线人数统计、消息计数等需要无锁并发的计数器场景 |
| 跳表 | skiplist (RyanCarrier fork, 内部) | — | 排行榜排名缓存 | 百万玩家排行榜的实时排名查询,支持 O(log n) 插入和查找 |
| Cron 解析 | cronexpr (aptible/supercronic fork, 内部) | — | 定时排行榜/锦标赛重置 | 配置 "每周一 0 点重置周榜"、"每月 1 日结算赛季" 等定时任务 |
| 压缩 | klauspost/compress | — | HTTP 响应 gzip/flate 压缩 | REST API 返回大量 JSON 数据时自动压缩,减少带宽消耗 |
| 压缩 | golang/snappy | — | Snappy 压缩 | 对 CPU 开销敏感的内部数据传输场景,比 gzip 更快但压缩率略低 |
| 错误处理 | pkg/errors | — | 错误包装与追踪 | 底层错误向上层传递时保留调用栈信息,便于定位问题根因 |
| 匿名遥测 | Segment (自定义 se 包) | — | 匿名使用统计 | Nakama 官方收集版本分布、功能使用频率等匿名数据,指导产品迭代方向 |
| 测试 | stretchr/testify | — | 测试断言与 Mock | 单元测试中验证函数返回值和模拟外部依赖 |

### 1.2 前端技术栈 (Console 管理后台)

Nakama 的 Console 前端是一个**嵌入式 Vue SPA**,源码在独立仓库 [heroiclabs/nakama-console](https://github.com/heroiclabs/nakama-console),此仓库仅包含构建产物。

| 层面 | 技术选型 | 说明 | 应用场景 |
|------|---------|------|---------|
| 框架 | Vue 3 (Composition API) | SPA 单页应用 | 整个 Console 管理后台的页面渲染、组件复用和状态管理 |
| UI 组件库 | 自研 Hiro 组件库 | Heroic Labs 内部设计系统 (Form/Table/Modal/Tab/Button/Input 等) | Console 中所有表单、表格、弹窗、标签页、按钮、输入框等 UI 元素,保证视觉一致性和交互规范 |
| 代码编辑器 | Monaco Editor | VS Code 编辑器内核 | API Explorer 中编写 JSON 请求体 (语法高亮+自动补全+格式化);Storage 中编辑 JSON 存储对象 |
| 构建工具 | Vite (ESBuild + Rollup) | 前端构建打包 | 将 Vue 组件、CSS、字体等源文件编译为带哈希的生产产物,支持 HMR 热更新开发 |
| 路由 | Vue Router 4 | 前端路由 (Hash 模式) | Console 中的页面切换:从账户列表点击进入详情页,URL 变化但不刷新页面,支持浏览器前进/后退 |
| HTTP 客户端 | fetch API | 调用 `/v2/console/...` gRPC-Gateway 端点 | Console 中所有数据操作都通过 HTTP JSON 请求与后端 Console API 通信 |
| API Mock | MSW (Mock Service Worker) | 开发/测试中拦截 API 响应 | 前端开发时后端可能尚未就绪,MSW 在 Service Worker 层拦截请求返回假数据,实现前后端解耦并行开发 |
| 字体 | Inter, Roboto Mono | UI 字体; 等宽代码字体 (WOFF2 格式, 多语言子集) | Inter 用于界面文字,Roboto Mono 用于 API Explorer 和 Storage 编辑器中的代码/JSON 显示 |
| 图标 | codicon (VS Code 图标集) + Hiro 自定义 SVG 图标 | VS Code 图标集 + 游戏主题图标 | codicon 用于通用 UI 图标 (搜索、设置、刷新等);Hiro SVG 按游戏领域分 16 个主题 (achievements 成就、leaderboards 排行榜、teams 战队等) |
| 样式 | 预编译 CSS | 随构建产物发布 | Console 所有页面样式,无运行时 CSS 编译开销 |
| 嵌入方式 | Go `embed.FS` | 静态文件编译进二进制 | 部署时只需一个二进制文件,无需单独部署前端 Web 服务器或 CDN |
| 嵌入路径 | `console/ui/dist/` | 生产构建产物存放位置 | Vite 构建输出到此目录,Go 编译时自动打包 |
| Go 嵌入代码 | `console/ui.go` | `//go:embed ui/dist/*` | 编译时将整个前端代码内嵌到 server 二进制中 |

Console 前端通过端口 7351 的 gRPC-Gateway 与后端 Console API 通信,所有 API 由 `console.proto` 定义。

### 1.3 运行时脚本语言

| 语言 | 引擎 | 入口 | 适用场景 | 应用场景 |
|------|------|------|---------|---------|
| Lua | gopher-lua (内部 fork, `internal/gopher-lua/`) | `*.lua` 文件 | 默认运行时,生态成熟 | 编写服务端自定义逻辑:登录前验证、注册后发欢迎奖励、排行榜结算、比赛胜负判定、IAP 收据校验等 |
| JavaScript | goja (ES5.1+) | `index.js` | JS 开发者友好的替代方案 | 与 Lua 相同的服务端自定义逻辑,适合前端/全栈开发者用熟悉的 JS 语法编写 |
| Go | Go native plugin (`.so`) | 导出函数 | 高性能场景,共享进程内运行 | 需要高性能计算 (如碰撞检测、物理模拟) 的服务器逻辑,编译为 `.so` 插件加载进 Nakama 进程 |

### 1.4 内置 Lua 模块

路径: `data/modules/`

| 文件 | 功能 | 应用场景 |
|------|------|---------|
| `clientrpc.lua` | 客户端 RPC 调用示例 | 演示如何定义客户端可调用的自定义 RPC 函数,如 `rpc_get_daily_reward` |
| `match.lua` | 权威比赛 (Match Handler) 示例 | 演示回合制/实时比赛的生命周期管理:初始化棋盘、处理玩家出招、判定胜负 |
| `match_init.lua` | 比赛初始化逻辑示例 | 演示从 matchmaker 匹配成功后创建比赛的完整流程 |
| `tournament.lua` | 锦标赛逻辑示例 | 演示锦标赛的报名、分组、多轮淘汰、最终排名和奖励发放 |
| `iap_verifier.lua` | 内购验证逻辑 | 收到客户端上报的购买收据后,向 Apple App Store / Google Play 服务器验证真伪 |
| `iap_verifier_rpc.lua` | 内购验证 RPC 端点 | 提供客户端可直接调用的 `rpc_verify_purchase` 函数 |
| `p2prelayer.lua` | P2P 中继逻辑 | 演示玩家之间通过服务器中继消息,实现点对点通信 |
| `debug_utils.lua` | 调试工具函数 | 开发阶段打印运行时状态、日志辅助,便于排查 Lua 脚本问题 |
| `runonce_check.lua` | 一次性检查逻辑 | 演示如何确保某些逻辑 (如初始化数据) 在每个服务器实例上只执行一次 |

### 1.5 基础设施 (Infrastructure)

| 技术 | 用途 | 应用场景 |
|------|------|---------|
| Docker + Docker Compose | 容器化与本地服务编排 | 开发环境一键启动 (nakama + postgres + prometheus);生产环境容器化部署 |
| Docker Buildx | 多平台镜像构建 (linux/amd64, linux/arm64) | 构建同时适用于 x86 云服务器和 ARM 云服务器 (如 AWS Graviton) 的 Docker 镜像 |
| Tini | 容器初始化系统 | 作为容器 PID 1 进程,正确处理 SIGTERM 信号实现优雅关闭,回收僵尸进程 |
| PostgreSQL 16.8 (Alpine) | 生产与测试数据库 | 集成测试和 CI 中使用 Alpine 轻量镜像,启动快占用小 |
| CockroachDB v24.1 | 可选分布式 SQL 数据库 | 游戏全球同服场景,利用 CockroachDB 的自动分片和跨区域复制实现数据库水平扩展 |
| Prometheus | 指标采集与监控 | 运维人员配置 Prometheus 定时抓取 Nakama 9100 端口指标,配合 Grafana 构建监控大盘 |
| Docker Hub (heroiclabs/nakama) | 容器镜像托管 | 用户通过 `docker pull heroiclabs/nakama` 直接获取官方镜像 |

### 1.6 开发工具 (Dev Tools)

| 层面 | 技术 | 用途 | 应用场景 |
|------|------|------|---------|
| Proto 管理 | Buf (buf.build) | Protocol Buffers 构建与 lint 管理 | 开发者修改 `.proto` 文件后运行 `./buf.sh` 一键生成所有代码,同时 lint 检查避免破坏性变更 |
| Proto 生成 | protoc-gen-go | Go Protobuf 消息代码生成 | 将 `.proto` 中的 message 定义编译为 Go struct,生成序列化/反序列化方法 |
| Proto 生成 | protoc-gen-go-grpc | Go gRPC 服务代码生成 | 将 `.proto` 中的 service 定义编译为 Go interface 和 gRPC client/server stub |
| Proto 生成 | protoc-gen-grpc-gateway | gRPC-Gateway REST 代理代码生成 | 自动生成 REST JSON API 的路由和处理函数,无需手写 HTTP → gRPC 转换代码 |
| Proto 生成 | protoc-gen-openapiv2 | OpenAPI/Swagger 规范生成 | 自动生成 `apigrpc.swagger.json` 和 `console.swagger.json`,用于 API 文档和客户端代码生成 |
| 代码检查 | golangci-lint | Go 静态分析 / lint | CI 流水线中自动检查代码质量,防止常见问题 (未处理错误、死代码、风格不一致) 进入主干 |
| 测试 | go test -race | Go 标准测试 + 竞态检测 | 本地开发和 CI 中运行测试,`-race` 在运行时检测 goroutine 数据竞争 |
| 集成测试 | Docker Compose + PostgreSQL | 连接真实数据库的端到端测试 | `docker-compose-tests.yml` 启动完整环境运行全量测试套件,验证与真实数据库的交互正确性 |
| CI/CD | GitHub Actions | 构建、测试、lint、发布流水线 | 代码推送自动触发:golangci-lint 检查 → 单元测试 → 多架构 Docker 镜像构建 → 推送到 Docker Hub |
| 跨平台编译 | QEMU (docker/setup-qemu) | ARM64 指令集模拟 | GitHub Actions 的 AMD64 机器通过 QEMU 用户模式模拟 ARM64 环境,编译出 `linux/arm64` 架构的 Docker 镜像 |
| 构建 | Go build -mod=vendor | vendored 依赖的离线构建 | 构建时不拉取网络,完全使用 `vendor/` 目录中的依赖源码,保证构建可重现 |
| 版本注入 | -ldflags "-X main.version=..." | 编译时注入版本号和 commit ID | 通过 `nakama --version` 查看运行时确切的版本号和 Git commit,便于问题追溯 |

---

## 2. 代码目录结构

```
nakama/
├── main.go                  # 入口点: 子命令解析、组件装配、信号处理
├── go.mod / go.sum          # Go modules 依赖定义
├── buf.yaml / buf.lock      # Buf 工具配置 (Protobuf 代码生成)
├── buf.sh                   # Protobuf 代码生成脚本
│
├── server/                  # ★ 核心包: 所有业务逻辑 (152 个 Go 文件)
│   ├── api.go               #   API Server: gRPC 服务实现入口
│   ├── config.go            #   配置系统: Config 接口 + 20 个子配置域
│   ├── db.go / db_error.go  #   数据库: 连接管理、错误码映射
│   ├── logger.go            #   日志: zap 结构化日志初始化
│   ├── pipeline.go          #   实时管道: WebSocket 消息分发路由
│   ├── tracker.go           #   在线追踪: 流式 Presence 管理
│   ├── session_ws.go        #   WebSocket 会话: 生命周期管理
│   ├── socket_ws.go         #   WebSocket 传输: HTTP 升级 + 读写协程
│   ├── match_registry.go    #   比赛注册表: 权威比赛实例管理
│   ├── matchmaker.go        #   匹配器: 玩家匹配算法
│   ├── message_router.go    #   消息路由: 按 Session/Presence 投递
│   ├── status_registry.go   #   状态注册表: 用户在线状态追踪
│   ├── leaderboard_cache.go     # 排行榜元数据缓存
│   ├── leaderboard_rank_cache.go # 排行榜排名缓存 (SkipList)
│   ├── leaderboard_scheduler.go  # 排行榜 Cron 定时调度
│   ├── storage_index.go         # 存储对象全文索引
│   ├── metrics.go               # Prometheus 指标定义与暴露
│   ├── mfa.go                   # MFA/TOTP 认证
│   ├── jwt.go                   # JWT 令牌生成与验证
│   ├── encryption.go            # AES 加密工具
│   ├── shutdown.go              # 优雅关闭
│   │
│   ├── api_*.go             #   HTTP/gRPC API 处理函数 (20 个文件)
│   ├── core_*.go            #   核心业务逻辑 (21 个文件)
│   ├── console_*.go         #   Console 管理后台 API (21 个文件)
│   ├── pipeline_*.go        #   WebSocket 实时消息处理 (7 个文件)
│   ├── runtime*.go          #   运行时系统 (19 个文件)
│   ├── runtime_lua*.go      #   Lua 运行时 (8 个文件)
│   ├── runtime_javascript*.go # JavaScript 运行时 (8 个文件)
│   ├── runtime_go*.go       #   Go 原生插件运行时 (5 个文件)
│   ├── match_*.go           #   比赛相关 (5 个文件)
│   ├── party_*.go           #   组队相关 (3 个文件)
│   └── *_test.go            #   测试文件 (16 个)
│
├── console/                 # Console API 定义 + 嵌入式 UI
│   ├── console.proto        #   Protobuf 服务定义
│   ├── console.pb.go        #   生成的 Protobuf 消息代码
│   ├── console_grpc.pb.go   #   生成的 gRPC 服务代码
│   ├── console.pb.gw.go     #   生成的 gRPC-Gateway 代码
│   ├── console.swagger.json #   生成的 Swagger/OpenAPI 文档
│   ├── ui.go                #   Go embed 嵌入式文件系统
│   └── ui/dist/             #   Vue SPA 构建产物 (嵌入二进制)
│
├── apigrpc/                 # 客户端 API Protobuf 定义
│   ├── apigrpc.proto        #   Protobuf 服务定义
│   ├── apigrpc.pb.go        #   生成的 Protobuf 消息代码
│   ├── apigrpc_grpc.pb.go   #   生成的 gRPC 服务代码
│   ├── apigrpc.pb.gw.go     #   生成的 gRPC-Gateway 代码
│   └── apigrpc.swagger.json #   生成的 Swagger/OpenAPI 文档
│
├── internal/                # 内部 Vendored/Forks
│   ├── gopher-lua/          #   Lua VM (自定义 fork)
│   ├── satori/              #   UUID 生成 (内部 fork)
│   ├── cronexpr/            #   Cron 表达式解析 (内部 fork)
│   ├── skiplist/            #   跳表数据结构
│   └── ctxkeys/             #   Context key 常量
│
├── migrate/                 # 数据库迁移
│   ├── migrate.go           #   迁移执行引擎
│   └── sql/                 #   SQL 迁移文件 (时间戳命名)
│
├── flags/                   # CLI Flag 自动生成
│   ├── flags.go             #   从 Config 结构体反射生成 Flag
│   └── vars.go              #   Flag 变量类型
│
├── social/                  # 社交登录集成
│   └── social.go            #   Google OAuth 登录
│
├── iap/                     # 内购验证
│   └── iap.go               #   App Store / Google Play 收据验证
│
├── se/                      # 匿名遥测
│   └── se.go                #   Segment 事件上报
│
├── build/                   # 构建与部署
│   ├── Dockerfile           #   生产 Docker 镜像
│   ├── Dockerfile.dsym      #   带调试符号的 Docker 镜像
│   ├── Dockerfile.local     #   本地开发 Docker 镜像
│   ├── build.sh             #   多架构构建脚本 (amd64/arm64)
│   ├── pluginbuilder/       #   Go 插件构建器 Docker 镜像
│   └── do-marketplace/      #   DigitalOcean Marketplace 部署
│
├── data/modules/            # 内置 Lua 脚本 (运行时自动加载)
│
├── examples/                # Go 客户端示例 (使用指南: docs/examples.md)
│   ├── leaderboard/         #   排行榜 HTTP REST 客户端
│   ├── tournament/          #   锦标赛 HTTP REST 客户端 (含 RPC)
│   ├── matchmaker/          #   匹配器 WebSocket 客户端
│   ├── party/               #   组队 + 匹配器 WebSocket 客户端
│   └── ping-pong/           #   WebSocket 心跳 RTT 测量客户端
│
├── sample_go_module/        # Go 插件示例
│
├── vendor/                  # Go vendor 目录 (源码级依赖)
│
└── docs/                    # 技术文档 (本目录)
```

---

## 3. 文件命名规范

### 3.1 server/ 包文件命名

所有 server 包文件采用**前缀_领域**命名模式:

| 前缀 | 用途 | 数量 | 示例 |
|------|------|------|------|
| `api_*.go` | HTTP REST + gRPC API 处理函数,负责请求校验、参数提取、调用核心逻辑、响应组装 | 20 | `api_account.go`, `api_leaderboard.go`, `api_match.go` |
| `core_*.go` | 核心业务逻辑,数据库查询/事务编排,与协议无关 | 21 | `core_account.go`, `core_leaderboard.go`, `core_wallet.go` |
| `console_*.go` | Console 管理后台 API 处理函数 | 21 | `console_account.go`, `console_audit.go`, `console_user.go` |
| `pipeline_*.go` | WebSocket 实时消息处理器 | 7 | `pipeline_channel.go`, `pipeline_match.go`, `pipeline_party.go` |
| `runtime.go` | 运行时核心: 钩子注册、RPC 函数表、Provider 调度 | 1 | — |
| `runtime_lua*.go` | Lua 运行时实现 | 8 | `runtime_lua.go`, `runtime_lua_nakama.go`, `runtime_lua_match_core.go` |
| `runtime_javascript*.go` | JavaScript 运行时实现 | 8 | `runtime_javascript.go`, `runtime_javascript_nakama.go` |
| `runtime_go*.go` | Go 原生插件运行时实现 | 5 | `runtime_go.go`, `runtime_go_nakama.go` |
| `match_*.go` | 比赛系统 | 5 | `match_registry.go`, `match_handler.go`, `match_common.go` |
| `party_*.go` | 组队系统 | 3 | `party_registry.go`, `party_handler.go` |
| `*_test.go` | 测试文件 (与被测文件同名前缀) | 16 | `core_wallet_test.go`, `leaderboard_cache_test.go` |

### 3.2 三层架构模式

```
api_*.go      → 协议层: 解析请求、校验参数、调用核心逻辑、组装响应
    ↓
core_*.go     → 业务层: 纯业务逻辑、数据库事务编排、协议无关
    ↓
db.go         → 数据层: SQL 执行、连接管理、错误映射
```

- `api_*.go` 和 `core_*.go` 通常成对出现,文件名后半部分一致 (如 `api_account.go` ↔ `core_account.go`)。
- Console API 文件 (`console_*.go`) 只有 api 层,直接调用 `core_*.go` 中的核心逻辑。
- Pipeline 文件 (`pipeline_*.go`) 处理 WebSocket 实时消息,不会调用 `api_*.go`,但会调用 `core_*.go` 和专门的实时组件。

### 3.3 SQL 迁移文件命名

路径: `migrate/sql/`

格式: `{timestamp}_{description}.sql`

```
20180103142001_initial_schema.sql
20180805174141-tournaments.sql
20200116134800-facebook-instant-games.sql
20200615102232-apple.sql
20201005180855-console.sql
20210416090601-purchase.sql
20220426122825-subscription.sql
...
```

- 时间戳格式: `YYYYMMDDHHMMSS`
- 描述: 使用小写字母和连字符 `-`
- 不可变: 已提交的迁移文件绝对不能修改,新变更必须创建新迁移

### 3.4 其他命名约定

| 约定 | 说明 |
|------|------|
| Go 包名 | 顶层包用单数名词: `server`, `console`, `flags`, `social`, `iap`, `se` |
| 接口命名 | 功能名词 + er/or 后缀: `SessionRegistry`, `MessageRouter`, `Matchmaker`, `Tracker` |
| 结构体 | PascalCase: `Pipeline`, `ApiServer`, `LeaderboardCache`, `PresenceStream` |
| 函数/方法 | PascalCase (导出) 或 camelCase (非导出): `GetAccount`, `updateWallet` |
| 常量 | PascalCase 或全大写: `StreamModeChannel`, `StreamModeMatchRelayed` |
| 测试函数 | `TestXxx` 格式: `TestLeaderboardRankCache`, `TestMatchPresence` |
| 测试文件 | 与源文件同目录,加 `_test` 后缀: `core_wallet.go` → `core_wallet_test.go` |
| 生成代码 | 后缀清晰标识来源: `.pb.go` (Protobuf), `.pb.gw.go` (gRPC-Gateway), `_grpc.pb.go` (gRPC 服务) |

---

## 4. 依赖管理

### 4.1 Go Modules + Vendor

```
go 1.26.1
module github.com/heroiclabs/nakama/v3
```

- **模块路径**: `github.com/heroiclabs/nakama/v3`
- **依赖策略**: Vendor 模式 (`-mod=vendor`),`vendor/` 目录是依赖的唯一来源
- **构建命令**: `go build -trimpath -mod=vendor`
- **测试命令**: `go test -v -race -mod=vendor ./...`

### 4.2 核心依赖关系

```mermaid
graph TB
    subgraph Nakama["github.com/heroiclabs/nakama/v3"]
        Server["server/"]
        Console["console/"]
        APIGRPC["apigrpc/"]
    end

    subgraph Common["nakama-common"]
        RuntimeAPI["runtime/ (接口定义)"]
        API["api/ (客户端 API 消息)"]
        RTAPI["rtapi/ (实时 API 消息)"]
    end

    subgraph External["外部关键依赖"]
        gRPC["google.golang.org/grpc"]
        GRPCGW["grpc-ecosystem/grpc-gateway/v2"]
        PGX["jackc/pgx/v5"]
        GorillaWS["gorilla/websocket"]
        GorillaMux["gorilla/mux"]
        Zap["uber/zap"]
        Prometheus["prometheus/client_golang"]
        Goja["dop251/goja"]
    end

    Server --> Common
    Server --> External
    Console --> gRPC
    Console --> GRPCGW
    APIGRPC --> gRPC
    APIGRPC --> GRPCGW
```

- `nakama-common` 是分离的公共接口库,定义了 `runtime` 接口 (RuntimeProvider, RuntimeRpcFunction 等) 和 `api`/`rtapi` 消息类型。所有三个运行时 (Lua/JS/Go) 都实现这些接口。
- gRPC-Gateway 在编译时通过 `.proto` 文件生成,不需要运行时转换。

---

## 5. 代码生成

### 5.1 Protobuf 代码生成流程

```mermaid
flowchart LR
    Proto[".proto 文件"] --> Buf["buf generate"]
    Buf --> PB["*.pb.go<br/>Protobuf 消息"]
    Buf --> GRPC["*_grpc.pb.go<br/>gRPC 服务"]
    Buf --> GW["*.pb.gw.go<br/>gRPC-Gateway"]
    Buf --> Swagger["*.swagger.json<br/>OpenAPI 文档"]
```

### 5.2 生成命令

```bash
# 一键生成所有 Protobuf 代码
./buf.sh
```

此脚本调用 `buf generate` 为 `apigrpc/` 和 `console/` 两个 proto 定义生成:

| 产物 | 客户端 API (`apigrpc/`) | Console API (`console/`) |
|------|------------------------|-------------------------|
| Protobuf 消息 | `apigrpc.pb.go` (~150KB) | `console.pb.go` (~370KB) |
| gRPC 服务 | `apigrpc_grpc.pb.go` | `console_grpc.pb.go` (~155KB) |
| gRPC-Gateway | `apigrpc.pb.gw.go` (~200KB) | `console.pb.gw.go` (~387KB) |
| OpenAPI/Swagger | `apigrpc.swagger.json` | `console.swagger.json` (~178KB) |

### 5.3 生成代码约定

- **绝不手动编辑** `.pb.go`、`.pb.gw.go`、`_grpc.pb.go` 文件。所有修改都在 `.proto` 源文件中进行。
- 修改 `.proto` 后必须运行 `./buf.sh` 重新生成。
- 生成代码使用 `protoc-gen-go-grpc v1.6.0` 和 `grpc-gateway/v2 v2.29.0`。

### 5.4 配置文件

```yaml
# buf.yaml - Buf 工具配置 (lint + breaking change 检测)
# buf.lock - 锁定依赖的 proto 文件版本
# console.gen.yaml - Console proto 的 buf generate 配置
# apigrpc.gen.yaml - 客户端 API proto 的 buf generate 配置
```

---

## 6. 构建系统

### 6.1 Go 构建

```bash
# 标准构建 (vendored 依赖)
go build -trimpath -mod=vendor

# 测试 (需要 PostgreSQL)
export TEST_DB_URL=postgresql://postgres:localdb@localhost:5432/nakama?sslmode=disable
go test -v -race ./...

# 单个测试
go test -v -race -run TestFoo ./server/
```

构建参数说明:
- `-trimpath`: 从二进制中移除文件系统路径,保证可重现构建
- `-mod=vendor`: 使用 `vendor/` 目录中的依赖,不拉取网络
- `-race`: 启用竞态检测器

### 6.2 Docker 构建

```bash
# 本地开发
docker compose -f docker-compose.yml up

# 运行测试套件
docker compose -f docker-compose-tests.yml up --build --abort-on-container-exit
docker compose -f docker-compose-tests.yml down -v

# 仅 PostgreSQL
docker compose -f docker-compose-postgres.yml up
```

### 6.3 多架构构建

`build/build.sh` 支持 amd64 和 arm64 双架构:

```bash
./build/build.sh
```

Docker 镜像:
- `build/Dockerfile` — 生产镜像 (alpine 基础)
- `build/Dockerfile.dsym` — 带调试符号
- `build/Dockerfile.local` — 本地开发
- `build/pluginbuilder/` — Go 插件编译环境 (用于构建 `.so` 插件)

---

## 7. 关键设计模式

### 7.1 接口驱动设计

所有核心组件都通过 Go interface 定义契约,在 `server/` 包中声明和实现:

```go
// 示例: SessionRegistry 接口 (session_registry.go)
type SessionRegistry interface {
    Add(session Session) error
    Remove(session Session)
    Get(sessionID uuid.UUID) Session
    // ...
}

// 示例: Matchmaker 接口 (matchmaker.go)
type Matchmaker interface {
    Add(ctx context.Context, presences []*MatchmakerPresence, ...) error
    Remove(sessionID uuid.UUID, ticketID string)
    Process()
    // ...
}
```

### 7.2 三层运行时架构

```
Runtime (runtime.go)
  ├── 钩子注册表 (Before/After hooks for all APIs + Realtime messages)
  ├── RPC 函数注册表 (map[string]RuntimeRpcFunction)
  └── RuntimeProvider 接口
        ├── LuaProvider (runtime_lua*.go)
        ├── JavascriptProvider (runtime_javascript*.go)
        └── GoProvider (runtime_go*.go)
```

所有三个 Provider 实现同一个 `RuntimeProvider` 接口 (定义在 `nakama-common/runtime`),因此核心系统不需要关心运行时的具体语言。

### 7.3 请求生命周期 (三层分离)

```
HTTP/gRPC 请求:
  gorilla/mux → gRPC-Gateway (JSON→Protobuf) → ApiServer (api_*.go)
    → core_*.go (业务逻辑) → db.go (SQL) → PostgreSQL

WebSocket 实时消息:
  socket_ws.go (升级/读写) → session_ws.go (会话)
    → Pipeline (pipeline.go 路由) → pipeline_*.go (消息处理器)
      → core_*.go (共享业务逻辑) → PostgreSQL
```

### 7.4 代码复用策略

- `core_*.go` 是唯一包含数据库查询和业务逻辑编排的层
- `api_*.go` (客户端 API) 和 `console_*.go` (管理后台 API) 都调用同一套 `core_*.go`
- `pipeline_*.go` (实时消息) 也调用 `core_*.go` 中的共享逻辑
- 这确保了三种协议表面 (REST, gRPC, WebSocket) 共享一致的业务行为和数据库操作

---

## 8. Console UI 前端工程

### 8.1 嵌入与服务机制

Console 前端源码在独立仓库 `heroiclabs/nakama-console`,构建后的静态文件通过 Go 1.16+ 的 `embed` 特性编译进二进制:

```go
// console/ui.go
//go:embed ui/dist/*
var embedFS embed.FS
```

启动时将 `index.html` 中的 `{{nt}}` 模板变量替换为配置值后缓存在内存中。HTTP 请求到达 7351 端口后,按以下优先级分发:

```
请求路径
  ├── /v2/console/*  → gRPC-Gateway (API 代理到内网 gRPC :7348)
  ├── /              → index.html (SPA 入口)
  ├── /static/*      → embed.FS 静态文件 (JS/CSS/字体 等,预 gzip)
  └── 其他路径        → index.html fallback (Vue Router 接管)
```

静态资源使用强缓存策略 (`Cache-Control: public, max-age=31536000, immutable`),JS/CSS 文件支持预压缩 `.gz` 版本直接返回。

### 8.2 前端构建产物

```
console/ui/dist/
├── index.html              # SPA 入口,含 {{nt}} 模板变量
├── index.html.gz           # 预压缩
├── favicon.ico
├── robots.txt
├── mockServiceWorker.js    # MSW Mock (开发/测试)
└── static/
    ├── index-C69ekwQb.js   # 主 JS Bundle (含 Vue + 所有组件)
    ├── index-DgkeZLvR.css  # 主 CSS Bundle
    ├── editor.worker-*.js  # Monaco Editor Web Worker
    ├── json.worker-*.js    # JSON 语法检查 Worker
    ├── inter-*.woff2       # Inter 字体 (多语言子集)
    ├── roboto-mono-*.woff2 # Roboto Mono 等宽代码字体
    ├── codicon.ttf         # VS Code 图标字体 (UI 图标)
    └── hiro_*.svg          # Hiro 插图 (base/rewards/stats/teams/...)
```

### 8.3 前端路由表

Nakama Console 是纯客户端路由的 SPA。Vue Router 4 根据 URL path 渲染对应页面组件。以下是基于 91 个 Console RPC 推断的完整路由和页面结构:

```
/login                          → 登录页
/logout                         → 登出页

/                               → 仪表盘首页 (Dashboard)

/accounts                       → 账户列表
/accounts/:id                   → 账户详情 (信息/设备/好友/组队/钱包/存储/日志)
/accounts/:id/notes             → 账户备注

/users                          → 控制台用户管理
/users/:id                      → 控制台用户详情

/groups                         → 群组列表
/groups/:id                     → 群组详情 (信息/成员管理)

/leaderboards                   → 排行榜列表
/leaderboards/:id               → 排行榜详情 (排名/记录)

/matches                        → 比赛列表 (活跃匹配)
/matches/:id                    → 比赛详情 (状态/参与者)

/channels                       → 频道消息浏览

/notifications                  → 通知列表

/storage                        → 存储对象浏览器
/storage/:collection            → 集合内容
/storage/:collection/:key       → 对象详情

/purchases                      → 内购记录
/subscriptions                  → 订阅记录

/api-explorer                   → API 调试器 (Monaco Editor + 请求构造)

/audit-log                      → 审计日志

/config                         → 服务端配置 (只读)

/status                         → 集群节点状态

/runtime                        → 运行时信息 (Lua/JS/Go 模块)

/settings                       → 系统设置

/extensions                     → 已注册扩展

/satori/templates               → Hiro/Satori 消息模板管理
```

### 8.4 页面-API 对应关系

每个页面/功能区域调用一组对应的 gRPC-Gateway API:

| 页面/区域 | API 端点 | gRPC RPC |
|-----------|---------|----------|
| **登录** | `POST /v2/console/authenticate` | `Authenticate` |
| | `POST /v2/console/authenticate/logout` | `AuthenticateLogout` |
| | `POST /v2/console/authenticate/mfa` | `AuthenticateMFASetup` |
| | `POST /v2/console/authenticate/password_change` | `AuthenticatePasswordChange` |
| **仪表盘** | `GET /v2/console/status` | `GetStatus` |
| | `GET /v2/console/config` | `GetConfig` |
| **账户管理** | `GET /v2/console/account` | `ListAccounts` |
| | `GET /v2/console/account/{id}` | `GetAccount` |
| | `POST /v2/console/account/{id}/ban` | `BanAccount` |
| | `POST /v2/console/account/{id}/unban` | `UnbanAccount` |
| | `DELETE /v2/console/account/{id}` | `DeleteAccount` |
| | `DELETE /v2/console/account` | `DeleteAccounts` |
| | `PUT /v2/console/account` | `UpdateAccount` |
| | `GET /v2/console/account/{id}/export` | `ExportAccount` |
| | `POST /v2/console/account/import` | `ImportAccount` |
| | `GET /v2/console/account/{id}/friend` | `GetFriends` |
| | `DELETE /v2/console/account/{id}/friend/{friend_id}` | `DeleteFriend` |
| | `GET /v2/console/account/{id}/group` | `GetGroups` |
| | `GET /v2/console/account/{account_id}/wallet` | `GetWalletLedger` |
| | `DELETE /v2/console/account/{id}/wallet/{wallet_id}` | `DeleteWalletLedger` |
| | `POST /v2/console/account/{id}/unlink/apple` | `UnlinkApple` |
| | `POST /v2/console/account/{id}/unlink/facebook` | `UnlinkFacebook` |
| | `POST /v2/console/account/{id}/unlink/google` | `UnlinkGoogle` |
| | `POST /v2/console/account/{id}/unlink/steam` | `UnlinkSteam` |
| | `POST /v2/console/account/{id}/unlink/gamecenter` | `UnlinkGameCenter` |
| | `POST /v2/console/account/{id}/unlink/custom` | `UnlinkCustom` |
| | `POST /v2/console/account/{id}/unlink/email` | `UnlinkEmail` |
| | `POST /v2/console/account/{id}/unlink/device` | `UnlinkDevice` |
| | `GET /v2/console/account/{account_id}/note` | `ListAccountNotes` |
| | `POST /v2/console/account/{account_id}/note` | `AddAccountNote` |
| | `DELETE /v2/console/account/{account_id}/note/{note_id}` | `DeleteAccountNote` |
| **用户管理** | `GET /v2/console/user` | `ListUsers` |
| | `GET /v2/console/user/{username}` | `GetUser` |
| | `POST /v2/console/user` | `AddUser` |
| | `PUT /v2/console/user` | `UpdateUser` |
| | `DELETE /v2/console/user` | `DeleteUser` |
| | `POST /v2/console/user/{username}/reset/password` | `ResetUserPassword` |
| | `POST /v2/console/user/{username}/mfa` | `RequireUserMfa` |
| | `POST /v2/console/user/{username}/mfa/reset` | `ResetUserMfa` |
| **群组管理** | `GET /v2/console/group` | `ListGroups` |
| | `GET /v2/console/group/{id}` | `GetGroup` |
| | `DELETE /v2/console/group/{id}` | `DeleteGroup` |
| | `PUT /v2/console/group` | `UpdateGroup` |
| | `GET /v2/console/group/{id}/member` | `GetMembers` |
| | `POST /v2/console/group/{group_id}/add` | `AddGroupUsers` |
| | `DELETE /v2/console/account/{id}/group/{group_id}` | `DeleteGroupUser` |
| | `POST /v2/console/group/{group_id}/account/{id}/promote` | `PromoteGroupMember` |
| | `POST /v2/console/group/{group_id}/account/{id}/demote` | `DemoteGroupMember` |
| | `GET /v2/console/group/{id}/export` | `ExportGroup` |
| **排行榜** | `GET /v2/console/leaderboard` | `ListLeaderboards` |
| | `GET /v2/console/leaderboard/{id}` | `GetLeaderboard` |
| | `GET /v2/console/leaderboard/{leaderboard_id}/records` | `ListLeaderboardRecords` |
| | `DELETE /v2/console/leaderboard/{id}` | `DeleteLeaderboard` |
| | `DELETE /v2/console/leaderboard/{id}/owner/{owner_id}` | `DeleteLeaderboardRecord` |
| **比赛** | `GET /v2/console/match` | `ListMatches` |
| | `GET /v2/console/match/{id}/state` | `GetMatchState` |
| **频道** | `GET /v2/console/channel` | `ListChannelMessages` |
| | `DELETE /v2/console/message` | `DeleteChannelMessages` |
| **通知** | `GET /v2/console/notification` | `ListNotifications` |
| | `GET /v2/console/notification/{id}` | `GetNotification` |
| | `POST /v2/console/notification` | `SendNotification` |
| | `DELETE /v2/console/notification/{id}` | `DeleteNotification` |
| **存储** | `GET /v2/console/storage/collections` | `ListStorageCollections` |
| | `GET /v2/console/storage` | `ListStorage` |
| | `GET /v2/console/storage/{collection}/{key}/{user_id}` | `GetStorage` |
| | `POST /v2/console/storage` | `WriteStorageObject` |
| | `DELETE /v2/console/storage` | `DeleteStorage` |
| | `DELETE /v2/console/storage/{collection}/{key}/{user_id}` | `DeleteStorageObject` |
| **内购/订阅** | `GET /v2/console/purchase` | `ListPurchases` |
| | `GET /v2/console/iap/purchase/{transaction_id}` | `GetPurchase` |
| | `GET /v2/console/subscription` | `ListSubscriptions` |
| | `GET /v2/console/iap/subscription/{original_transaction_id}` | `GetSubscription` |
| **API Explorer** | `GET /v2/console/api/endpoints` | `ListApiEndpoints` |
| | `POST /v2/console/api/endpoints/{method}` | `CallApiEndpoint` |
| | `POST /v2/console/api/endpoints/rpc/{method}` | `CallRpcEndpoint` |
| **审计日志** | `GET /v2/console/audit/log` | `ListAuditLogs` |
| | `GET /v2/console/audit/log/users` | `ListAuditLogsUsers` |
| **设置** | `GET /v2/console/setting` | `ListSettings` |
| | `GET /v2/console/setting/{name}` | `GetSetting` |
| | `PUT /v2/console/setting` | `UpdateSetting` |
| **运行时** | `GET /v2/console/runtime` | `GetRuntime` |
| **扩展** | `GET /v2/console/extensions` | `RegisteredExtensions` |
| **ACL 模板** | `GET /v2/console/acl/template` | `ListAclTemplates` |
| | `POST /v2/console/acl/template` | `AddAclTemplate` |
| | `PUT /v2/console/acl/template/{id}` | `UpdateAclTemplate` |
| | `DELETE /v2/console/acl/template/{id}` | `DeleteAclTemplate` |
| **Satori** | `GET /v2/console/satori/template` | `SatoriListTemplates` |
| | `POST /v2/console/satori/message` | `SatoriSendDirectMessage` |
| **危险操作** | `DELETE /v2/console/all` | `DeleteAllData` |

### 8.5 页面组件树与交互流程

#### 8.5.1 整体布局组件

```
App.vue
├── <router-view> (未登录时 → LoginPage)
│   (已登录后 → AuthenticatedLayout)
│
└── AuthenticatedLayout
    ├── SidebarNav                   # 左侧导航栏
    │   ├── DashboardLink            #   → /
    │   ├── AccountsLink             #   → /accounts
    │   ├── UsersLink                #   → /users (ACL 控制显示)
    │   ├── GroupsLink               #   → /groups
    │   ├── LeaderboardsLink         #   → /leaderboards
    │   ├── MatchesLink              #   → /matches
    │   ├── ChannelsLink             #   → /channels
    │   ├── NotificationsLink        #   → /notifications
    │   ├── StorageLink              #   → /storage
    │   ├── PurchasesLink            #   → /purchases
    │   ├── SubscriptionsLink        #   → /subscriptions
    │   ├── APIExplorerLink          #   → /api-explorer
    │   ├── AuditLogLink             #   → /audit-log (ACL 控制)
    │   ├── ConfigLink               #   → /config
    │   ├── StatusLink               #   → /status
    │   ├── RuntimeLink              #   → /runtime
    │   ├── SettingsLink             #   → /settings (ACL 控制)
    │   ├── ExtensionsLink           #   → /extensions
    │   └── SatoriLink               #   → /satori/templates (Hiro)
    ├── TopBar                       # 顶部栏
    │   ├── Breadcrumb               #   面包屑导航
    │   ├── SearchBar                #   全局搜索 (账户/用户)
    │   └── UserMenu                 #   当前用户头像/名称 + 登出入口
    └── <router-view>                # 页面内容区
```

#### 8.5.2 登录认证流程

```
浏览器访问 / → Vue Router 检查 token
  ├── 无 token → 重定向 /login → LoginPage
  │     LoginPage
  │     ├── UsernameInput
  │     ├── PasswordInput
  │     ├── MFAInput (条件渲染,登录响应返回 mfa_code 时)
  │     └── LoginButton
  │            │ 点击
  │            ▼
  │     POST /v2/console/authenticate
  │            │
  │     ┌──────┴──────────────────────────┐
  │     │ 响应无 mfa_code                  │ 响应含 mfa_code
  │     ▼                                 ▼
  │   存储 token → router.push("/")     显示 MFAInput → 再次 POST
  │                                           │
  │                                           ▼
  │                                     存储 token → router.push("/")
  │
  └── 有 token → 验证 token 有效性
       ├── 有效 → 渲染 AuthenticatedLayout → 加载对应页面
       └── 过期 → 清除 token → 重定向 /login
```

#### 8.5.3 列表页通用交互模式

大多数列表页面 (`/accounts`, `/users`, `/groups`, `/leaderboards`, `/matches`, 等) 遵循统一的数据流模式:

```
ListPage
├── SearchBar / FilterPanel      # 筛选条件输入
│   ├── SearchInput              #   关键词搜索
│   ├── FilterDropdown           #   状态/类型过滤
│   └── RefreshButton            #   手动刷新
├── DataTable / CardList         # 数据展示
│   ├── ColumnHeader (可排序)    #
│   ├── RowItem                  #   每行数据
│   │   └── ActionButtons        #   行操作 (查看/编辑/删除/封禁等)
│   └── Pagination               #   分页控件
└── DetailModal / Drawer         # 行点击 → 详情弹出 (或跳转详情页)
```

**数据流:**

```
1. 组件 mounted → GET /v2/console/{resource}?filter=...&cursor=...
2. 响应 → 更新 DataTable 数据 + 页码
3. 筛选条件变化 → 重新请求 (debounce 300ms)
4. 行操作 (如删除) → POST/DELETE 后刷新列表
5. 点击行 → router.push(`/{resource}/{id}`) 或打开 DetailModal
```

#### 8.5.4 详情页通用交互模式

详情页面 (`/accounts/:id`, `/groups/:id`, `/leaderboards/:id` 等) 使用 Tab 或 Section 分区展示:

```
DetailPage
├── BackButton                   # 返回列表
├── DetailHeader                 # 实体基本信息
│   ├── Avatar/Icon
│   ├── Title (用户名/群组名)
│   └── ActionBar                #   顶层操作 (编辑/封禁/导出/删除)
├── TabBar                       # 详情子区域切换
│   ├── OverviewTab               #   概览
│   ├── RelationsTab              #   关联数据 (好友/群组/成员)
│   ├── DataTab                   #   数据 (钱包/存储/购买)
│   └── LogTab                    #   日志 (审计/活动)
└── TabContent                   # 当前 Tab 内容
    └── 子组件 (对应各自 API)
```

**以账户详情页为例:**

```
/accounts/:id
  mounted → GET /v2/console/account/{id}
  ├── OverviewTab
  │   ├── AccountInfoCard        # 用户名/邮箱/地区/时区/语言
  │   ├── DeviceList             # 设备 ID 列表
  │   ├── LinkStatusPanel        # 社交登录绑定状态 (Apple/FB/Google/Steam/...)
  │   │   └── UnlinkButton (每个) # POST /v2/console/account/{id}/unlink/{provider}
  │   └── BanToggle              # POST /v2/console/account/{id}/ban|unban
  ├── FriendsTab
  │   └── GET /v2/console/account/{id}/friend
  ├── GroupsTab
  │   └── GET /v2/console/account/{id}/group
  ├── WalletTab
  │   ├── WalletSummary          # GET /v2/console/account/{account_id}/wallet
  │   └── LedgerList             # 钱包流水记录
  ├── StorageTab
  │   └── 嵌入 Storage 组件,按 user_id 过滤
  └── NotesTab
      ├── GET /v2/console/account/{account_id}/note
      ├── NoteList
      └── AddNoteForm            # POST /v2/console/account/{account_id}/note
```

#### 8.5.5 API Explorer 组件交互

API Explorer 是 Console 最复杂的交互组件之一,本质是一个内置的 API 调试客户端:

```
APIExplorerPage
├── EndpointSidebar
│   └── EndpointTree             # 从 GET /v2/console/api/endpoints 获取
│       ├── CategoryGroup        #   按功能分组 (Account/Match/Leaderboard/...)
│       └── EndpointItem         #   每个端点,点击选中
├── RequestPanel
│   ├── MethodBadge              #   GET/POST/PUT/DELETE
│   ├── URIDisplay               #   /v2/...
│   ├── HeadersEditor            #   Key-Value 编辑器 (自定义 HTTP 头)
│   ├── BodyEditor               #   Monaco Editor (JSON 编辑器)
│   │                            #   语法高亮 + 自动补全 + 格式化
│   └── SendButton               #   发送请求
│        │ 点击
│        ▼
│   POST /v2/console/api/endpoints/{method}
│   或 POST /v2/console/api/endpoints/rpc/{method}
│        │
│        ▼
└── ResponsePanel
    ├── StatusCode                #   HTTP 状态码
    ├── ResponseTime              #   耗时
    ├── ResponseBody              #   JSON 格式化输出 (Monaco Editor 只读)
    └── ResponseHeaders           #   响应头列表
```

#### 8.5.6 存储对象浏览器

```
StorageBrowserPage
├── CollectionSidebar
│   └── CollectionList           # GET /v2/console/storage/collections
│       └── CollectionItem       #   点击 → router.push(`/storage/${name}`)
├── ObjectTable
│   ├── CollectionBreadcrumb     #   当前集合路径
│   ├── SearchInput              #   按 key/user_id 筛选
│   ├── ObjectRow                #   每行: collection/key/user_id/version
│   │   └── ActionButtons        #   查看/编辑/删除
│   └── Pagination
└── ObjectEditor / ObjectViewer
    ├── MonacoEditor             #   JSON 编辑 (读/写模式)
    ├── ReadonlyBadge            #   只读模式 (有权限控制)
    └── SaveButton               #   POST /v2/console/storage
```

#### 8.5.7 审计日志

```
AuditLogPage
├── FilterBar
│   ├── UserFilter               # 按操作用户筛选 (GET /v2/console/audit/log/users)
│   ├── DateRangeFilter          # 时间范围
│   └── EventTypeFilter          # 事件类型 (Create/Update/Delete)
├── LogTable
│   ├── TimestampColumn
│   ├── UserColumn
│   ├── EventColumn              #   操作类型 (AddUser/BanAccount/UpdateSetting/...)
│   ├── TargetColumn             #   操作目标 (用户/群组/排行榜/...)
│   └── DetailColumn             #   变更详情 (JSON diff)
└── Pagination
```

### 8.6 前端状态管理

Console SPA 使用 Vue 3 Composition API 进行状态管理,不依赖 Vuex/Pinia 等外部库:

| 状态 | 范围 | 存储方式 | 说明 |
|------|------|---------|------|
| 当前用户会话 | 全局 | `sessionStorage` / `provide/inject` | JWT token + 用户信息 (username, email, ACL) |
| 认证状态 | 全局 | Composition API `reactive` | 是否已登录、token 过期时间 |
| 页面数据 | 页面级 | 组件 `ref/reactive` | 列表数据、详情数据,mounted 时请求 |
| 筛选条件 | 页面级 | `useRoute` query params | URL 参数持久化,支持浏览器前进/后退 |
| UI 状态 | 组件级 | 组件 `ref` | 加载中、错误、弹窗开关、表单输入 |
| API Explorer | 页面级 | `localStorage` | 请求历史、自定义请求体 (持久化) |

### 8.7 前端技术栈完整清单

| 层面 | 技术 | 版本/说明 |
|------|------|----------|
| 框架 | Vue 3 | Composition API (`<script setup>`) |
| 构建工具 | Vite | ESBuild + Rollup |
| 路由 | Vue Router 4 | `createRouter({ history: createWebHashHistory() })` |
| HTTP 客户端 | gRPC-Gateway (JSON) | `fetch` API 调用 `/v2/console/...` |
| UI 组件库 | Hiro (自研) | Heroic Labs 内部设计系统,含 Form/Table/Modal/Tab/Button/Input 等 |
| 代码编辑器 | Monaco Editor | VS Code 编辑器内核 (用于 API Explorer + Storage JSON 编辑) |
| 字体 | Inter + Roboto Mono | Google Fonts |
| 图标 | codicon | VS Code 图标集 (TTF 格式) |
| 国际化 | 无 (仅英文) | — |
| 测试 | MSW (Mock Service Worker) | `mockServiceWorker.js` 用于拦截 API 响应 |
| 样式 | 预编译 CSS | 随构建产物发布,无 CSS Modules/Tailwind |
| 嵌入方式 | Go `embed.FS` | 编译进 server 二进制,零外部依赖 |
