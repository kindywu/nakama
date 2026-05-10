# Nakama 前端调用链详解 (Console SPA)

> 后端调用链请参见 [call-chains-backend.md](call-chains-backend.md) — 包含 HTTP/gRPC/WebSocket 三种协议的后端完整处理链路。

本文追踪 Console Vue SPA 中的典型用户交互在前端侧的完整调用链: 路由导航、组件渲染、API 调用、状态更新和视图刷新。前端与后端的对接点标注了指向后端文档的超链接。

## 1. 用户登录 (前端视角)

> 此场景对应的后端调用链: [后端: Console 用户登录](call-chains-backend.md#1-console-用户登录)

**场景**: 用户在浏览器输入 Console URL,看到登录页,输入凭据点击登录。

```mermaid
sequenceDiagram
    participant Browser as 浏览器地址栏
    participant Router as Vue Router
    participant Guard as 路由守卫
    participant Login as LoginPage
    participant API as fetch API
    participant Store as 会话状态
    participant Layout as AuthenticatedLayout

    Browser->>Router: 输入 / → router.push("/")
    Router->>Guard: beforeEach(to, from)
    Guard->>Guard: 读取 sessionStorage("console_token")
    alt token 不存在或已过期
        Guard->>Router: 重定向 → /login
        Router->>Login: 渲染 LoginPage 组件
        Login->>Login: onMounted() 清空表单
    end

    Note over Login: 用户输入 username + password
    Login->>Login: handleSubmit()
    Login->>Login: set loading = true, error = null
    Login->>API: fetch("POST /v2/console/authenticate")<br/>body: {username, password}
    API-->>Login: Response

    alt 响应含 mfa_code
        Login->>Login: set showMFA = true<br/>存储临时 token
        Note over Login: 用户输入 TOTP 6位码
        Login->>API: fetch("POST /v2/console/authenticate")<br/>body: {username, password, mfa}
        API-->>Login: {token}
    end

    Login->>Store: sessionStorage.setItem("console_token", token)
    Login->>Store: 解析 JWT claims → username, email, acl
    Login->>Router: router.replace("/")
    Router->>Guard: beforeEach → token 有效
    Guard->>Layout: 渲染 AuthenticatedLayout
    Layout->>Layout: onMounted() 加载侧栏菜单<br/>(根据 ACL 控制可见性)
    Layout->>Router: <router-view> 渲染仪表盘首页
```

#### 分步骤追踪

| # | 层面 | 操作 | 说明 |
|---|------|------|------|
| 1 | Router | `createRouter({ history: createWebHashHistory() })` | 使用 hash 模式路由 (`/#/login`),无需服务端路由支持 |
| 2 | Guard | `router.beforeEach(to, from)` | 全局前置守卫,每次路由变化触发 |
| 3 | Guard | `sessionStorage.getItem("console_token")` | 读取持久化的 JWT token |
| 4 | Guard | 解析 token — 无 token / exp < now | 未认证或 token 过期 → `next("/login")` |
| 5 | Router | `{ path: "/login", component: LoginPage }` | 路由匹配,异步加载 LoginPage 组件 |
| 6 | Component | `LoginPage` setup | `ref(username)`, `ref(password)`, `ref(loading)`, `ref(error)`, `ref(showMfa)` |
| 7 | Component | `<form @submit.prevent="handleSubmit">` | 表单提交阻止默认行为 |
| 8 | API | `fetch("/v2/console/authenticate", ...)"` | 调用 gRPC-Gateway 端点 → 进入 [后端登录流程](call-chains-backend.md#1-console-用户登录) |
| 9 | Response | `res.json()` → `{ token: "eyJ...", mfa_code?: "..." }` | 解析 JSON 响应 |
| 10 | Branch | `if (data.mfa_code)` | MFA 未配置: 显示 TOTP 设置界面,存储临时 token |
| 11 | API | 再次 `fetch("/v2/console/authenticate", { body: {username, password, mfa} })` | 提交 TOTP 码完成 MFA 验证 |
| 12 | Store | `sessionStorage.setItem("console_token", data.token)` | 持久化 token (关闭标签页后清除) |
| 13 | Store | 解析 token payload: `JSON.parse(atob(token.split(".")[1]))` | 提取 `uid`, `usn` (username), `ema` (email), `acl` (权限位图) |
| 14 | Router | `router.replace("/")` | 替换历史记录,防止用户返回到登录页 |
| 15 | Guard | `beforeEach` → token 有效 → `next()` | 放行路由 |
| 16 | Layout | `AuthenticatedLayout` 渲染 | 显示 SidebarNav + TopBar + `<router-view>` |
| 17 | Component | `SidebarNav` | 根据 `acl` 值控制菜单项可见性 (read=1, write=2, delete=4) |
| 18 | Router | `<router-view>` 渲染 DashboardPage | 默认首页,调用 `GET /v2/console/status` 获取节点状态 |

### 2. 账户列表 → 详情导航

**场景**: 管理员点击侧栏 "Accounts",浏览列表,点击某个用户查看详情。

```mermaid
sequenceDiagram
    participant Nav as SidebarNav
    participant Router as Vue Router
    participant List as AccountsListPage
    participant API as fetch API
    participant Detail as AccountDetailPage
    participant Tabs as Tab 组件

    Nav->>Router: 点击 "Accounts" → router.push("/accounts")
    Router->>List: 匹配路由 { path: "/accounts", component: AccountsListPage }
    List->>List: onMounted()
    List->>API: fetch("GET /v2/console/account?filter=...&cursor=")
    API-->>List: { accounts: [...], next_cursor: "..." }
    List->>List: 更新 accounts (reactive ref)<br/>渲染 DataTable

    Note over List: 用户在搜索框输入 "player"
    List->>List: watch(searchTerm) with 300ms debounce
    List->>API: fetch("GET /v2/console/account?filter=player&cursor=")
    API-->>List: { accounts: [...filtered] }
    List->>List: 更新列表,重置分页

    Note over List: 用户点击某行账户
    List->>Router: router.push(`/accounts/${account.user.id}`)
    Router->>Detail: 匹配路由 { path: "/accounts/:id", component: AccountDetailPage }
    Detail->>Detail: onMounted() — 从 route.params 提取 id
    Detail->>Detail: set activeTab = "overview", loading = true
    Detail->>API: fetch(`GET /v2/console/account/${id}`)
    API-->>Detail: { account: { user: {...}, wallet: "...", ... } }
    Detail->>Detail: 更新 account ref → 渲染 OverviewTab

    Note over Detail: 用户切换到 "Friends" tab
    Detail->>Tabs: @change="handleTabChange('friends')"
    Tabs->>API: fetch(`GET /v2/console/account/${id}/friend`)
    API-->>Tabs: { friends: [...] }
    Tabs->>Tabs: 渲染 FriendsTab 表格

    Note over Detail: 用户切换到 "Wallet" tab
    Detail->>API: fetch(`GET /v2/console/account/${account_id}/wallet`)
    API-->>Detail: { items: [...] }
    Detail->>Detail: 渲染 WalletTab 流水列表

    Note over Detail: 用户点击 "Unlink Google"
    Detail->>API: fetch("POST /v2/console/account/{id}/unlink/google")
    API-->>Detail: 200 OK
    Detail->>API: 重新 fetch(`GET /v2/console/account/${id}`)
    Detail->>Detail: 刷新 account 数据,LinkStatusPanel 更新
```

#### 分步骤追踪

| # | 层面 | 操作 | 说明 |
|---|------|------|------|
| 1 | Router | `router.push("/accounts")` | 侧栏导航触发路由跳转 |
| 2 | Router | 懒加载 `() => import("./pages/AccountsList.vue")` | Vite 代码分割 |
| 3 | Component | `AccountsListPage` setup | 初始化 `accounts = ref([])`, `loading = ref(false)`, `searchTerm = ref("")`, `cursor = ref(null)` |
| 4 | Lifecycle | `onMounted(() => fetchAccounts())` | 组件挂载后立即加载数据 |
| 5 | API | `fetchAccounts()` → `fetch("/v2/console/account?cursor=")` | 首次加载不带 filter,使用默认排序 |
| 6 | Reactivity | `accounts.value = data.accounts` | 响应式赋值,触发 DataTable 重新渲染 |
| 7 | Pagination | 检查 `data.next_cursor` | 有值 → 显示 "Load More" 按钮,点击追加数据 |
| 8 | Debounce | `watch(searchTerm, () => { clearTimeout(timer); timer = setTimeout(fetchAccounts, 300) })` | 搜索防抖,避免每次按键都请求 |
| 9 | Route | 行点击 → `router.push({ path: \`/accounts/${id}\` })` | 导航到详情页 |
| 10 | Detail | `AccountDetailPage` setup | `route = useRoute()`; `account = ref(null)`; `activeTab = ref("overview")` |
| 11 | Lifecycle | `onMounted(() => fetchAccount(route.params.id))` | 提取路由参数,请求详情 |
| 12 | Watcher | `watch(() => route.params.id, (newId) => fetchAccount(newId))` | 监听路由参数变化 (从详情页导航到另一个详情页) |
| 13 | API | `fetch(\`/v2/console/account/${id}\`)` | 获取账户完整信息 |
| 14 | Tab | `handleTabChange(tab)` | 切换 tab 时按需加载: friends/groups/wallet/storage/notes |
| 15 | Action | `unlinkProvider(provider)` → `fetch(\`POST /v2/console/account/${id}/unlink/${provider}\`)` | 解除社交登录绑定 |
| 16 | Refresh | `unlink` 成功后重新 `fetchAccount(id)` | 乐观更新或请求后刷新 |
| 17 | Back | 点击 BackButton → `router.back()` 或 `router.push("/accounts")` | 返回列表; 列表数据保留在组件缓存中 (若使用 `<keep-alive>`) |

### 3. API Explorer 请求构造与发送

**场景**: 管理员在 API Explorer 中选择端点、编辑请求体、发送请求并查看响应。

```mermaid
sequenceDiagram
    participant Sidebar as EndpointSidebar
    participant Monaco as Monaco Editor
    participant API as fetch API
    participant Response as ResponsePanel

    Sidebar->>Sidebar: onMounted()
    Sidebar->>API: fetch("GET /v2/console/api/endpoints")
    API-->>Sidebar: { endpoints: [{method, url, description, body_example}, ...] }
    Sidebar->>Sidebar: 按 category 分组<br/>渲染 EndpointTree

    Note over Sidebar: 用户点击 "GetAccount\nGET /v2/account"
    Sidebar->>Monaco: @select="selectEndpoint(endpoint)"
    Monaco->>Monaco: 更新 method/url 显示
    Monaco->>Monaco: 若 endpoint 有 body_example<br/>→ editor.setValue(JSON.stringify(example, null, 2))
    Monaco->>Monaco: 若 endpoint 无 body<br/>→ editor.setValue("")

    Note over Monaco: 用户在编辑器中修改 JSON
    Monaco->>Monaco: editor.onDidChangeModelContent()<br/>→ 更新 requestBody ref

    Note over Monaco: 用户点击 "Send"
    Monaco->>API: handleSend()
    Monaco->>Monaco: set sending = true, response = null
    Monaco->>API: const headers = buildHeaders(customHeaders)
    Monaco->>API: method === "GET"<br/>? fetch(`${url}?${queryParams}`, {headers})<br/>: fetch(url, {method, headers, body: requestBody})
    API-->>Response: HTTP Response
    Response->>Response: statusCode = res.status<br/>responseTime = Date.now() - startTime
    Response->>Response: const body = await res.json()
    Response->>Response: Monaco Editor (只读模式)<br/>editor.setValue(JSON.stringify(body, null, 2))
    Response->>Response: 渲染 ResponseHeaders 列表
    Monaco->>Monaco: set sending = false

    Note over Monaco: 用户点击另一个端点 → 重复上述流程
```

#### 分步骤追踪

| # | 层面 | 操作 | 说明 |
|---|------|------|------|
| 1 | Lifecycle | `onMounted(() => loadEndpoints())` | APIExplorerPage 挂载时加载端点列表 |
| 2 | API | `fetch("GET /v2/console/api/endpoints")` | 获取所有可调用的 API 端点描述 |
| 3 | Data | 响应含 `method`, `url`, `description`, `body_example` | 每个端点带请求体示例 (来自 proto 定义) |
| 4 | Component | `EndpointTree` 渲染 | 按首段路径分组: `account`, `leaderboard`, `match`, `group`, `user`, `storage`, ... |
| 5 | Event | `@select` → `selectedEndpoint = endpoint` | 选中端点,触发 RequestPanel 更新 |
| 6 | Monaco | `editor.setValue(prettyPrint(endpoint.body_example))` | Monaco Editor 显示预格式化的 JSON 模板 |
| 7 | Monaco | `editor.onDidChangeModelContent(() => { requestBody.value = editor.getValue() })` | 监听编辑内容变化,同步到响应式状态 |
| 8 | Headers | 自定义 Key-Value 编辑器 | 可添加/删除 HTTP 头 (如 `Authorization` 覆盖) |
| 9 | Send | `handleSend()` | 构造并发送请求 |
| 10 | Send | `startTime = Date.now()` | 开始计时 |
| 11 | API | `fetch(url, { method, headers, body: method !== "GET" ? requestBody : undefined })` | 实际 HTTP 请求 |
| 12 | Response | `responseTime = Date.now() - startTime` | 计算耗时 (ms) |
| 13 | Response | `responseBody = await res.json()` | 解析 JSON 响应体 |
| 14 | Response | 右侧 Monaco Editor (`readOnly: true`) 显示格式化 JSON | 语法高亮,支持折叠/搜索 |
| 15 | Response | ResponseHeaders 列表渲染 | 逐行显示 `Content-Type`, `Grpc-Status` 等 |
| 16 | History | `localStorage.setItem("api_explorer_history", JSON.stringify(history))` | 请求历史持久化 (可选) |

### 4. 前端组件通信模式

以下是 Console SPA 中主要的组件通信模式:

```
1. Props down / Events up (父 → 子 → 父)
   AuthenticatedLayout
     │  :acl="acl"
     ▼
   SidebarNav
     │  @navigate="handleNavigate"
     ▼
   AuthenticatedLayout → router.push(...)

2. Provide / Inject (祖先 → 后代, 跨层级)
   App.vue
     │  provide("consoleConfig", { nt, ... })
     │  provide("currentUser", user)
     ▼  (任意深度的子组件)
   AnyNestedComponent
     │  const user = inject("currentUser")

3. Route params (页面间通信)
   AccountsListPage → router.push(`/accounts/${id}`)
     │
     ▼
   AccountDetailPage → const id = useRoute().params.id

4. sessionStorage (跨标签页/刷新持久化)
   LoginPage → sessionStorage.setItem("console_token", token)
     │  (页面刷新后)
     ▼
   router.beforeEach → sessionStorage.getItem("console_token")

5. Composition API ref/reactive (组件内状态)
   const accounts = ref([])
   const loading = ref(false)
   watch(searchTerm, debounce(fetchAccounts, 300))
```

### 5. 前后端调用链对接

将前端调用链与后端调用链拼接,形成完整的端到端链路。以账户详情页为例:

```
前端 (Vue SPA)                              后端 (Go Server)
═══════════════                             ═══════════════
                                             详见 call-chains-backend.md

AccountDetailPage.onMounted()
  │
  ├─ fetch("/v2/console/account/{id}") ──── → [后端: Console API 认证拦截](call-chains-backend.md#2-客户端-api-调用-getaccount)
  │                                            ├─ gorilla/mux → gRPC-Gateway
  │                                            ├─ consoleAuthInterceptor
  │                                            ├─ ConsoleServer.GetAccount()
  │                                            └─ SELECT ... FROM users
  │                                           │
  ◄──────────────────── JSON Response ────────┘
  │
  ├─ account.value = data.account
  ├─ 渲染 OverviewTab
  │
  ├─ 用户点击 FriendsTab
  │
  ├─ fetch("/v2/console/account/{id}/friend") ──── → 后端 GetFriends RPC
  │                                           │
  ◄──────────────────── JSON Response ────────┘
  │
  └─ friends.value = data.friends
     └─ 渲染 FriendsTable
```

前端 Vue SPA 中的一个"页面"可能触发多个后端 API 调用 (详情页首次加载调用 1 个,切换 Tab 各调用 1 个),每个调用都是独立的 HTTP 请求,经过完整的后端中间件链和 gRPC 调用链。后端的详细处理步骤参见 [后端调用链 §2](call-chains-backend.md#2-客户端-api-调用-getaccount)。

### 6. 前端错误处理

| 场景 | 处理方式 |
|------|---------|
| 401 未认证 | 全局 `fetch` 拦截器 → 清除 token → `router.push("/login")` |
| 403 权限不足 | 显示 toast 提示 "Insufficient permissions" |
| 404 不存在 | 页面显示空状态或 "Not found" 占位 |
| 500 服务端错误 | 显示 toast 提示 + 服务端返回的 error_message |
| 网络错误 | 显示 toast "Network error, please try again" + RetryButton |
| token 临近过期 | 全局定时器检测 exp → 弹出续期提示或自动跳转登录 |
| 表单校验失败 | 字段级 inline 错误提示 (红色边框 + 文字) |
