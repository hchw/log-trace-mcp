# 能力矩阵：参考实现 → 统一落点

本表把社区参考实现的原生工具逐项映射到 log-trace-mcp 的落点，用来核对"能力不下降"。
层级：**T1** 语义糖、**T2** 原生透传、**T3** 引擎独占（按租户类型门控）。

## 阿里云 SLS（参考 `alibabacloud-observability-mcp-server`，IaaS 工具集）

| 参考工具 | 统一落点 | 层 | 状态 |
|---|---|---|---|
| `sls_list_projects` | `list_sources(tenant)` | T1 | ✅ |
| `sls_list_logstores` | `list_sources(tenant, project)` | T1 | ✅ |
| `sls_execute_sql` | `search_logs`（语义）+ `native_query(language=sql)` | T1/T2 | ✅ |
| `sls_execute_spl` | `native_query(language=spl)` | T2 | ✅ |
| `sls_get_context_logs` | `get_context`（SLS 语义） | T1 | ✅ |
| `sls_log_explore` | `analyze`（时间直方图 + top 值） | T1 | ✅ |
| `sls_log_compare` | `compare_logs`（两时间窗对照） | T1 | ✅ |
| 时间表达式解析 | 统一 `time_range`（`sdk.ParseTimeRange`） | T0 | ✅ |
| `sls_text_to_sql` / `sls_text_to_spl` / `sls_sop` | 有意移出范围（见下） | — | ⛔ |

## OpenSearch（参考 `opensearch-project/opensearch-mcp-server-py`）

| 参考工具 | 统一落点 | 层 | 状态 |
|---|---|---|---|
| `ListIndexTool` | `list_sources(tenant)` | T1 | ✅ |
| `IndexMappingTool` | `describe_schema(tenant, source)` | T1 | ✅ |
| `SearchIndexTool` | `search_logs`（语义）+ `native_query(language=dsl)` | T1/T2 | ✅ |
| `PPLQueryTool` | `native_query(language=ppl)` | T2 | ✅ |
| `CountTool` | `count_logs` | T1 | ✅ |
| `MsearchTool` | `native_query`（原始批量体透传） | T2 | ✅ |
| `GenericOpenSearchApiTool` | `native_query` + T3 集群工具的底层 `RawRequest` | T2/T3 | ✅ |
| `ClusterHealthTool` | `os_cluster_health` | T3 | ✅ |
| `GetShardsTool` | `os_shards` | T3 | ✅ |
| `ExplainTool` | `os_explain` | T3 | ✅ |
| 动态 per-call 连接参数 | `tenant` 参数 + SQLite 配置 | — | ✅ |

## 天然不齐、按"不行就分开"处理的能力

| 能力 | SLS | OpenSearch | 处理 |
|---|---|---|---|
| 上下文日志 | `get_context` 基于 `pack_id`/`pack_meta` | 无等价语义 | OS 侧返回明确"不支持" |
| 集群运维 | 无 | `os_*` 工具 | 仅 OS 租户存在时注册 |
| 查询语言 | SQL / SPL | DSL / PPL | 由 T2 各自透传，互不翻译 |

## 有意移出范围

- **SLS AI 工具（`text_to_sql` / `text_to_spl` / `sop`）**：阿里云日志 Go SDK
  （`aliyun-log-go-sdk`）未暴露这些接口；它们实际走 CMS 数字员工 Chat API，
  需额外的 `cms:CreateChat` 权限并按次计费。经与用户确认，本变更不实现，
  T3 仅保留 OpenSearch 集群运维工具。如需支持，另开独立变更。
