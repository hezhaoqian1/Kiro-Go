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

在模型名后加后缀（默认 `-thinking`）即可启用，例如 `claude-sonnet-4.5-thinking`。Claude 兼容请求如果带有顶层 `thinking` 配置，例如 `{"type":"enabled","budget_tokens":2048}` 或 `{"type":"adaptive"}`，也会自动启用 thinking 模式。输出格式可在管理面板「设置 - Thinking 模式」中配置。

模型能力统一由 `proxy/thinking_policy.go` 管理，三个协议共用。旧模型使用 enabled + budget 提示；Sonnet/Opus 4.6 及能力表中的后续模型默认 adaptive + medium effort。Claude 的 `output_config.effort`、Chat Completions 的 `reasoning_effort`、Responses 的 `reasoning.effort` 映射到相同策略。仅对能力表支持的模型发送 `additionalModelRequestFields.output_config.effort`；不支持的模型/模式/effort 返回 400，未知模型不会自动生成 thinking 变体。

显式 `thinking.type=disabled` 优先于模型后缀。enabled 模式遵循显式 `thinking.budget_tokens`；未指定时默认 8192，提供 `max_tokens` 时默认预算不超过它的一半。adaptive 模式使用 effort，不套用固定 200000 预算。Token 计数使用同一套提示。这是 Kiro 兼容策略，不代表已验证所有账号都支持原生 thinking，也不保证延迟或可见思考内容。

## 共享流式链路与完整性

Claude Messages、Chat Completions、Responses 的流式/非流式请求共用上游超时、思考解析和完成判定。默认 `compatible` 策略只接受正常 EOF 且已有非空回答、没有未闭合思考块的缺失 stopReason 响应；不重新生成答案，保守标为 Claude `max_tokens` / Chat `length` / Responses `incomplete`，不再伪造 `end_turn`。这只是“无法证明完整”的协议映射，并不表示实际触及 token 上限。`KIRO_STREAM_EOF_POLICY=strict` 则将缺失终止信号视为错误，仅在未输出内容时允许有界重试。

缺少终止信号的纯思考/空白响应、传输损坏、未知 stopReason、未闭合思考仍报错；显式 token/context 上限允许以 incomplete 结束未闭合思考。可见文本、思考或工具调用输出后不重放请求。Responses 使用 `response.incomplete` / `response.failed`，不把失败包装成 `response.completed`；reasoning 通过 summary_text 兼容输出，流式事件与最终对象共享 item ID。

只解析开头的 `<thinking>`、`<think>`、`<reasoning>`、`<thought>`，支持跨事件切分和原生 reasoning 事件去重。正文开始后的标签、引号/代码围栏内的标签作为普通文本保留。仅清理有效的开头思考控制提示，不对正文做全局 XML 删除。

流式请求立即发送 ping（Claude）或 SSE 注释（OpenAI），因此等待上游时连接已经是 HTTP 200；之后失败通过协议内错误事件发送，调用方必须检查终止事件，不能只看 HTTP 状态。心跳与业务写入串行化，客户端断开或写失败会取消生成，终止事件后不再发心跳。原生 web_search 路径也使用心跳、请求取消和生成完整性校验；其搜索内容仍按原有合成结果格式返回。

首事件预算覆盖生成 HTTP 请求的响应头等待，metadata/心跳不算首个生成事件；真实文本/思考/工具事件后切换为空闲预算。总预算不被心跳、上游活动或重试延长。OAuth 刷新/Profile 发现仍使用原有独立 HTTP 超时，不能视为生成首事件时限内可立即取消的操作。

参考实现：`vagmr/kiro2api-rs`（MIT，心跳/标签边界）、`mydisha/keirouter`（MIT）、`jwadow/kiro-gateway`（AGPL-3，超时/完成状态）、`justlovemaki/AIClient2API`（GPL-3，effort 适配）。本次采用独立 Go 实现并复用本仓库的解析/重试基础设施，未复制这些项目代码；不将 GPL/AGPL 源文件直接混入本仓库 MIT 代码。

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

## 参与贡献

欢迎友好交流。遇到问题时，建议先让 Claude Code、Codex 等工具帮忙排查一下，大部分问题都能自己解决。如果能直接提个 PR 就更好了。

## 友情链接

- [LINUX DO](https://linux.do)

## 免责声明

本项目仅供学习和研究目的使用，与 Amazon、AWS 或 Kiro 没有任何关联。用户需自行确保使用行为符合所有适用的服务条款和法律法规，使用风险自负。

## 许可证

[MIT](LICENSE)
