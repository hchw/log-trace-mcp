# log-trace-mcp

> 一个 Go 单二进制，把阿里云 SLS 和 OpenSearch 的日志查询统一成一套 MCP 工具。
> 多租户配置落在 SQLite、凭据加密存储、支持热加载，并自带一个 Web 管理页。

面对"日志散在 SLS 和 OpenSearch 两个后端、每个环境一套凭据"的现实，这套工具的目标是：
给 AI Agent 一个统一入口去查日志，同时**不削减任一后端的原生能力**。

---

## 特性一览

- **统一工具面**：一套工具覆盖 SLS 与 OpenSearch，参数与结果结构一致。
- **能力不下降**：语义工具（T1）+ 原生透传（T2）+ 引擎独占（T3）三层，任何后端能做的都能做到。
- **多租户**：`tenant` 代表一套日志中心连接（一套凭据/权限范围），可对应 prod / test 等不同环境。
- **热加载**：改配置后同进程原子替换 adapter，无需重启。
- **凭据加密**：AES-256-GCM 落库，主密钥来自环境变量或配置文件，不在库内。
- **单二进制**：无 CGO（`modernc.org/sqlite`），内置时区库，离线可跑。
- **Web 管理页**：登录 + 租户增删改查 + 连通性测试，另有 Bearer Token 供脚本调用。
- **双传输**：MCP 同时支持 stdio 与 streamable HTTP。

---

## 快速开始

```bash
# 1. 构建
make build

# 2. 生成一份配置（随机主密钥，写入 config.json）
make config
#    把 config.json 里的 admin_token 改掉

# 3. 启动（默认同时开管理页 8710 与 MCP HTTP 8711）
./bin/log-trace-mcp serve

# 4. 打开管理页新增一个 tenant
open http://127.0.0.1:8710
```

把 MCP 接入客户端（以 pi 为例，写项目级 `.pi/mcp.json`）：

```bash
pi mcp add -l logtrace --url http://127.0.0.1:8711/mcp --description "统一日志查询 MCP"
pi mcp list
```

或用 stdio 传输：

```bash
./bin/log-trace-mcp serve --stdio
```

---

## 工具清单

### 语义工具（T1）与原生透传（T2）——始终注册

| 工具 | 作用 |
|---|---|
| `list_sources` | 列出租户下的数据源：SLS 返回 project（给 `project` 时下钻 logstore），OpenSearch 返回 index |
| `describe_schema` | 描述数据集字段结构：SLS 索引键、OpenSearch mapping |
| `search_logs` | 关键字 + 字段过滤 + 时间窗 + 排序分页 |
| `count_logs` | 统计匹配条数 |
| `compare_logs` | 两个时间窗对照并给出差异 |
| `analyze` | 时间直方图 + 可选字段分组 top 值 |
| `get_context` | 某一点前后的上下文日志（SLS） |
| `native_query` | 原生查询透传：SLS `{sql,spl}`，OpenSearch `{dsl,ppl}` |
| `convert_time` | 时区换算：给定时间 + 原时区 + 目标时区，返回目标时区的时间 |

### 引擎独占（T3）——仅当存在对应类型租户时注册

| 工具 | 后端 | 作用 |
|---|---|---|
| `os_cluster_health` | OpenSearch | 集群健康 |
| `os_shards` | OpenSearch | 分片信息 |
| `os_explain` | OpenSearch | 解释某文档为何命中 |

> "能力不下降"由 T2 + T3 保证：任何后端原生能做的，都能通过 `native_query` 或引擎独占工具完成。
> 逐项映射见 [`docs/capability-matrix.md`](docs/capability-matrix.md)。

---

## 概念模型

```
  tenant   一套日志中心连接 / 一套凭据（例如 prod、test）
    │      每个 tenant 绑定一个后端类型：sls 或 opensearch
    └─ project   SLS project（OpenSearch 忽略）
         └─ source   SLS logstore 或 OpenSearch index
```

- **`tenant`** 是工具的必填参数，代表权限范围。不同环境用不同 `tenant`。若用户未指明，模型应先询问。
- **`project`** 是 SLS 的时间/空间命名层，OpenSearch 侧忽略。
- **`source`** 是具体数据集；也可用 `project/logstore` 两段式传给 `source`（此时 `project` 可省略）。

### 时间参数

所有带时间窗的工具都同时接受：

- `time_range`：`last_1h`、`today`、`now-15m~now-5m`、unix 秒、或 `2026-10-17 12:26:00`
- `from` / `to`：显式端点，取值可为 unix 秒/毫秒或多种日期写法，**给出时覆盖 `time_range` 的对应端点**

时间的决定权交给调用方：想查哪个窗口就传哪个窗口，不受固定预设限制。

---

## 配置

优先级：**环境变量 > 配置文件 > 默认值**。配置文件默认读取 `./config.json`（可用 `LOG_TRACE_CONFIG` 指定）。

`config.json`：

```json
{
  "master_key": "<base64 的 32 字节密钥>",
  "admin_token": "change-me",
  "db_path": "log-trace-mcp.db",
  "admin_addr": "127.0.0.1:8710",
  "mcp_http_addr": "127.0.0.1:8711"
}
```

### 环境变量

| 变量 | 说明 | 默认 |
|---|---|---|
| `LOG_TRACE_MASTER_KEY` | 凭据加密主密钥（base64 的 32 字节） | 必填 |
| `LOG_TRACE_ADMIN_TOKEN` | 管理接口 Token | 必填 |
| `LOG_TRACE_DB` | SQLite 路径 | `log-trace-mcp.db` |
| `LOG_TRACE_ADMIN_ADDR` | 管理页监听地址 | `127.0.0.1:8710` |
| `LOG_TRACE_MCP_HTTP_ADDR` | MCP HTTP 监听地址 | `127.0.0.1:8711` |
| `LOG_TRACE_CONFIG` | 配置文件路径 | `./config.json` |
| `LOG_TRACE_NEW_MASTER_KEY` | `rekey` 用的新主密钥 | — |

### 租户配置示例

SLS：

```json
{
  "id": "prod-sls", "type": "sls", "enabled": true,
  "config": { "region": "cn-hangzhou", "endpoint": "cn-hangzhou.log.aliyuncs.com" },
  "creds": { "ak_id": "LTAI...", "ak_secret": "..." }
}
```

OpenSearch：

```json
{
  "id": "prod-os", "type": "opensearch", "enabled": true,
  "config": {
    "endpoint": "https://os.example.com:9200",
    "index": "app-logs-*", "time_field": "@timestamp",
    "auth_kind": "basic", "tls_skip_verify": false
  },
  "creds": { "username": "admin", "password": "..." }
}
```

`auth_kind` 取值：`basic` | `header`（配 `header_key`/`header_value`）| `none`。

---

## 管理页

默认只监听回环地址，避免误暴露。

- **浏览器**：访问 `/login`，输入 `LOG_TRACE_ADMIN_TOKEN` 登录，之后用 HttpOnly Session Cookie（12h）。
- **脚本**：直接带 `Authorization: Bearer <token>`。

主要接口：

```
GET    /api/tenants            列出（凭据脱敏为 ****）
POST   /api/tenants            新增
GET    /api/tenants/{id}       查看
PUT    /api/tenants/{id}       更新
DELETE /api/tenants/{id}       删除
POST   /api/tenants/{id}/test  连通性测试（10s 超时，返回样例与耗时）
GET    /api/tools              当前已注册工具
```

页面底部有「运行日志」面板，记录加载/保存/测试等操作。

---

## CLI

```bash
log-trace-mcp serve [--stdio]   # 启动服务（默认 serve）
log-trace-mcp tools             # 列出当前已注册工具
log-trace-mcp version           # 版本
log-trace-mcp rekey --key <b64> # 用新主密钥重加密全部凭据
```

`make` 常用目标：

```
build run test test-race coverage vet fmt tidy check tools version
keygen config rekey validate cross clean help
```

---

## 架构

```
cmd/log-trace-mcp        CLI 入口（serve / tools / rekey / version）
internal/config          配置加载（文件 + 环境变量）
internal/crypto          AES-256-GCM 凭据加解密
internal/store           SQLite 持久化（租户、凭据、data_version）
internal/sdk             与 MCP/后端无关的核心：
                           types / service / registry / source /
                           timerange / timezone / normalize
internal/adapter/sls     Aliyun SLS 适配
internal/adapter/opensearch  OpenSearch 适配
internal/manager         热加载：写后通知 + data_version 轮询兜底
internal/mcp             MCP 工具注册与处理（T1/T2/T3、门控）
internal/admin           Web 管理页 + 会话鉴权
```

分层意图：**SDK 层可复用**，MCP 只是它的一个消费方；后端差异被收敛在 adapter 内。

### 热加载

配置变更后同进程原子替换注册表：

1. 写入后主动触发重载；
2. `data_version` 轮询兜底（多实例/外部改动场景）；
3. 旧 adapter 延迟 `Close`，避免打断在途请求。

---

## 安全

- 凭据以 AES-256-GCM 加密落库，AAD 绑定 `tenant_id`；主密钥不写库。
- 管理接口默认绑定 `127.0.0.1`，登录用 HttpOnly + SameSite Cookie，Token 常量时间比较。
- Token/凭据在 API 返回与页面中脱敏。

---

## 开发

```bash
make check        # fmt + vet + test
make test-race    # 竞态检测
make coverage     # 覆盖率
make cross        # 交叉编译
```

真实端到端测试需要提供凭据（未配置时自动跳过）：

```
LTM_TEST_SLS_*   阿里云 SLS
LTM_TEST_OS_*    OpenSearch
```

---

## 范围说明

- 只做**日志**，不含 trace / metric。
- SLS 的 AI 辅助工具（`text_to_sql` / `text_to_spl` / `sop`）**不在范围内**：Go SDK 未暴露，
  实际走 CMS 数字员工 Chat API，需额外权限且按次计费。详见能力矩阵。

## License

仓库当前未附 License 文件；对外使用前请先与维护者确认授权方式。
