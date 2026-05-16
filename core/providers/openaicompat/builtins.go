package openaicompat

import "github.com/wzhongyou/llmgate/core"

type providerDef struct {
	name         string
	baseURL      string
	defaultModel string
	models       []string
	envVar       string
	bodyHook     func(map[string]interface{})
}

var builtins = []providerDef{
	{
		name:         "baichuan",
		baseURL:      "https://api.baichuan-ai.com/v1",
		defaultModel: "Baichuan4",
		models:       []string{"Baichuan4", "Baichuan4-Turbo", "Baichuan4-Air"},
		envVar:       "BAICHUAN_KEY",
	},
	{
		name:         "deepseek",
		baseURL:      "https://api.deepseek.com/v1",
		defaultModel: "deepseek-v4-flash",
		models:       []string{"deepseek-v4-flash", "deepseek-v4", "deepseek-r2"},
		envVar:       "DEEPSEEK_KEY",
	},
	{
		name:         "doubao",
		baseURL:      "https://ark.cn-beijing.volces.com/api/v3",
		defaultModel: "doubao-seed-1.6-250615",
		models:       []string{"doubao-seed-1.6-250615", "doubao-pro-32k", "doubao-lite-32k"},
		envVar:       "DOUBAO_KEY",
	},
	{
		name:         "ernie",
		baseURL:      "https://qianfan.baidubce.com/v2",
		defaultModel: "ernie-5.1",
		models:       []string{"ernie-5.1", "ernie-4.5"},
		envVar:       "ERNIE_KEY",
	},
	{
		name:         "glm",
		baseURL:      "https://open.bigmodel.cn/api/paas/v4",
		defaultModel: "glm-5.1",
		models:       []string{"glm-5.1", "glm-5"},
		envVar:       "GLM_KEY",
			bodyHook:     glmBodyHook,
		},
	{
		name:         "grok",
		baseURL:      "https://api.x.ai/v1",
		defaultModel: "grok-4.1-fast-non-reasoning",
		models:       []string{"grok-4.1", "grok-4.1-fast-non-reasoning"},
		envVar:       "GROK_KEY",
	},
	{
		name:         "groq",
		baseURL:      "https://api.groq.com/openai/v1",
		defaultModel: "llama-3.3-70b-versatile",
		models:       []string{"llama-3.3-70b-versatile", "mixtral-8x7b-32768"},
		envVar:       "GROQ_KEY",
	},
	{
		name:         "hunyuan",
		baseURL:      "https://api.hunyuan.cloud.tencent.com/v1",
		defaultModel: "hy3-preview",
		models:       []string{"hy3-preview", "hunyuan-turbos"},
		envVar:       "HUNYUAN_KEY",
	},
	{
		name:         "kimi",
		baseURL:      "https://api.moonshot.cn/v1",
		defaultModel: "kimi-k2.6",
		models:       []string{"kimi-k2.6", "moonshot-v1-8k"},
		envVar:       "KIMI_KEY",
	},
	{
		name:         "llama",
		baseURL:      "https://api.llama.com/v1",
		defaultModel: "llama-4-maverick",
		models:       []string{"llama-4-maverick", "llama-4-scout"},
		envVar:       "LLAMA_KEY",
	},
	{
		name:         "mimo",
		baseURL:      "https://api.xiaomimimo.com/v1",
		defaultModel: "mimo-v2-pro",
		models:       []string{"mimo-v2-pro"},
		envVar:       "MIMO_KEY",
	},
	{
		name:         "minimax",
		baseURL:      "https://api.minimaxi.com/v1",
		defaultModel: "MiniMax-M2.7",
		models:       []string{"MiniMax-M2.7", "MiniMax-Text-01"},
		envVar:       "MINIMAX_KEY",
	},
	{
		name:         "mistral",
		baseURL:      "https://api.mistral.ai/v1",
		defaultModel: "mistral-large-latest",
		models:       []string{"mistral-large-latest", "mistral-small-latest"},
		envVar:       "MISTRAL_KEY",
	},
	{
		name:         "openai",
		baseURL:      "https://api.openai.com/v1",
		defaultModel: "gpt-5.5",
		models:       []string{"gpt-5.5", "gpt-4o"},
		envVar:       "OPENAI_KEY",
	},
	{
		name:         "qwen",
		baseURL:      "https://dashscope.aliyuncs.com/compatible-mode/v1",
		defaultModel: "qwen3.6-plus",
		models:       []string{"qwen3.6-plus", "qwen-max"},
		envVar:       "QWEN_KEY",
	},
	{
		name:         "siliconflow",
		baseURL:      "https://api.siliconflow.cn/v1",
		defaultModel: "Qwen/Qwen2.5-72B-Instruct",
		models:       []string{"Qwen/Qwen2.5-72B-Instruct", "deepseek-ai/DeepSeek-R1"},
		envVar:       "SILICONFLOW_KEY",
	},
	{
		name:         "stepfun",
		baseURL:      "https://api.stepfun.com/v1",
		defaultModel: "step-3.5-flash",
		models:       []string{"step-3.5-flash", "step-3-mini"},
		envVar:       "STEPFUN_KEY",
	},
	{
		name:         "together",
		baseURL:      "https://api.together.xyz/v1",
		defaultModel: "meta-llama/Meta-Llama-3.1-70B-Instruct-Turbo",
		models:       []string{"meta-llama/Meta-Llama-3.1-70B-Instruct-Turbo"},
		envVar:       "TOGETHER_KEY",
	},
	{
		name:         "yi",
		baseURL:      "https://api.lingyiwanwu.com/v1",
		defaultModel: "yi-large",
		models:       []string{"yi-large", "yi-medium"},
		envVar:       "YI_KEY",
	},
}

func init() {
	for _, def := range builtins {
		d := def
		core.RegisterProvider(d.name, func(cfg core.ProviderConfig) (core.Provider, error) {
			baseURL := cfg.BaseURL
			if baseURL == "" {
				baseURL = d.baseURL
			}
			defaultModel := cfg.DefaultModel
			if defaultModel == "" {
				defaultModel = d.defaultModel
			}
			return &Provider{
				name:         d.name,
				key:          cfg.Key,
				baseURL:      baseURL,
				defaultModel: defaultModel,
				models:       d.models,
				BodyHook:     d.bodyHook,
			}, nil
		})
		core.RegisterProviderEnv(d.envVar, d.name)
	}

	// Generic factory for user-defined OpenAI-compatible providers via config protocol = "openai-compat"
	core.RegisterProvider("openai-compat", func(cfg core.ProviderConfig) (core.Provider, error) {
		return &Provider{
			name:         cfg.Name,
			key:          cfg.Key,
			baseURL:      cfg.BaseURL,
			defaultModel: cfg.DefaultModel,
		}, nil
	})
}

// glmBodyHook adapts request body for GLM API.
// GLM does not support OpenAI's "thinking" param; it uses "enable_thinking" instead.
func glmBodyHook(body map[string]interface{}) {
	if _, ok := body["thinking"]; ok {
		delete(body, "thinking")
		body["enable_thinking"] = false
	}
}
