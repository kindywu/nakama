# Nakama 客户端示例覆盖计划

本文档列出 Nakama 所有特性及其示例覆盖状态。已完成标记 `[DONE]`，未完成标记 `[TODO]`。

---

## 已完成示例

| # | 示例 | 特性 | 传输 | 客户端数 | 复杂度 |
|---|------|------|------|---------|--------|
| 1 | `ping-pong/` [DONE] | WebSocket 双层心跳 Ping/Pong + RTT 测量 | WS protobuf | 1 | ★☆☆☆☆ |
| 2 | `leaderboard/` [DONE] | 排行榜：提交分数、查询排名、ANSI 终端实时刷新 | HTTP REST | 10 | ★★☆☆☆ |
| 3 | `matchmaker/` [DONE] | 匹配器：加入匹配器、匹配成功、加入比赛、交换数据、Presence 事件 | WS protobuf | 10 | ★★★☆☆ |
| 4 | `tournament/` [DONE] | 锦标赛：RPC 创建、加入、提交分数、查询排名、双重编码 | HTTP REST + RPC | 12 | ★★★★☆ |
| 5 | `party/` [DONE] | 组队：创建/加入 Party、Coordinator 模式、Party Matchmaker、队内/全局通信、Solo 回退 | WS protobuf | 10 | ★★★★★ |

---

## 待实现示例 — HTTP REST API

### 6. Storage 存储 [TODO]

**对应 API:** `api_storage.go`
**复杂度:** ★★☆☆☆
**客户端数:** 1
**传输:** HTTP REST

#### 测试重点

Nakama 的基础持久化 API。单个客户端演示存储对象的完整 CRUD 生命周期。

#### 客户端生命周期

1. 设备认证 → 获取 token
2. **写入** (`POST /v2/storage`) — 写入 2 条不同 collection 的对象（如 `"settings"` 和 `"inventory"`），附带 JSON value 和 version `"*"`（无乐观锁）
3. **列表** (`GET /v2/storage`) — 按 collection 筛选，验证返回的对象列表；按 user_id 筛选确认仅返回自己的数据
4. **更新** (`PUT /v2/storage`) — 修改一条对象的值，带上之前的 `version` 做乐观锁校验
5. **删除** (`DELETE /v2/storage`) — 删除一条对象
6. 再次列表验证删除结果

#### 覆盖的 API

| 方法 | 端点 | 关键点 |
|------|------|--------|
| POST | `/v2/storage` | collection, key, value(JSON), version(`"*"` 表示无锁写入) |
| GET | `/v2/storage` | 按 collection 筛选，分页 cursor |
| PUT | `/v2/storage` | version 乐观锁，版本冲突时返回错误 |
| DELETE | `/v2/storage` | 按 collection/key/version 删除

#### 注意事项

- `value` 字段在 HTTP body 中是 JSON 字符串（类似 RPC payload），需要 `json.Marshal(string(valueJSON))` 双重编码
- version 用于乐观并发控制：写入时传 `"*"` 表示不检查，更新时传返回的版本号

### 7. Friends 好友 [TODO]

**对应 API:** `api_friend.go`
**复杂度:** ★★☆☆☆
**客户端数:** 4
**传输:** HTTP REST

#### 测试重点

好友系统的核心操作：添加、列表、删除、屏蔽。使用 4 个客户端模拟社交关系。

#### 客户端生命周期

- **A** (中心玩家): 设备认证 → 设置用户名
- **B, C, D** (其他玩家): 各自认证 → 设置用户名

1. **添加好友** — A 向 B、C 发送好友请求 (`POST /v2/friend`)，需要 B、C 的 user_id
2. **列出好友** — A 调用 `GET /v2/friend`，验证返回 B、C；B 调用确认 A 在自己好友列表中
3. **屏蔽** — A 屏蔽 D (`POST /v2/friend/block`)，验证 D 出现在屏蔽列表中
4. **解除屏蔽** — A 解除屏蔽 D (`DELETE /v2/friend/block`)
5. **删除好友** — A 删除 B (`DELETE /v2/friend`)，再次列表验证只剩 C

#### 覆盖的 API

| 方法 | 端点 | 关键点 |
|------|------|--------|
| POST | `/v2/friend` | 通过 user_id 或 username 添加好友 |
| GET | `/v2/friend` | 返回 friends 列表，含 user_id、username、state |
| DELETE | `/v2/friend` | 按 user_id 删除好友 |
| POST | `/v2/friend/block` | 屏蔽用户 |
| DELETE | `/v2/friend/block` | 解除屏蔽 |

#### 注意事项

- 需要获取其他用户的 user_id，可通过 `GET /v2/user` 查询或认证响应中的信息
- 添加好友是单向还是双向取决于服务端配置（默认需要对方接受）
- state 字段表示好友关系状态（0=pending, 1=accepted, 2=blocked）

### 8. Groups 群组/公会 [TODO]

**对应 API:** `api_group.go`
**复杂度:** ★★★☆☆
**客户端数:** 4
**传输:** HTTP REST

#### 测试重点

公会系统的完整生命周期：创建、加入、成员管理、权限操作、离开、删除。

#### 客户端生命周期

- **Owner** (会长): 设备认证 → 设置用户名
- **M1, M2, M3** (成员): 各自认证 → 设置用户名

1. **创建群组** — Owner 调用 `POST /v2/group` 创建公会（名称、描述、open=true 允许自由加入）
2. **列出群组** — 任意客户端 `GET /v2/group` 按名称搜索，验证公会存在
3. **成员加入** — M1、M2 调用 `POST /v2/group/{id}/join` 加入
4. **列出成员** — `GET /v2/group/{id}/user` 查看成员列表，验证 Owner、M1、M2
5. **权限管理** — Owner 提升 M1 为 admin (`POST .../promote`)，踢出 M2 (`POST .../kick`)
6. **邀请** — Owner 邀请 M3 加入 (`POST .../add`)
7. **离开** — M1 调用 `POST .../leave` 离开公会
8. **删除** — Owner 调用 `DELETE /v2/group/{id}` 解散公会

#### 覆盖的 API

| 方法 | 端点 | 关键点 |
|------|------|--------|
| POST | `/v2/group` | name, description, open, avatar_url, lang_tag |
| GET | `/v2/group` | 按 name 搜索，分页 cursor |
| DELETE | `/v2/group/{id}` | 仅 superadmin 或 owner |
| POST | `/v2/group/{id}/join` | open=true 时直接加入 |
| POST | `/v2/group/{id}/leave` | 普通成员离开 |
| GET | `/v2/group/{id}/user` | 成员列表，含 state 角色 |
| POST | `/v2/group/{id}/add` | 邀请指定 user_id |
| POST | `/v2/group/{id}/kick` | 踢出成员 |
| POST | `/v2/group/{id}/promote` / `demote` | 修改成员角色 |

#### 注意事项

- group state: 0=superadmin, 1=admin, 2=member, 3=join request
- open=false 时需要 owner 批准加入请求

### 9. Notifications 通知 [TODO]

**对应 API:** `api_notification.go`
**复杂度:** ★★☆☆☆
**客户端数:** 2
**传输:** HTTP REST

#### 测试重点

服务器通知的发送、接收、列表和删除。演示客户端间通过服务端通知通信。

#### 客户端生命周期

- **A** (发送者): 设备认证
- **B** (接收者): 设备认证

1. **发送通知** — A 调用 `POST /v2/notification` 向 B 发送通知，body 包含 `user_ids: [B的id]`, `subject`, `content`, `code`（自定义通知码）
2. **列出通知** — B 调用 `GET /v2/notification`，验证收到的通知内容，确认 `subject`、`content`、`sender_id` 正确
3. **删除通知** — B 调用 `DELETE /v2/notification` 按 `ids` 删除已读通知
4. 再次列表确认删除

#### 覆盖的 API

| 方法 | 端点 | 关键点 |
|------|------|--------|
| POST | `/v2/notification` | user_ids(数组), subject, content, code, persistent |
| GET | `/v2/notification` | 分页 cursor, 返回通知列表含 create_time |
| DELETE | `/v2/notification` | 按 id 数组批量删除 |

#### 注意事项

- `code` 是整型自定义通知码，客户端据此区分通知类型（如 1=好友请求, 2=公会邀请）
- `persistent: true` 表示离线时也保存通知，上线后可拉取
- 通知是服务端间通信（A→服务器→B），A 和 B 无需同时在线

### 10. Chat/Channel 消息 (HTTP) [TODO]

**对应 API:** `api_channel.go`
**复杂度:** ★★☆☆☆
**客户端数:** 1
**传输:** HTTP REST

#### 测试重点

通过 HTTP REST 获取频道历史消息。通常与 WebSocket Channels 示例配合使用（先通过 WS 发送消息，再通过 HTTP 拉取历史）。

#### 客户端生命周期

1. 设备认证 → 获取 token
2. **前提:** 假设频道中已有消息（由其他客户端或 WS 发送）
3. **获取历史** — `GET /v2/channel/{room_name}/message`，指定 `limit` 和 `cursor`
4. 验证返回的消息列表：`sender_id`、`content`、`create_time`
5. **分页** — 使用上一页的 `next_cursor` 继续拉取

#### 覆盖的 API

| 方法 | 端点 | 关键点 |
|------|------|--------|
| GET | `/v2/channel/{room_name}/message` | limit, cursor 分页, forward 参数 |

#### 注意事项

- 频道名称格式如 `"room:lobby"`, `"group:{group_id}"`, `"direct:{user1}-{user2}"`
- 此端点仅支持 REST，实时收发消息必须通过 WebSocket Channel
- 建议与 #17 Channels (WebSocket) 示例配合：先 WS 发消息，再 HTTP 拉取验证

### 11. Account Linking 账号关联 [TODO]

**对应 API:** `api_link.go` / `api_unlink.go`
**复杂度:** ★★☆☆☆
**客户端数:** 1
**传输:** HTTP REST

#### 测试重点

游客转正式用户的典型流程：Device 认证 → 关联 Email → 使用 Email 重新登录 → 解除关联。

#### 客户端生命周期

1. **Device 认证** — 设备和之前一样，获取 token
2. **关联 Email** — 调用 `POST /v2/account/link/email`，传入 `email` + `password`，将 Email 认证方式绑定到当前账号
3. **验证关联** — 调用 `GET /v2/account` 查看 devices 和 email 字段，确认两个认证方式都在
4. **Email 认证** — 用相同 email+password 调用 `POST /v2/account/authenticate/email` 登录，获取新 token
5. **验证身份一致** — 用新 token `GET /v2/account`，确认 user_id 和 username 与之前一致
6. **解除关联** — 调用 `POST /v2/account/unlink/email` 移除 email 关联
7. 再次 `GET /v2/account` 验证 email 已移除

#### 覆盖的 API

| 方法 | 端点 | 关键点 |
|------|------|--------|
| POST | `/v2/account/link/email` | email + password |
| POST | `/v2/account/unlink/email` | 解除后该 email 可重新注册 |
| GET | `/v2/account` | 返回关联的 devices, email, facebook 等 |

#### 注意事项

- link 需要当前有效 token（已认证状态下调用）
- unlink 后 device 认证仍然有效，不会丢失账号
- 也可演示 link Google/Facebook 等社交账号，需 OAuth token

### 12. Other Authentication 其他认证方式 [TODO]

**对应 API:** `api_authenticate.go`
**复杂度:** ★★☆☆☆
**客户端数:** 1
**传输:** HTTP REST

#### 测试重点

演示 Email 认证的完整流程：注册（首次认证即注册）、登录、密码变更。仅覆盖 Email，社交登录（Google/Facebook 等）需要 OAuth 环境，作为可选扩展。

#### 客户端生命周期

1. **Email 注册** — `POST /v2/account/authenticate/email`，传入 `email` + `password` + `create`。Nakama 中首次认证即创建用户（无需单独注册端点）
2. **验证** — 返回 token，`GET /v2/account` 确认 email 和 user_id
3. **Email 登录** — 再次调用同一端点（无 `create` 参数或指定不同参数），传入相同 email+password，获取新 token
4. **修改密码** — `PUT /v2/account` 或专用端点更新密码（如适用）

#### 覆盖的 API

| 方法 | 端点 | 关键点 |
|------|------|--------|
| POST | `/v2/account/authenticate/email` | email, password, create(bool), username(可选) |
| GET | `/v2/account` | 验证认证方式绑定 |

#### 注意事项

- Email 认证使用 HTTP Basic Auth（serverKey:）与服务端握手
- 首次认证时 `create` 参数控制是否创建新用户（默认 false 则需要先有设备认证并关联）
- 社交登录需要有效的 OAuth token 从对应平台获取（不在本示例范围内）

### 13. Match Listing (HTTP) [TODO]

**对应 API:** `api_match.go`
**复杂度:** ★★☆☆☆
**客户端数:** 1 (配合已有 matchmaker 示例)
**传输:** HTTP REST

#### 测试重点

通过 HTTP REST 列出服务器当前活跃的比赛。需先通过 matchmaker 创建比赛，再通过 REST 接口查询。

#### 客户端生命周期

1. 设备认证 → 获取 token
2. **前提:** 确保服务器上有活跃比赛（运行 matchmaker 示例或其他方式创建比赛）
3. **列出比赛** — `GET /v2/match`，可带 `limit`、`label`（标签过滤）、`authoritative`（只查权威匹配）参数
4. 验证返回列表包含 match_id、size（当前人数）、authoritative 标识
5. **标签过滤** — 使用 `label` 参数筛选特定类型的比赛

#### 覆盖的 API

| 方法 | 端点 | 关键点 |
|------|------|--------|
| GET | `/v2/match` | limit, authoritative, label, node 过滤 |

#### 注意事项

- 仅返回活跃比赛（已结束或已满的比赛不会出现在列表中）
- 与 matchmaker 示例结合使用：先运行 matchmaker 创建比赛，再运行此示例查看列表
- `label` 可在创建比赛或 matchmaker 匹配时设置

### 14. IAP 内购验证 [TODO]

**对应 API:** `api_purchase.go`
**复杂度:** ★★★☆☆
**客户端数:** 1
**传输:** HTTP REST

#### 测试重点

演示 IAP 收据验证的流程。由于需要真实商店收据，示例使用模拟数据演示 API 调用模式，重点展示请求结构和错误响应处理。

#### 客户端生命周期

1. 设备认证 → 获取 token
2. **Google 验证** — `POST /v2/iap/purchase/google`，传入 `purchase`（收据字符串）和 `persist` 参数
3. 验证服务端返回：成功时包含 validated 状态，失败时收据被拒绝
4. **Apple 验证** — `POST /v2/iap/purchase/apple`，传入 `receipt`（Base64 编码收据）
5. 验证服务端返回结果

#### 覆盖的 API

| 方法 | 端点 | 关键点 |
|------|------|--------|
| POST | `/v2/iap/purchase/google` | purchase(string), persist(bool) |
| POST | `/v2/iap/purchase/apple` | receipt(base64), persist(bool) |
| POST | `/v2/iap/purchase/huawei` | purchase, signature, persist |

#### 注意事项

- 需要 Nakama 配置 IAP 相关的 API key/secret 才能返回验证成功（否则收据被拒绝）
- 示例主要演示 API 调用模式，实际验证结果取决于配置
- `persist` 参数控制是否持久化验证记录

### 15. Subscriptions 订阅 [TODO]

**对应 API:** `api_subscription.go`
**复杂度:** ★★★☆☆
**客户端数:** 1
**传输:** HTTP REST

#### 测试重点

订阅验证流程。与 IAP 类似，依赖商店配置，示例重点演示 API 调用和请求结构。

#### 客户端生命周期

1. 设备认证 → 获取 token
2. **Google 订阅** — `POST /v2/iap/subscription/google`，传入 `purchase`（订阅 token）和 `persist`
3. **Apple 订阅** — `POST /v2/iap/subscription/apple`，传入 `receipt`（Base64 编码收据）
4. 分别验证返回的结构化错误信息

#### 覆盖的 API

| 方法 | 端点 | 关键点 |
|------|------|--------|
| POST | `/v2/iap/subscription/google` | purchase(string), persist(bool) |
| POST | `/v2/iap/subscription/apple` | receipt(base64), persist(bool) |

#### 注意事项

- 需要 Nakama 配置对应商店的 API 密钥
- 与 #14 IAP 示例分离，因订阅和一次性购买的业务流程不同
- 订阅验证返回的 subscription 信息包含到期时间

### 16. Sessions 会话管理 [TODO]

**对应 API:** `api_session.go`
**复杂度:** ★★☆☆☆
**客户端数:** 1 (模拟多端)
**传输:** HTTP REST

#### 测试重点

会话管理：多端登录、token 刷新、session 吊销。单个用户从两个设备登录，演示 session 控制。

#### 客户端生命周期

1. **模拟双端登录** — 同一 device_id 两次认证（或不同 device_id 关联同一用户），获得 token1 和 token2
2. **列出 session** — 用 token1 调用 `GET /v2/session`，验证返回两个活跃 session
3. **刷新 token** — 用 token1 调用 `POST /v2/session/refresh`，获得新 token1_new，旧 token1 失效
4. **吊销 session** — 用 token2 调用 `POST /v2/session/logout`，token2 失效
5. 再次列出 session，验证仅剩 token1_new 对应的一个 session

#### 覆盖的 API

| 方法 | 端点 | 关键点 |
|------|------|--------|
| GET | `/v2/session` | 返回所有活跃 session（含设备信息、IP、创建时间） |
| POST | `/v2/session/refresh` | 返回新 token，旧 token 立即失效 |
| POST | `/v2/session/logout` | 吊销当前 session 的 token |

#### 注意事项

- session 信息包含 device_id、ip_address、create_time，方便用户管理多端登录
- refresh 后需用新 token，旧 token 的后续请求会被拒绝
- 与 logout 相关的 API（`/v2/session/logout` 只登出当前 session；另有 `/v2/session/logout/{type}` 可批量登出）

---

## 待实现示例 — WebSocket 实时管道

### 17. Channels 频道 [TODO]

**对应 Pipeline:** `pipeline_channel.go`
**复杂度:** ★★★☆☆
**客户端数:** 5
**传输:** WebSocket protobuf

#### 测试重点

Nakama 最核心的实时通信功能。5 个客户端加入同一聊天频道，收发消息，监听成员进出。

#### 客户端生命周期

1. 5 个客户端各自：设备认证 → WebSocket 连接 → go readLoop
2. **加入频道** — 所有客户端发送 `Envelope_ChannelJoin`，target 为 `"room:lobby"`，persistence=true（消息持久化）
3. **接收 Presence** — 每个客户端收到 `Envelope_ChannelPresenceEvent`，确认当前频道成员列表（joins 为已在线成员）
4. **发送消息** — Client 0 发送 `Envelope_ChannelMessageSend`，content 为 `"Hello from C0"`
5. **接收消息** — 其他 4 个客户端收到 `Envelope_ChannelMessage`，验证 content 和 sender 的 username
6. **多人发言** — Client 1-4 轮流发送消息，验证所有人在同一频道
7. **离开频道** — Client 4 发送 `Envelope_ChannelLeave`，其他客户端收到 `Envelope_ChannelPresenceEvent`（leaves）
8. 剩余客户端确认消息收发仍然正常

#### 覆盖的 Envelope 类型

| 发送 | 接收 | 说明 |
|------|------|------|
| `Envelope_ChannelJoin` | - | target（频道名）, type（1=room, 2=direct, 3=group）, persistence, hidden |
| `Envelope_ChannelLeave` | - | channel_id, target |
| `Envelope_ChannelMessageSend` | - | channel_id, content(string) |
| - | `Envelope_ChannelMessage` | channel_id, message_id, content, sender(Presence), create_time, persistent |
| - | `Envelope_ChannelPresenceEvent` | channel_id, joins, leaves（频道成员上下线） |

#### 注意事项

- Channel type: 1=room（房间）, 2=direct（私聊）, 3=group（群组频道）
- `persistence: true` 表示消息存入数据库，可通过 HTTP REST 拉取历史
- `hidden: true` 表示不在成员列表显示（静默加入）
- readLoop 模式复用 matchmaker/party 示例的读协程+channel 事件分发模式

### 18. Status 状态 [TODO]

**对应 Pipeline:** `pipeline_status.go`
**复杂度:** ★★☆☆☆
**客户端数:** 3
**传输:** WebSocket protobuf

#### 测试重点

在线状态系统：关注/取关用户、状态更新、Presence 事件推送。演示好友在线状态通知的底层机制。

#### 客户端生命周期

- **A, B, C** 全部连接 WebSocket（connect 参数 `status=true`，收到初始 `StatusPresenceEvent`）

1. **关注用户** — A 发送 `Envelope_StatusFollow`，target 为 B 的 user_id。A 收到 `Envelope_Status` 确认关注关系，同时收到 B 的当前在线 `StatusPresenceEvent`
2. **更新状态** — B 发送 `Envelope_StatusUpdate`，status 为 `"In a match"`。A 收到 `Envelope_StatusPresenceEvent`（B 的状态变更）
3. **多用户关注** — C 也关注 B
4. **B 离线** — B 断开 WebSocket。A 和 C 都收到 `Envelope_StatusPresenceEvent`（leaves = [B]）
5. **取关** — A 发送 `Envelope_StatusUnfollow`，target 为 B 的 user_id。之后 B 上线时 A 不再收到通知
6. **验证独立性** — B 重新连接，仅 C 收到 Presence 通知

#### 覆盖的 Envelope 类型

| 发送 | 接收 | 说明 |
|------|------|------|
| `Envelope_StatusFollow` | - | user_ids(数组), usernames(数组) |
| `Envelope_StatusUnfollow` | - | user_ids |
| `Envelope_StatusUpdate` | - | status(string) — 自定义状态信息 |
| - | `Envelope_Status` | 确认关注关系 |
| - | `Envelope_StatusPresenceEvent` | joins, leaves（被关注者的上下线+状态变更） |

#### 注意事项

- connect 时需 `status=true` 参数，否则不接收 Presence 事件
- `StatusFollow` 可以同时关注多个 user_id
- `StatusUpdate` 的 status 是自由字符串（如 `"In menu"`, `"In match"`, `"AFK"`）
- 与 Channels Presence 不同：Status 是跨频道的全局关注关系

### 19. RPC via WebSocket [TODO]

**对应 Pipeline:** `pipeline_rpc.go`
**复杂度:** ★★☆☆☆
**客户端数:** 1
**传输:** WebSocket protobuf
**前置条件:** `data/modules/` 下有注册了 RPC 函数的 Lua 模块

#### 测试重点

通过已建立的 WebSocket 连接调用服务端 RPC，对比 HTTP RPC（tournament 示例）的区别：无需双重编码，payload 直接在 Envelope 中。

#### 客户端生命周期

1. 设备认证 → WebSocket 连接 → go readLoop
2. **发送 RPC** — 构造 `Envelope_Rpc`，包含 `id`（RPC 函数名，如 `"clientrpc.hello"`）和 `payload`（任意字符串，服务端 RPC 函数解析）
3. **接收响应** — 服务端返回 `Envelope_Rpc` 或 `Envelope_Error`，payload 包含 RPC 函数返回值
4. **对比 HTTP RPC** — 可选：用相同参数调用 HTTP `/v2/rpc/{id}`，验证两者返回相同结果

#### 覆盖的 Envelope 类型

| 发送 | 接收 | 说明 |
|------|------|------|
| `Envelope_Rpc` | - | id(string, RPC 函数名), payload(string, 传给 RPC 函数的参数) |
| - | `Envelope_Rpc` | id(cid), payload(RPC 函数返回的 JSON string) |

#### 注意事项

- 与 HTTP RPC 的关键区别：payload 无需双重编码（HTTP 的 gRPC-Gateway 映射要求 body 是 JSON string；WS 是 protobuf oneof，直接传 string）
- 需要先在服务端注册 RPC 函数（Lua/Go/JS），复用 tournament 示例的 Lua 模块即可
- `Envelope.Cid` 用于匹配请求-响应（类似 ping-pong 示例）

---

## 待实现示例 — 服务端运行时

### 20. Authoritative Match 权威匹配 [TODO]

**对应 Pipeline:** `pipeline_match.go` + Lua match handler
**复杂度:** ★★★★★
**客户端数:** 2
**传输:** WebSocket protobuf
**前置条件:** `data/modules/match.lua` 注册了 match handler

#### 测试重点

服务端控制的权威比赛（与 matchmaker 创建的 relayed match 对比）。服务端 Lua 代码接收客户端消息、处理游戏逻辑、广播结果。重点演示客户端如何通过 `MatchCreate` 创建权威比赛，以及 match handler 的消息处理循环。

#### 客户端生命周期

1. **两个客户端** 各自：设备认证 → WebSocket 连接 → go readLoop
2. **Client 1 创建比赛** — 发送 `Envelope_MatchCreate`，可附带自定义 `label`, `version`, `name`。服务端 match handler 的 `match_init` 被调用
3. **Client 1 收到 Match** — 服务端返回 `Envelope_Match`，match_id 由 handler 生成
4. **Client 2 加入比赛** — 使用 match_id 发送 `Envelope_MatchJoin`
5. **Client 2 收到 Match** — 服务端返回 `Envelope_Match`，match handler 的 `match_join_attempt` 和 `match_join` 被触发
6. **数据交换** — Client 1 发送 `Envelope_MatchDataSend`（opCode=1, data="move"）
   - 服务端 match handler 的 `match_loop` 收到消息
   - handler 处理（如验证、修改状态）
   - handler 通过 `nk.match_broadcast()` 广播给所有玩家
7. **接收广播** — Client 2 收到 `Envelope_MatchData`（opCode 和数据由 handler 控制）
8. **离开比赛** — Client 1 发送 `Envelope_MatchLeave`，handler 的 `match_leave` 被触发

#### 覆盖的 Envelope 类型

| 发送 | 接收 | 说明 |
|------|------|------|
| `Envelope_MatchCreate` | - | 创建权威比赛。label, version, name |
| `Envelope_MatchJoin` | - | match_id 或 token |
| `Envelope_MatchLeave` | - | match_id |
| `Envelope_MatchDataSend` | - | match_id, opCode, data([]byte) |
| - | `Envelope_Match` | match_id, presences, self(index) |
| - | `Envelope_MatchData` | match_id, opCode, data, presence(发送者) |
| - | `Envelope_MatchPresenceEvent` | joins, leaves |

#### 与 relayed match (matchmaker 示例) 的关键区别

| 维度 | Relayed Match | Authoritative Match |
|------|-------------|-------------------|
| 创建方式 | matchmaker 自动创建 | 客户端 `MatchCreate` 主动创建 |
| 消息处理 | 服务端直接转发 | handler 拦截、验证、处理后再广播 |
| 游戏逻辑 | 客户端自行处理 | 服务端权威处理（防作弊） |
| opCode 含义 | 客户端自定义 | handler 定义协议 |

#### 注意事项

- 需要服务端注册 match handler（Lua: `nk.register_match_handler()`，Go: plugin）
- match handler 有完整的生命周期：`match_init` → `match_join_attempt` → `match_join` → `match_loop` → `match_leave` → `match_terminate`
- `match_loop` 是核心：在协程中运行，通过 channel 接收消息、ticker 驱动定时逻辑
- 建议配合 `data/modules/match.lua`（或新建）演示

### 21. Before/After Hooks 钩子函数 [TODO]

**对应:** Runtime hooks
**复杂度:** ★★☆☆☆
**客户端数:** 1
**传输:** HTTP REST
**前置条件:** Lua 模块注册了 hook 函数

#### 测试重点

演示服务端 runtime hooks 如何拦截和影响客户端请求。通过几个典型 hook 展示请求拦截、修改、审计日志等功能入口。此示例偏"观察"性质，客户端执行标准 API 调用，观察服务端 hooks 的副作用。

#### 客户端生命周期

1. **前置:** 服务端 Lua 已注册以下 hooks:
   - `before_authenticate_device` — 检查 device_id 是否在白名单（模拟），不在则拒绝
   - `after_authenticate_device` — 打印认证成功日志
   - `before_write_leaderboard_record` — 检查分数是否合法（如 > 1000000 拒绝）

2. **认证被拒绝** — 用黑名单 device_id 认证，服务端返回 error（`before_authenticate_device` 拒绝）。客户端处理 `401` / error
3. **认证成功** — 用合法 device_id 认证，成功。服务端日志输出（`after_authenticate_device`）
4. **排行榜防作弊** — 提交超大分数（如 99999999），服务端返回 validation error（`before_write_leaderboard_record` 拒绝）
5. **正常提交** — 提交正常分数，成功。验证 hook 不影响合法操作

#### 覆盖的 API / Hook

| 触发条件 | Hook 名称 | 能力 |
|---------|-----------|------|
| `POST /v2/account/authenticate/device` | `before_authenticate_device` | 拒绝请求（return error） |
| 同上 | `after_authenticate_device` | 记录日志、修改响应 |
| `POST /v2/leaderboard/{id}` | `before_write_leaderboard_record` | 校验分数合法性 |
| 同上 | `after_write_leaderboard_record` | 发送通知、更新其他数据 |

#### 注意事项

- Hooks 是服务端行为，客户端代码无特殊处理——只需展示正确处理 hook 返回的错误
- `before_*` hooks 可以返回 error 阻止操作；`after_*` hooks 不能阻止但可以修改结果
- 需要对应的 Lua 模块放在 `data/modules/` 下（可新建 `hooks.lua`）
- 此示例体积较小，可单独写也可合并到 leaderboard 示例说明中

---

## 覆盖统计

| 分类 | 总数 | 已完成 | 待完成 |
|------|------|--------|--------|
| HTTP REST API 示例 | 11 | 2 (leaderboard, tournament) | 9 |
| WebSocket 实时示例 | 6 | 3 (ping-pong, matchmaker, party) | 3 |
| 服务端运行时示例 | 2 | 0 | 2 |
| **合计** | **19** | **5** | **14** |

> 注: leaderboard 和 tournament 同时覆盖 HTTP REST，ping-pong/matchmaker/party 覆盖 WebSocket，tournament 额外覆盖 RPC 调用。
