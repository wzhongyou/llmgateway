# llmgate

> Go 智能体应用的 LLM 基础设施层

[![Go Version](https://img.shields.io/badge/go-%3E%3D1.21-blue)](https://golang.org/)
[![License](https://img.shields.io/badge/license-MIT-green)](LICENSE)

[English](README.md) · [设计文档](docs/design.md) · [参与共建](CONTRIBUTING.md)

---

## 这是什么

你在构建智能体应用，需要接入多个大模型。三个真实问题：

- **换模型要改代码** — 每家 API 格式不同，业务逻辑和模型耦合
- **出了问题不知道为什么** — 是模型慢？被限速？还是 token 超出预算？
- **Token 用量不透明** — 多个 provider 混用，输入/输出/推理 token 各自消耗对不上

**llmgate** 的做法：统一接口屏蔽差异，每次调用白盒记录 provider / 模型 / token 明细 / 延迟，内置降级和延迟限制策略，为可视化监控铺路。

**三种使用形态，按需选择：**

| 形态 | 适用场景 |
|------|----------|
| **SDK** | Go 项目直接引入，一行代码接入多个模型 |
| **Gateway** | 独立部署 HTTP 服务，非 Go 项目也能接入 |
| **Studio**（规划中） | 可视化控制台，延迟分布 / 模型对比 / Token 趋势一屏看清 |

---

## 快速开始

```bash
go get github.com/wzhongyou/llmgate
```

**三种配置方式任选其一：**

**方式一 — 配置文件（推荐）**

```bash
cp llmgate.toml.example llmgate.toml
# 编辑 key 字段
```

```go
gw, err := llmgate.NewFromFile("llmgate.toml")
```

**方式二 — 环境变量**

```bash
export DEEPSEEK_KEY="sk-xxx"
export GLM_KEY="your-glm-key"
```

```go
gw := llmgate.New()  // 自动从环境变量加载
```

**方式三 — 代码**

```go
gw := llmgate.New()
gw.Use("deepseek", "sk-xxx")
```

**开始使用：**

```go
package main

import (
    "context"
    "fmt"

    "github.com/wzhongyou/llmgate"

    // 步骤一：blank import 注册 provider
    _ "github.com/wzhongyou/llmgate/core/providers/deepseek"
    _ "github.com/wzhongyou/llmgate/core/providers/glm"
)

func main() {
    // 步骤二：创建 gateway（三选一）
    gw, err := llmgate.NewFromFile("llmgate.toml")
    if err != nil {
        panic(err)
    }

    // 步骤三：对话
    ctx := context.Background()
    reply, err := gw.Chat(ctx, &llmgate.ChatRequest{
        Messages: []llmgate.Message{
            {Role: "user", Content: "帮我写一个 Go HTTP server"},
        },
    })
    if err != nil {
        panic(err)
    }
    fmt.Printf("[%s] %s\n", reply.Provider, reply.Content)
}
```

---

## API

```go
gw := llmgate.New()

// 注册 provider
gw.Use("deepseek", "sk-xxx")
gw.Use("anthropic", "sk-xxx")

// 指定 provider
reply, _ := gw.With("anthropic").Chat(ctx, req)

// 降级链
reply, _ := gw.Fallback("anthropic", "deepseek").Chat(ctx, req)

// 流式输出（SSE）
ch, err := gw.ChatStream(ctx, &llmgate.ChatRequest{
    Messages: []llmgate.Message{{Role: "user", Content: "你好"}},
})
if err != nil {
    return
}
for chunk := range ch {
    if chunk.Error != nil {
        fmt.Println("stream error:", chunk.Error)
        return
    }
    fmt.Print(chunk.Content)
}

// 指标
snap := gw.Snapshot()
fmt.Printf("DeepSeek 延迟: %.2f ms\n", snap.Providers["deepseek"].AvgLatencyMs)

// 自定义策略（需要 import core）
// gw.UseStrategy(&core.PrimaryFirstStrategy{...})
```

**优先级（从高到低）：**
1. `.Fallback(...)` — 代码中显式指定降级链
2. `.With(...)` — 固定使用某个 provider
3. `UseStrategy(...)` — 自定义策略
4. 自动检测（llmgate.toml → 环境变量 → 代码）

---

## Server 模式

独立部署 HTTP 服务，多语言接入：

```bash
cp llmgate.toml.example llmgate.toml
go run examples/server/main.go
```

```toml
# llmgate.toml
[[providers]]
name = "glm"
key = "${GLM_KEY}"
default_model = "glm-5.1"
# base_url = "https://open.bigmodel.cn/api/paas/v4"

[[providers]]
name = "deepseek"
key = "${DEEPSEEK_KEY}"
default_model = "deepseek-v4-flash"

[strategy]
primary = "glm"
fallback = ["deepseek"]
latency_threshold_ms = 5000

[server]
listen_addr = ":8080"
```

接口：
- `POST /v1/chat` — 对话（支持 `?provider=` / `?fallback=` 查询参数）
- `GET /v1/models` — 可用模型列表
- `GET /health` — 健康检查

---

## 可观测性

每次 `/v1/chat` 请求输出一条结构化 JSON 日志，包含性能、Token 明细全部关键字段：

```json
{"time":"...","level":"INFO","msg":"request",
 "request_id":"1747123456789","method":"POST","path":"/v1/chat",
 "status":200,"latency_ms":312.5,"remote_addr":"127.0.0.1:54321",
 "provider":"glm","model":"glm-5.1",
 "input_tokens":15,"output_tokens":42,"reasoning_tokens":0}
```

Server 模式下注入自定义 logger：

```go
logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
srv, _ := server.New(cfg, server.WithLogger(logger))
```

---

## 支持的供应商

| 供应商 | 协议 | 默认模型 |
|--------|------|----------|
| Anthropic (Claude) | Anthropic Messages API | `claude-sonnet-4-6` |
| 百度（文心 ERNIE） | OpenAI 兼容 | `ernie-5.1` |
| DeepSeek | OpenAI 兼容 | `deepseek-v4-flash` |
| Google (Gemini) | Gemini generateContent | `gemini-3.1-flash` |
| Meta (Llama) | OpenAI 兼容 | `llama-4-maverick` |
| MiniMax | OpenAI 兼容 | `MiniMax-M2.7` |
| 月之暗面（Kimi） | OpenAI 兼容 | `kimi-k2.6` |
| OpenAI | OpenAI 兼容 | `gpt-5.5` |
| 阿里百炼（通义千问） | OpenAI 兼容 | `qwen3.6-plus` |
| 阶跃星辰（StepFun） | OpenAI 兼容 | `step-3.5-flash` |
| 腾讯（混元） | OpenAI 兼容 | `hy3-preview` |
| xAI (Grok) | OpenAI 兼容 | `grok-4.1-fast-non-reasoning` |
| 小米（MiMo） | OpenAI 兼容 | `mimo-v2-pro` |
| 智谱（GLM） | OpenAI 兼容 | `glm-5.1` |

**3 套协议**：OpenAI 兼容（12 个供应商）、Anthropic Messages、Gemini generateContent。

所有供应商均支持 `base_url` 覆盖，用于代理、私有部署或第三方转售。

---

## 项目结构

```
llmgate/
├── core/                 # Provider 接口、引擎、策略、指标
├── sdk/                  # Go SDK
├── server/               # HTTP 服务
├── docs/                 # 设计文档
└── examples/             # 使用示例
```

---

## 运行测试

```bash
# 1. 配置 key
cp llmgate.toml.example llmgate.toml
# 填入真实 key，或直接设置环境变量：
# export GLM_KEY=xxx  MINIMAX_KEY=xxx  DEEPSEEK_KEY=xxx

# 2. 运行集成测试
go test ./sdk/ ./server/ -v -count=1
```

未配置 key 时测试自动跳过。

---

## 路线图

- [x] **v0.1** — Go SDK + DeepSeek + 基础降级策略 + 指标采集
- [x] **v0.2** — 智谱（GLM）+ MiniMax + 结构化日志（slog）
- [x] **v0.3** — 14 家供应商 / 3 套协议全覆盖，推理 token 追踪，默认模型可配置
- [x] **v1.0** — Streaming（SSE）+ 生产级路由策略（熔断、限流、重试）
- [ ] **v1.5** — 可视化控制台：延迟分布、Prompt 版本管理、模型对比评估

---

## 新增 Provider

1. 实现 `Provider` 接口（参考 [adapter-template.md](docs/adapter-template.md)）
2. 通过 `init()` 注册:
   ```go
   func init() {
       core.RegisterProvider("name", factory)
   }
   ```
3. 在 `sdk/gateway.go` 的 env map 中添加对应条目
4. 提 PR 并附上测试

---

## License

[MIT](LICENSE)
