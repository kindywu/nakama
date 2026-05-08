# Nakama 数据库设计文档

## 1. 概述

Nakama 使用 **PostgreSQL** 或 **CockroachDB**(wire-compatible) 作为后端数据库。驱动层使用 `pgx/v5` 通过 `pgx/v5/stdlib` 兼容 `database/sql` 接口。系统启动时自动检测数据库类型(`SELECT version()`),据此调整事务重试策略。

### 1.1 数据库配置

```go
type DatabaseConfig struct {
    Addresses          []string  // 默认: ["root@localhost:26257"]
    ConnMaxLifetimeMs  int       // 默认: 3600000 (1小时)
    MaxOpenConns       int       // 默认: 100
    MaxIdleConns       int       // 默认: 100
    DnsScanIntervalSec int       // 默认: 60
}
```

- 最大连接数: 100 个连接,最大空闲连接数 100
- 连接最大生命周期: 1 小时,超时后自动回收
- DNS 重扫间隔: 60 秒,用于检测 CockroachDB 集群拓扑变化
- 连接字符串格式: `postgresql://user:pass@host:port/dbname?sslmode=disable`

### 1.2 事务管理

```mermaid
graph TB
    subgraph TransactionWrappers["事务封装"]
        ExecuteInTx["ExecuteInTx(db, fn)<br/>标准 *sql.Tx"]
        ExecuteInTxPgx["ExecuteInTxPgx(db, fn)<br/>pgx 原生 Tx"]
    end

    subgraph RetryLogic["重试逻辑"]
        PG["PostgreSQL 路径<br/>检测 40XXX SQLSTATE<br/>最多重试 5 次"]
        CRDB["CockroachDB 路径<br/>SAVEPOINT cockroach_restart<br/>检测 40001 / CR000"]
    end

    ExecuteInTx --> PG
    ExecuteInTx --> CRDB
    ExecuteInTxPgx --> PG
    ExecuteInTxPgx --> CRDB
```

**重试策略:**
- PostgreSQL: 捕获 `40XXX` SQLSTATE(序列化失败/死锁等),最多重试 5 次
- CockroachDB: 使用 `SAVEPOINT cockroach_restart` / `RELEASE SAVEPOINT` / `ROLLBACK TO SAVEPOINT` 模式,检测 `40001`(序列化失败)或 `CR000`(旧版)重试
- 非事务操作: 通过 `ExecuteRetryable(fn)` 重试序列化失败

---

## 2. 数据库错误处理

**文件:** `server/db_error.go`

| 错误类型 | 说明 |
|---------|------|
| `statusError` | 包装 gRPC 状态码,实现 `ErrorCauser` 接口用于 CockroachDB 重试 |
| `AmbiguousCommitError` | 事务提交状态未知 |
| `TxnRestartError` | 无法重启事务,包装重启错误和原始错误 |
| `ErrRowsAffectedCount` | 受影响行数不匹配的哨兵错误 |

PostgreSQL 错误码常量:

| 常量 | 错误码 | 说明 |
|------|--------|------|
| `dbErrorUniqueViolation` | 23505 | 唯一约束冲突 |
| `SerializationFailure` | 40001 | 序列化失败(可重试) |
| `InvalidCatalogName` | 3D000 | 数据库不存在 |

---

## 3. 迁移系统

**包:** `migrate/`,使用 [sql-migrate](https://github.com/heroiclabs/sql-migrate) 库。

### 3.1 迁移机制

```mermaid
graph LR
    subgraph MigrationFiles["迁移文件 (migrate/sql/)"]
        direction TB
        M1["20180103142001_initial_schema"]
        M2["20180805174141_tournaments"]
        M3["20200116134800_facebook_instant_games"]
        M4["20200615102232_apple"]
        M5["20201005180855_console"]
        M6["20210416090601_purchase"]
        M7["20220426122825_subscription"]
        M8["20221027145900_subscription_raw"]
        M9["20230222172540_purchases_not_null"]
        M10["20230706095800_leaderboard_index"]
        M11["20240715155039_leaderboard_rank"]
        M12["20240725115145_purchase_index"]
        M13["20240801100859_console_mfa"]
        M14["20250113112512_user_edge_metadata"]
        M15["20250703094700_console_settings"]
        M16["20250926112031_fine_grained_acl"]
        M17["20251015150737_user_notes_audit"]
        M18["20260319134532_display_name_index"]
    end

    M1 --> M2 --> M3 --> M4 --> M5 --> M6 --> M7 --> M8 --> M9
    M9 --> M10 --> M11 --> M12 --> M13 --> M14 --> M15 --> M16 --> M17 --> M18
```

- 迁移文件通过 `//go:embed sql/*` 嵌入二进制,命名格式: `YYYYMMDDHHmmSS-description.sql`
- 每个文件包含 `-- +migrate Up` 和 `-- +migrate Down` 两部分
- 迁移追踪表: `migration_info`(覆盖 `gorp_migrations` 默认名)
- 子命令: `up`, `down`, `redo`, `status`
- 支持 `limit` 参数进行部分迁移

---

## 4. 核心数据表

### 4.1 表关系总览

```mermaid
erDiagram
    users ||--o{ user_device : "has"
    users ||--o{ user_edge : "source"
    users ||--o{ notification : "receives"
    users ||--o{ storage : "owns"
    users ||--o{ message : "sends"
    users ||--o{ wallet_ledger : "has"
    users ||--o{ leaderboard_record : "has"
    users ||--o{ user_tombstone : "deleted"
    users ||--o{ purchase : "makes"
    users ||--o{ subscription : "subscribes"
    users ||--o{ users_notes : "noted"
    users }o--o{ user_edge : "destination"
    groups ||--o{ group_edge : "members"
    users }o--o{ group_edge : "member"
    leaderboard ||--o{ leaderboard_record : "contains"

    users {
        uuid id PK "主键"
        varchar username "唯一,128字符"
        varchar display_name "显示名"
        varchar email "唯一"
        bytea password "bcrypt哈希"
        jsonb metadata "用户元数据"
        jsonb wallet "虚拟钱包"
        timestamptz create_time "创建时间"
        timestamptz update_time "更新时间"
        timestamptz disable_time "禁用时间(软删除)"
    }

    storage {
        varchar collection PK "集合名"
        varchar key PK "键"
        uuid user_id PK "用户ID"
        jsonb value "存储值"
        varchar version "MD5版本哈希"
        smallint read "读权限"
        smallint write "写权限"
    }

    groups {
        uuid id PK "主键"
        uuid creator_id "创建者"
        varchar name "唯一,255字符"
        smallint state "状态(open/closed)"
        int edge_count "成员数"
        int max_count "最大成员数"
    }
```

### 4.2 users — 用户表

| 列名 | 类型 | 约束/默认值 | 说明 |
|------|------|-------------|------|
| id | UUID | NOT NULL | 主键 |
| username | VARCHAR(128) | NOT NULL, UNIQUE | 用户名 |
| display_name | VARCHAR(255) | NULL | 显示名称 |
| avatar_url | VARCHAR(512) | NULL | 头像 URL |
| lang_tag | VARCHAR(18) | NOT NULL, DEFAULT 'en' | 语言标签 |
| location | VARCHAR(255) | NULL | 地理位置 |
| timezone | VARCHAR(255) | NULL | 时区 |
| metadata | JSONB | NOT NULL, DEFAULT '{}' | 用户元数据 |
| wallet | JSONB | NOT NULL, DEFAULT '{}' | 虚拟钱包余额 |
| email | VARCHAR(255) | UNIQUE | 邮箱 |
| password | BYTEA | CHECK(length < 32000) | bcrypt 密码哈希 |
| facebook_id | VARCHAR(128) | UNIQUE | Facebook ID |
| google_id | VARCHAR(128) | UNIQUE | Google ID |
| gamecenter_id | VARCHAR(128) | UNIQUE | GameCenter ID |
| steam_id | VARCHAR(128) | UNIQUE | Steam ID |
| custom_id | VARCHAR(128) | UNIQUE | 自定义 ID |
| apple_id | VARCHAR(128) | UNIQUE | Apple ID |
| facebook_instant_game_id | VARCHAR(128) | UNIQUE | Facebook Instant Game ID |
| edge_count | INT | NOT NULL, DEFAULT 0, CHECK(>=0) | 好友数 |
| create_time | TIMESTAMPTZ | NOT NULL, DEFAULT now() | 创建时间 |
| update_time | TIMESTAMPTZ | NOT NULL, DEFAULT now() | 更新时间 |
| verify_time | TIMESTAMPTZ | NOT NULL, DEFAULT '1970-01-01' | 验证时间 |
| disable_time | TIMESTAMPTZ | NOT NULL, DEFAULT '1970-01-01' | 禁用时间(软删除) |

**索引:**
- `users_username_key` — UNIQUE(username)
- `users_email_key` — UNIQUE(email)
- 各社交 ID 的 UNIQUE 约束
- `users_display_name_idx` — GIN trigram 索引(display_name),用于全文搜索

**种子数据:** 系统用户 `id = '00000000-0000-0000-0000-000000000000'`, `username = ''`,不可删除。

### 4.3 user_device — 用户设备

| 列名 | 类型 | 说明 |
|------|------|------|
| id | VARCHAR(128) | 设备 ID |
| user_id | UUID | 关联用户,ON DELETE CASCADE |
| preferences | JSONB | 设备偏好设置 |
| push_token_amazon | VARCHAR(512) | Amazon 推送令牌 |
| push_token_android | VARCHAR(512) | Android 推送令牌 |
| push_token_huawei | VARCHAR(512) | 华为推送令牌 |
| push_token_ios | VARCHAR(512) | iOS 推送令牌 |
| push_token_web | VARCHAR(512) | Web 推送令牌 |

**约束:** PRIMARY KEY `(id)`, UNIQUE `(user_id, id)`, FOREIGN KEY `user_id → users(id)` CASCADE。

### 4.4 user_edge — 好友/屏蔽关系

| 列名 | 类型 | 说明 |
|------|------|------|
| source_id | UUID | 发起者,PART OF PK |
| position | BIGINT | 排序位置,PART OF PK |
| update_time | TIMESTAMPTZ | 更新时间 |
| destination_id | UUID | 目标用户 |
| state | SMALLINT | 状态: friend(0), invite_sent(1), invite_received(2), blocked(3) |
| metadata | JSONB | 好友元数据(v2025-01-13 新增) |

**约束:** PRIMARY KEY `(source_id, state, position)`, UNIQUE `(source_id, destination_id)`, CHECK: source_id 和 destination_id 不能为系统用户 UUID。

### 4.5 notification — 通知

| 列名 | 类型 | 说明 |
|------|------|------|
| id | UUID | 通知 ID,UNIQUE |
| user_id | UUID | 接收用户,PART OF PK |
| subject | VARCHAR(255) | 通知标题 |
| content | JSONB | 通知内容 |
| code | SMALLINT | 通知代码(负数=系统保留) |
| sender_id | UUID | 发送者 |
| create_time | TIMESTAMPTZ | 创建时间,PART OF PK |

**约束:** PRIMARY KEY `(user_id, create_time, id)`, UNIQUE(id), FOREIGN KEY `user_id → users(id)` CASCADE。

### 4.6 storage — 用户存储

| 列名 | 类型 | 说明 |
|------|------|------|
| collection | VARCHAR(128) | 集合名,PART OF PK |
| key | VARCHAR(128) | 键,PART OF PK |
| user_id | UUID | 用户 ID,PART OF PK |
| value | JSONB | 存储值 |
| version | VARCHAR(32) | MD5 哈希,乐观锁 |
| read | SMALLINT | 读权限(>=0),1=仅自己,2=公开 |
| write | SMALLINT | 写权限(>=0) |
| create_time | TIMESTAMPTZ | 创建时间 |
| update_time | TIMESTAMPTZ | 更新时间 |

**约束:** PRIMARY KEY `(collection, key, user_id)`, FOREIGN KEY `user_id → users(id)` CASCADE。

**索引:**
- `collection_read_user_id_key_idx`
- `collection_read_key_user_id_idx`
- `collection_user_id_read_key_idx`

### 4.7 message — 聊天/频道消息

| 列名 | 类型 | 说明 |
|------|------|------|
| id | UUID | 消息 ID,UNIQUE |
| code | SMALLINT | 消息类型: chat(0), update(1), remove(2), group_join(3) 等 |
| sender_id | UUID | 发送者 |
| username | VARCHAR(128) | 发送者用户名(冗余) |
| stream_mode | SMALLINT | 流模式,PART OF PK |
| stream_subject | UUID | 流主题,PART OF PK |
| stream_descriptor | UUID | 流描述符,PART OF PK |
| stream_label | VARCHAR(128) | 流标签,PART OF PK |
| content | JSONB | 消息内容 |
| create_time | TIMESTAMPTZ | 创建时间,PART OF PK |
| update_time | TIMESTAMPTZ | 更新时间 |

**约束:** PRIMARY KEY `(stream_mode, stream_subject, stream_descriptor, stream_label, create_time, id)`, UNIQUE(id), UNIQUE(sender_id, id)。

### 4.8 leaderboard — 排行榜

| 列名 | 类型 | 说明 |
|------|------|------|
| id | VARCHAR(128) | 排行榜 ID,PRIMARY KEY |
| authoritative | BOOLEAN | 是否权威排行榜 |
| sort_order | SMALLINT | 排序: asc(0), desc(1) |
| operator | SMALLINT | 操作符: best(0), set(1), increment(2), decrement(3) |
| reset_schedule | VARCHAR(64) | Cron 重置表达式 |
| metadata | JSONB | 元数据 |
| category | SMALLINT | 分类 |
| description | VARCHAR(255) | 描述 |
| duration | INT | 持续时间(秒),非零即为锦标赛 |
| title | VARCHAR(255) | 标题 |
| start_time | TIMESTAMPTZ | 开始时间 |
| end_time | TIMESTAMPTZ | 结束时间 |
| max_size | INT | 最大参与人数 |
| max_num_score | INT | 最大提交分数次数 |
| join_required | BOOLEAN | 是否需要加入(锦标赛) |
| enable_ranks | BOOLEAN | 是否启用排名(v2024-07-15) |
| size | INT | 当前参与人数 |

**索引:**
- `duration_start_time_end_time_category_idx`
- `leaderboard_create_time_id_idx`

### 4.9 leaderboard_record — 排行榜记录

| 列名 | 类型 | 说明 |
|------|------|------|
| leaderboard_id | VARCHAR(128) | 排行榜 ID,PART OF PK |
| expiry_time | TIMESTAMPTZ | 过期时间,PART OF PK |
| score | BIGINT | 分数,PART OF PK |
| subscore | BIGINT | 子分数,PART OF PK |
| owner_id | UUID | 用户 ID,PART OF PK |
| username | VARCHAR(128) | 用户名(冗余) |
| num_score | INT | 提交次数 |
| metadata | JSONB | 元数据 |
| create_time | TIMESTAMPTZ | 创建时间 |
| update_time | TIMESTAMPTZ | 更新时间 |
| max_num_score | INT | 最大提交次数(冗余) |

**约束:** PRIMARY KEY `(leaderboard_id, expiry_time, score, subscore, owner_id)`, UNIQUE `(owner_id, leaderboard_id, expiry_time)`, CHECK(score >= 0, subscore >= 0, num_score >= 0)。

### 4.10 wallet_ledger — 钱包账本

| 列名 | 类型 | 说明 |
|------|------|------|
| id | UUID | 记录 ID,UNIQUE |
| user_id | UUID | 用户 ID,PART OF PK |
| changeset | JSONB | 变更集 |
| metadata | JSONB | 元数据 |
| create_time | TIMESTAMPTZ | 创建时间,PART OF PK |
| update_time | TIMESTAMPTZ | 更新时间 |

**约束:** PRIMARY KEY `(user_id, create_time, id)`, FOREIGN KEY `user_id → users(id)` CASCADE。

### 4.11 user_tombstone — 删除用户追踪

| 列名 | 类型 | 说明 |
|------|------|------|
| user_id | UUID | 用户 ID,UNIQUE |
| create_time | TIMESTAMPTZ | 删除时间,PART OF PK |

**约束:** PRIMARY KEY `(create_time, user_id)`, UNIQUE(user_id)。用于追踪已删除用户,防止 ID 重用。

### 4.12 groups — 群组

| 列名 | 类型 | 说明 |
|------|------|------|
| id | UUID | 群组 ID,UNIQUE |
| creator_id | UUID | 创建者 |
| name | VARCHAR(255) | 群组名,UNIQUE |
| description | VARCHAR(255) | 描述 |
| avatar_url | VARCHAR(512) | 头像 |
| lang_tag | VARCHAR(18) | 语言标签,PART OF PK |
| metadata | JSONB | 元数据 |
| state | SMALLINT | 状态: open(0), closed(1) |
| edge_count | INT | 成员数,PART OF PK |
| max_count | INT | 最大成员数 |
| create_time | TIMESTAMPTZ | 创建时间 |
| update_time | TIMESTAMPTZ | 更新时间 |
| disable_time | TIMESTAMPTZ | 禁用时间(软删除),PART OF PK |

**约束:** PRIMARY KEY `(disable_time, lang_tag, edge_count, id)`, CHECK(state >= 0, edge_count >= 1 AND edge_count <= max_count)。

### 4.13 group_edge — 群组成员关系

| 列名 | 类型 | 说明 |
|------|------|------|
| source_id | UUID | 群组 ID,PART OF PK |
| state | SMALLINT | 角色: superadmin(0), admin(1), member(2), join_request(3), banned(4) |
| position | BIGINT | 排序位置 |
| destination_id | UUID | 成员用户 ID |
| update_time | TIMESTAMPTZ | 更新时间 |

**约束:** PRIMARY KEY `(source_id, state, position)`, UNIQUE `(source_id, destination_id)`。

### 4.14 purchase — 内购记录

| 列名 | 类型 | 说明 |
|------|------|------|
| transaction_id | VARCHAR(512) | 交易 ID,PRIMARY KEY |
| user_id | UUID | 用户 ID |
| store | SMALLINT | 商店: AppleAppStore(0), GooglePlay(1), Huawei(2) |
| environment | SMALLINT | 环境: Unknown(0), Sandbox(1), Production(2) |
| product_id | VARCHAR(512) | 商品 ID |
| purchase_time | TIMESTAMPTZ | 购买时间 |
| raw_response | JSONB | 原始响应 |
| refund_time | TIMESTAMPTZ | 退款时间 |
| create_time | TIMESTAMPTZ | 创建时间 |
| update_time | TIMESTAMPTZ | 更新时间 |

**约束:** PRIMARY KEY `(transaction_id)`, FOREIGN KEY `user_id → users(id)` ON DELETE SET DEFAULT(默认为系统用户 UUID)。

### 4.15 subscription — 订阅记录

| 列名 | 类型 | 说明 |
|------|------|------|
| original_transaction_id | VARCHAR(512) | 原始交易 ID,PRIMARY KEY |
| user_id | UUID | 用户 ID |
| store | SMALLINT | 商店 |
| environment | SMALLINT | 环境 |
| product_id | VARCHAR(512) | 商品 ID |
| purchase_time | TIMESTAMPTZ | 购买时间 |
| expire_time | TIMESTAMPTZ | 过期时间 |
| raw_response | JSONB | 原始响应 |
| raw_notification | JSONB | 原始通知 |
| refund_time | TIMESTAMPTZ | 退款时间 |
| create_time | TIMESTAMPTZ | 创建时间 |
| update_time | TIMESTAMPTZ | 更新时间 |

**约束:** PRIMARY KEY `(original_transaction_id)`, FOREIGN KEY `user_id → users(id)` ON DELETE SET DEFAULT。

### 4.16 console_user — 控制台管理员

| 列名 | 类型 | 说明 |
|------|------|------|
| id | UUID | 主键 |
| username | VARCHAR(128) | 用户名,UNIQUE |
| email | VARCHAR(255) | 邮箱,UNIQUE |
| password | BYTEA | bcrypt 密码哈希,CHECK(length < 32000) |
| metadata | JSONB | 元数据 |
| acl | JSONB | ACL 权限位图(v2025-09-26,替代 role 列) |
| mfa_secret | BYTEA | MFA 密钥(AES-256-GCM 加密) |
| mfa_recovery_codes | BYTEA | MFA 恢复码(AES-256-GCM 加密) |
| mfa_required | BOOLEAN | 是否需要 MFA |
| create_time | TIMESTAMPTZ | 创建时间 |
| update_time | TIMESTAMPTZ | 更新时间 |
| disable_time | TIMESTAMPTZ | 禁用时间(软删除) |

**历史:** `role` SMALLINT 列(admin/developer/maintainer/readonly)在 v2025-09-26 被 `acl` JSONB 替代。

### 4.17 console_audit_log — 审计日志

| 列名 | 类型 | 说明 |
|------|------|------|
| id | UUID | 记录 ID,UNIQUE |
| create_time | TIMESTAMPTZ | 操作时间,PART OF PK |
| console_user_id | UUID | 操作者 ID |
| console_username | TEXT | 操作者用户名,PART OF PK |
| email | TEXT | 操作者邮箱 |
| action | TEXT | 操作类型,PART OF PK |
| resource | TEXT | 资源,PART OF PK |
| message | TEXT | 描述 |
| metadata | JSONB | 附加元数据 |

**约束:** PRIMARY KEY `(create_time, console_username, action, resource, id)`。

### 4.18 console_acl_template — ACL 模板

| 列名 | 类型 | 说明 |
|------|------|------|
| id | UUID | 主键 |
| name | VARCHAR(64) | 模板名,UNIQUE,CHECK(length > 0) |
| description | VARCHAR(64) | 描述 |
| acl | JSONB | ACL 权限定义 |
| create_time | TIMESTAMPTZ | 创建时间 |
| update_time | TIMESTAMPTZ | 更新时间 |

### 4.19 users_notes — 用户备注

| 列名 | 类型 | 说明 |
|------|------|------|
| id | UUID | 记录 ID,UNIQUE |
| user_id | UUID | 用户 ID,PART OF PK |
| note | TEXT | 备注内容 |
| create_id | UUID | 创建者 ID |
| update_id | UUID | 更新者 ID |
| create_time | TIMESTAMPTZ | 创建时间,PART OF PK |
| update_time | TIMESTAMPTZ | 更新时间 |

**约束:** PRIMARY KEY `(user_id, create_time, id)`, FOREIGN KEY `user_id → users(id)` CASCADE。

### 4.20 setting — 控制台设置

| 列名 | 类型 | 说明 |
|------|------|------|
| name | VARCHAR(64) | 设置名,PRIMARY KEY,UNIQUE |
| value | JSONB | 设置值 |
| update_time | TIMESTAMPTZ | 更新时间 |

**种子数据:** `('utc_toggle', 'false')`。

---

## 5. 数据访问层架构

```mermaid
graph TB
    subgraph ConnectionPool["连接池层 (db.go)"]
        DbConnect["DbConnect()<br/>连接建立 + 连接池配置"]
        ExecuteInTx["ExecuteInTx()<br/>事务包装"]
        ExecuteInTxPgx["ExecuteInTxPgx()<br/>pgx 原生事务"]
    end

    subgraph CoreFunctions["核心数据函数 (core_*.go)"]
        Account["core_account.go<br/>账户 CRUD + 导出/导入"]
        Auth["core_authenticate.go<br/>认证逻辑"]
        User["core_user.go<br/>用户查询/封禁"]
        Friend["core_friend.go<br/>好友管理"]
        Group["core_group.go<br/>群组管理"]
        Leaderboard["core_leaderboard.go<br/>排行榜操作"]
        Tournament["core_tournament.go<br/>锦标赛操作"]
        Wallet["core_wallet.go<br/>钱包操作"]
        Storage["core_storage.go<br/>存储 CRUD"]
        Channel["core_channel.go<br/>频道消息"]
        Notification["core_notification.go<br/>通知管理"]
        Purchase["core_purchase.go<br/>内购验证"]
        Subscription["core_subscription.go<br/>订阅管理"]
        Multi["core_multi.go<br/>多表原子更新"]
    end

    subgraph CacheLayer["缓存层"]
        LBCache["LeaderboardCache<br/>排行榜元数据缓存"]
        LBRankCache["LeaderboardRankCache<br/>排名缓存"]
        SessionCache["SessionCache<br/>会话令牌缓存"]
        LoginCache["LoginAttemptCache<br/>登录尝试缓存"]
        StorageIdx["StorageIndex<br/>Bluge 全文搜索索引"]
    end

    DbConnect --> CoreFunctions
    CoreFunctions --> CacheLayer
```

### 5.1 数据访问特点

- **时间哨兵值:** `'1970-01-01 00:00:00 UTC'` 用作"未设置"的哨兵值,适用于 `verify_time`, `disable_time`, `end_time`, `expiry_time`, `refund_time` 等可选时间戳。
- **软删除:** users(disable_time), groups(disable_time), console_user(disable_time) 使用软删除。硬删除用户通过 user_tombstone 追踪。
- **游标分页:** 使用 base64 编码的 gob 序列化结构体,包含位置字段。
- **乐观锁:** storage 表使用 MD5 版本的 version 字段实现乐观锁,防止并发覆盖。
- **钱包更新:** 使用 pgx 原生事务的 JSONB 操作实现原子更新。

---

## 6. 外键关系总结

| 子表 | 外键 | 父表 | 删除策略 |
|------|------|------|---------|
| user_device | user_id | users(id) | CASCADE |
| user_edge | source_id | users(id) | CASCADE |
| user_edge | destination_id | users(id) | CASCADE |
| notification | user_id | users(id) | CASCADE |
| storage | user_id | users(id) | CASCADE |
| message | sender_id | users(id) | CASCADE |
| leaderboard_record | leaderboard_id | leaderboard(id) | CASCADE |
| wallet_ledger | user_id | users(id) | CASCADE |
| purchase | user_id | users(id) | SET DEFAULT(系统用户) |
| subscription | user_id | users(id) | SET DEFAULT(系统用户) |
| users_notes | user_id | users(id) | CASCADE |

---

## 7. 复合主键设计分析

Nakama 大量使用复合主键来优化查询模式:

| 表 | 主键 | 查询优化目标 |
|----|------|-------------|
| notification | (user_id, create_time, id) | 按用户时间序列列出通知 |
| message | (stream, create_time, id) | 按频道时间序列检索消息 |
| storage | (collection, key, user_id) | 按集合-键定位存储对象 |
| leaderboard_record | (leaderboard_id, expiry_time, score, subscore, owner_id) | 排行榜排名自然排序 |
| wallet_ledger | (user_id, create_time, id) | 按用户时间序列列出账本 |
| groups | (disable_time, lang_tag, edge_count, id) | 按语言/热度筛选群组 |
| group_edge | (source_id, state, position) | 按角色筛选/排序成员 |
| user_edge | (source_id, state, position) | 按状态筛选/排序好友 |
