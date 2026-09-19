# Kiro-Go

[![Go Version](https://img.shields.io/badge/Go-1.21+-00ADD8?style=flat&logo=go)](https://go.dev/)
[![Docker](https://img.shields.io/badge/Docker-Ready-2496ED?style=flat&logo=docker)](https://www.docker.com/)
[![License](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

将 Kiro 账号转换为 OpenAI / Anthropic 兼容的 API 服务。

[English](README.md) | 中文 | [Tiếng Việt](README_VI.md)

如果这个项目帮到了你，欢迎点个 Star 支持一下。

## 功能特性

- Anthropic `/v1/messages`、OpenAI `/v1/chat/completions` 与 OpenAI `/v1/responses`
- 多账号池轮询负载均衡
- 自动 Token 刷新、SSE 流式输出、Web 管理面板
- 多种认证方式：AWS Builder ID、IAM Identity Center (企业 SSO)、Microsoft 企业 SSO、SSO Token、本地缓存、凭证 JSON、Kiro API Key
- 用量追踪、账号导入导出、中英越三语
- 支持设置出站代理（SOCKS5 / HTTP）

## 快速开始

### Docker Compose（推荐）

```bash
git clone https://github.com/Quorinex/Kiro-Go.git
cd Kiro-Go
mkdir -p data
docker-compose up -d
```

### Docker 运行

```bash
docker run -d \
  --name kiro-go \
  -p 8080:8080 \
  -e ADMIN_PASSWORD=your_secure_password \
  -v /path/to/data:/app/data \
  --restart unless-stopped \
  ghcr.io/quorinex/kiro-go:latest
```

### 源码编译

```bash
git clone https://github.com/Quorinex/Kiro-Go.git
cd Kiro-Go
go build -o kiro-go .
./kiro-go
```

### 部署到 Zeabur

仓库已包含 `Dockerfile`，可直接在 Zeabur 上构建运行。

**方式一：面板一键部署**

1. Fork 本仓库到你的 GitHub 账号。
2. 在 Zeabur 新建服务，选择 **Deploy from GitHub**，绑定刚才 fork 的仓库。
3. Zeabur 自动识别 `Dockerfile` 并完成构建。
4. 在 **Networking** 标签暴露端口 `8080` 并绑定域名。
5. 在 **Variables** 标签至少设置 `ADMIN_PASSWORD`（管理面板密码）。
6. 如需持久化账号 / 配置，挂载 Volume 到 `/app/data`。

**方式二：CLI 部署**

```bash
npm i -g zeabur
zeabur auth login
zeabur deploy
```

> 命令需在项目根目录执行。CLI 会生成 `.zeabur/context.json` 记录目标 project / service，包含个人 ID，请勿提交。

部署完成后访问 `https://<你的域名>/admin` 登录管理面板。

首次运行会在 `data/config.json` 自动生成配置，挂载 `/app/data` 以持久化。默认管理密码为 `changeme`，生产环境请务必通过 `ADMIN_PASSWORD` 环境变量或在管理面板中修改。

## 使用方法

访问 `http://localhost:8080/admin` 登录、添加账号，然后调用 API：

```bash
# Claude
curl http://localhost:8080/v1/messages \
  -H "Content-Type: application/json" \
  -H "anthropic-version: 2023-06-01" \
  -d '{"model":"claude-sonnet-4.5","max_tokens":1024,"messages":[{"role":"user","content":"你好！"}]}'

# OpenAI
curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer any" \
  -d '{"model":"gpt-4o","messages":[{"role":"user","content":"你好！"}]}'
```

### 添加 Kiro API Key 账号

管理面板「添加账号」可选择 **API Key**，填写 `ksk_...`（也支持 `ksk_...|region`）。

也可通过凭证导入接口添加：

```bash
curl -X POST http://localhost:8080/admin/api/auth/credentials \
  -H "Content-Type: application/json" \
  -H "Cookie: <admin-session>" \
  -d '{"kiroApiKey":"ksk_your_key|us-east-1","authMethod":"api_key","nickname":"cli-key"}'
```

API Key 账号会走 Kiro CLI runtime（`https://runtime.{region}.kiro.dev/`），请求头带 `tokentype: API_KEY`，无需 OAuth 刷新，也不使用 `profileArn`。

## 思考模式

Thinking 设置中的「触发后缀」可以留空。留空后，直接使用基础模型名（例如 `claude-sonnet-5`）就会默认启用 thinking；填写 `-thinking` 等后缀后，则恢复为只有带后缀的模型名启用 thinking。Claude 兼容请求如果带有顶层 `thinking` 配置，例如 `{"type":"enabled","budget_tokens":2048}` 或 `{"type":"adaptive"}`，也会自动启用 thinking 模式。显式 `thinking.type=disabled` 仍然可以关闭本次请求。输出格式可在管理面板「设置 - Thinking 模式」中配置。

模型能力统一由 `proxy/thinking_policy.go` 管理，三个协议共用。旧模型使用 enabled + budget 提示；Sonnet/Opus 4.6 及能力表中的后续模型默认 adaptive + medium effort。Claude 的 `output_config.effort`、Chat Completions 的 `reasoning_effort`、Responses 的 `reasoning.effort` 映射到相同策略。仅对能力表支持的模型发送 `additionalModelRequestFields.output_config.effort`；不支持的模型/模式/effort 返回 400，未知模型不会自动生成 thinking 变体。

显式 `thinking.type=disabled` 优先于模型后缀。enabled 模式遵循显式 `thinking.budget_tokens`；未指定时默认 8192，提供 `max_tokens` 时默认预算不超过它的一半。adaptive 模式使用 effort，不套用固定 200000 预算。Token 计数使用同一套提示。这是 Kiro 兼容策略，不代表已验证所有账号都支持原生 thinking，也不保证延迟或可见思考内容。

## Prompt Cache

2026-09-19 在当前部署账号上进行重复前缀诊断：IDE 路线只有正文、contextUsage 和 metering 事件；CLI runtime 路线额外返回 metadata 和真实结束原因，但两条路线均未返回 tokenUsage/cache 计数。因此这里只能确认“未报告”，不能把未报告当成 0 命中，也不能从 credits 的下降反推出缓存 token 数。

Claude 工具定义中的 `cache_control` 会转换为 `userInputMessageContext.tools` 内的 `cachePoint`。这只是请求兼容映射，不代表上游已经缓存或给予缓存折扣。系统提示和消息上的 `cache_control` 接受但不转换为断点；保留正常内容。当前部署端点的对照实测中，历史数组插入独立 `cachePoint` 会导致 HTTP 400 `Improperly formed request`，移除该断点后同一请求成功，因此不再发送这种历史条目。

代理不会根据本地 fingerprint、账号或 TTL 猜测缓存命中。只有 Kiro 上游实际返回缓存 usage 时，Claude 响应才会带缓存统计；5 分钟/1 小时明细仅有上游报告才有意义，TTL 选择不保证支持。当前部署的工具前缀重复请求（流式与非流式）均未观察到缓存统计，因此不能宣称已实现 Anthropic 官方 Prompt Caching 或缓存折扣。`stream=true` 与缓存是否生效独立，关闭流式不会开启缓存。BirdSub2Api 账单金额是网关定价结果，不是 Kiro 实际 credits 的缓存证明。

## 共享流式链路与完整性

管理面板「端点设置」新增 `Kiro CLI (runtime)`，对应 `preferredEndpoint=cli`，OAuth 和 API Key 均可使用；OAuth 使用 profile 的数据区域，API Key 使用配置区域。CLI 使用自身的 JSON 协议、origin 和客户端头。它在当前账号的实测中返回真正的结束原因，避免 IDE 路线正常 EOF 却缺 stopReason 的问题。原有 auto/IDE 配置不自动迁移；启用 endpointFallback 时 CLI 失败仍可能降级到 IDE，API Key 则不会降级到 IDE。

真实 tokenUsage 优先于上下文百分比和本地估算，显式零值也不会被估算覆盖。输入缓存桶按互斥分量汇总；不存在的 TTL 明细不再伪造。invalidStateEvent 被作为错误处理。无输出时的连接失败、429/5xx 最多在同入口重试一次，遵循短 Retry-After；超过 5 秒的 Retry-After 直接报错，不提前重试。总超时不因重试延长，输出后不重放。

管理员可使用 `POST /admin/api/accounts/{id}/diagnose`，沿用 `X-Admin-Password` 鉴权。请求格式为 `{"request":{"model":"claude-sonnet-5","messages":[{"role":"user","content":"Reply OK"}]},"repeat":2,"endpoint":"cli"}`。省略 endpoint 则使用当前路由。最多重复 2 次、请求 256 KiB、max_tokens 8192；这是真实生成，会消耗额度。只返回入口状态/耗时、事件数量和尾部顺序、白名单数值用量、结束原因及 cache_status（unreported/reported/hit），不返回提示词、回答、凭据或工具参数，也不写入持久诊断日志。`success` 表示上游调用/解析无错误，完整性仍要结合 stop_reason 判断。

Claude Messages、Chat Completions、Responses 的流式/非流式请求共用上游超时、思考解析和完成判定。默认 `compatible` 策略只接受正常 EOF 且已有非空回答、没有未闭合思考块的缺失 stopReason 响应；不重新生成答案，保守标为 Claude `max_tokens` / Chat `length` / Responses `incomplete`，不再伪造 `end_turn`。这只是“无法证明完整”的协议映射，并不表示实际触及 token 上限。`KIRO_STREAM_EOF_POLICY=strict` 则将缺失终止信号视为错误，仅在未输出内容时允许有界重试。

缺少终止信号的纯思考/空白响应、传输损坏、未知 stopReason、未闭合思考仍报错；显式 token/context 上限允许以 incomplete 结束未闭合思考。可见文本、思考或工具调用输出后不重放请求。Responses 使用 `response.incomplete` / `response.failed`，不把失败包装成 `response.completed`；reasoning 通过 summary_text 兼容输出，流式事件与最终对象共享 item ID。

只解析开头的 `<thinking>`、`<think>`、`<reasoning>`、`<thought>`，支持跨事件切分和原生 reasoning 事件去重。正文开始后的标签、引号/代码围栏内的标签作为普通文本保留。仅清理有效的开头思考控制提示，不对正文做全局 XML 删除。

流式请求立即发送 ping（Claude）或 SSE 注释（OpenAI），因此等待上游时连接已经是 HTTP 200；之后失败通过协议内错误事件发送，调用方必须检查终止事件，不能只看 HTTP 状态。心跳与业务写入串行化，客户端断开或写失败会取消生成，终止事件后不再发心跳。原生 web_search 路径也使用心跳、请求取消和生成完整性校验；其搜索内容仍按原有合成结果格式返回。

首事件预算覆盖生成 HTTP 请求的响应头等待，metadata/心跳不算首个生成事件；真实文本/思考/工具事件后切换为空闲预算。总预算不被心跳、上游活动或重试延长。OAuth 刷新/Profile 发现仍使用原有独立 HTTP 超时，不能视为生成首事件时限内可立即取消的操作。

参考实现：`vagmr/kiro2api-rs`（MIT，心跳/标签边界）、`mydisha/keirouter`（MIT）、`jwadow/kiro-gateway`（AGPL-3，超时/完成状态）、`justlovemaki/AIClient2API`（GPL-3，effort 适配）。本次采用独立 Go 实现并复用本仓库的解析/重试基础设施，未复制这些项目代码；不将 GPL/AGPL 源文件直接混入本仓库 MIT 代码。

后续协议核验还参考 `d-kuro/kirocc`（Apache-2.0，CLI 请求协议、tokenUsage 分桶）和 `dat-lequoc/dsh-kiro`（tokenUsage 缺失时不声称已报告）。仅核对协议行为并独立实现，缓存是否生效以部署端实测元数据为准。

## 出站代理

可在管理面板「设置 - 出站代理设置」中配置代理。支持 SOCKS5 和 HTTP 代理。

设置保存后即时生效，无需重启服务。

## 环境变量

| 变量 | 说明 | 默认值 |
|-----|------|-------|
| `CONFIG_PATH` | 配置文件路径 | `data/config.json` |
| `ADMIN_PASSWORD` | 管理面板密码（覆盖配置文件） | - |
| `KIRO_FIRST_EVENT_TIMEOUT` | 普通生成首事件时限 | `60s` |
| `KIRO_THINKING_FIRST_EVENT_TIMEOUT` | thinking 生成首事件时限 | `180s` |
| `KIRO_STREAM_IDLE_TIMEOUT` | 首事件后的上游读空闲时限 | `90s` |
| `KIRO_STREAM_TOTAL_TIMEOUT` | 单请求生成总预算（含重试） | `5m` |
| `KIRO_SSE_PING_INTERVAL` | 下游心跳间隔 | `15s` |
| `KIRO_SSE_WRITE_TIMEOUT` | 单次下游写入/刷新时限 | `15s` |
| `KIRO_STREAM_EOF_POLICY` | `compatible` 或 `strict` | `compatible` |

时间支持 Go duration（如 `90s`、`5m`）或整数秒，必须大于 0 且不超过 24 小时；无效值记录警告并使用默认值。Railway 可在服务环境变量中配置。超时错误标识 `first_event`、`idle` 或 `total`，便于区分等待生成、流中断和总预算耗尽。

CI 执行全量普通测试和静态检查，并对新增传输、超时、解析器、Responses 输出模块重复执行竞态检查。全量 proxy 竞态测试目前会暴露既有的跨测试 `config.Init` 与异步账号统计 `config.Save` 竞态；本次不将整个项目宣称为无竞态。

## 参与贡献

欢迎友好交流。遇到问题时，建议先让 Claude Code、Codex 等工具帮忙排查一下，大部分问题都能自己解决。如果能直接提个 PR 就更好了。

## 友情链接

- [LINUX DO](https://linux.do)

## 免责声明

本项目仅供学习和研究目的使用，与 Amazon、AWS 或 Kiro 没有任何关联。用户需自行确保使用行为符合所有适用的服务条款和法律法规，使用风险自负。

## 许可证

[MIT](LICENSE)
