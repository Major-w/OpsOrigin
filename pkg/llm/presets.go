package llm

// Preset 描述一个 LLM 服务商的默认接入参数(OpenAI 兼容端点)。
//
// 设计要点:
//   - BaseURL 只填「OpenAI 兼容基址」, 不要带 /chat/completions —— 请求路径由
//     joinEndpoint 按 BaseURL 末尾是否含版本段自动补全(见 client.go)。
//     例如智谱填 https://open.bigmodel.cn/api/paas/v4 而非 .../v4/v1。
//   - EmbeddingModel 留空表示该厂商不提供向量接口, 此时知识库语义检索会自动
//     降级为关键词匹配, 前端应提示用户单独配置 embedding 服务商。
//   - 内置项不可删除; 用户自定义预设若与内置项同名, 则覆盖内置项(可改不可删)。
type Preset struct {
	Name             string `json:"name"`                         // 唯一标识, 如 zhipu / deepseek
	Label            string `json:"label"`                        // 展示名, 如「智谱 GLM」
	BaseURL          string `json:"base_url"`                     // OpenAI 兼容基址
	DefaultModel     string `json:"default_model"`                // 推荐聊天模型
	EmbeddingModel   string `json:"embedding_model"`              // 推荐向量模型, 空=不提供
	EmbeddingBaseURL string `json:"embedding_base_url,omitempty"` // 空=复用 BaseURL
	Doc              string `json:"doc"`                          // 申请 Key / 文档地址
	Weight           int    `json:"weight"`                       // 加入模型池时的默认权重
	Builtin          bool   `json:"builtin"`                      // true=内置, false=用户自定义
	// Models 该厂商常见模型 ID 清单(供前端"增加模型"时下拉选择, 避免手敲)。
	// 不含全部模型, 用户仍可在下拉中手动输入任意自定义模型 ID。
	Models []string `json:"models"`
}

// builtinPresets 内置厂商预设。所有 BaseURL 均取自厂商官方文档的 OpenAI 兼容基址。
// Models 清单随主流厂商模型迭代定期更新(最近核对: 2026-09)。
var builtinPresets = []Preset{
	{
		Name: "openai", Label: "OpenAI", BaseURL: "https://api.openai.com/v1",
		DefaultModel: "gpt-5.5", EmbeddingModel: "text-embedding-3-small",
		Doc: "https://platform.openai.com/api-keys", Weight: 60,
		Models: []string{"gpt-5.6", "gpt-5.5", "gpt-5.4", "gpt-5.2", "o3-mini", "text-embedding-3-small", "text-embedding-3-large"},
	},
	{
		Name: "deepseek", Label: "DeepSeek", BaseURL: "https://api.deepseek.com",
		DefaultModel: "deepseek-v4-flash",
		Doc:          "https://platform.deepseek.com/api_keys", Weight: 60,
		// 旧名 deepseek-chat / deepseek-reasoner 已于 2026-07 退役, 由 V4 系列取代
		Models: []string{"deepseek-v4-flash", "deepseek-v4-pro", "deepseek-v4-flash-vision-exp"},
	},
	{
		Name: "qwen", Label: "通义千问(阿里百炼)", BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1",
		DefaultModel: "qwen3.7-plus", EmbeddingModel: "text-embedding-v4",
		Doc: "https://bailian.console.aliyun.com/?tab=model#/api-key", Weight: 60,
		Models: []string{"qwen3.8-max", "qwen3.8-flash", "qwen3.8-flash-next", "qwen3.7-max", "qwen3.7-plus", "qwen3.7-flash", "qwen-long", "text-embedding-v4", "text-embedding-v3"},
	},
	{
		Name: "zhipu", Label: "智谱 GLM", BaseURL: "https://open.bigmodel.cn/api/paas/v4",
		DefaultModel: "glm-5.3-flash", EmbeddingModel: "embedding-3",
		Doc: "https://open.bigmodel.cn/usercenter/apikeys", Weight: 60,
		// cogview-3 / cogview-3-flash: 智谱图像生成模型(POST /images/generations, 非对话流);
		// 通过 MCP 工具 image_generate 调用, 计入模型池时 Types=[image] 与文本模型区分。
		Models: []string{"glm-5.3", "glm-5.3-flash", "glm-5.2", "glm-5.1", "glm-5", "glm-5-turbo", "glm-5v-turbo", "glm-4.7", "glm-4.7-flash", "glm-4.6v", "glm-4.6v-flash", "glm-4.5v", "glm-4.5-air", "embedding-3", "cogview-3", "cogview-3-flash"},
	},
	{
		Name: "kimi", Label: "Kimi(月之暗面)", BaseURL: "https://api.moonshot.cn/v1",
		DefaultModel: "kimi-k3",
		Doc:          "https://platform.moonshot.cn/console/api-keys", Weight: 60,
		Models: []string{"kimi-k3", "kimi-k2.7", "kimi-k2.7-code"},
	},
	{
		Name: "doubao", Label: "豆包(火山方舟)", BaseURL: "https://ark.cn-beijing.volces.com/api/v3",
		DefaultModel: "doubao-seed-evolving",
		Doc:          "https://console.volcengine.com/ark/region:ark+cn-beijing/apikey", Weight: 60,
		// seed-evolving 为统一 Model ID 持续滚动升级, 无需关注版本号
		Models: []string{"doubao-seed-evolving", "doubao-seed-2-1-pro-260628", "doubao-seed-2-1-turbo-260628"},
	},
	{
		Name: "qianfan", Label: "文心(百度千帆)", BaseURL: "https://qianfan.baidubce.com/v2",
		DefaultModel: "ernie-4.0-turbo-8k",
		Doc:          "https://console.bce.baidu.com/qianfan/ais/console/apiKey", Weight: 60,
		Models: []string{"ernie-4.0-turbo-8k", "ernie-4.0-8k", "ernie-3.5-8k", "ernie-speed-8k", "ernie-lite-8k", "ernie-4.5-8k-preview"},
	},
	{
		Name: "hunyuan", Label: "腾讯混元", BaseURL: "https://api.hunyuan.cloud.tencent.com/v1",
		DefaultModel: "hunyuan-turbo",
		Doc:          "https://console.cloud.tencent.com/hunyuan/api-key", Weight: 60,
		Models: []string{"hunyuan-turbo", "hunyuan-turbo-latest", "hunyuan-pro", "hunyuan-standard", "hunyuan-vision", "hunyuan-embedding"},
	},
	{
		Name: "siliconflow", Label: "硅基流动", BaseURL: "https://api.siliconflow.cn/v1",
		DefaultModel: "deepseek-ai/DeepSeek-V4-Flash", EmbeddingModel: "BAAI/bge-m3",
		Doc: "https://cloud.siliconflow.cn/account/ak", Weight: 60,
		Models: []string{"deepseek-ai/DeepSeek-V4-Flash", "deepseek-ai/DeepSeek-V4-Pro", "Qwen/Qwen3.8-Flash", "zai-org/GLM-5.2", "moonshotai/Kimi-K3", "meta-llama/Llama-3.3-70B-Instruct", "BAAI/bge-m3", "BAAI/bge-large-zh-v1.5"},
	},
	{
		Name: "ollama", Label: "Ollama 本地", BaseURL: "http://host.docker.internal:11434/v1",
		DefaultModel: "qwen3", EmbeddingModel: "nomic-embed-text",
		Doc: "https://ollama.com", Weight: 60,
		Models: []string{"qwen3", "qwen2.5", "glm-4.6", "deepseek-r1", "llama3.1", "mistral", "nomic-embed-text", "bge-m3"},
	},
	{
		Name: "openrouter", Label: "OpenRouter", BaseURL: "https://openrouter.ai/api/v1",
		DefaultModel: "openai/gpt-5.5",
		Doc:          "https://openrouter.ai/keys", Weight: 50,
		Models: []string{"openai/gpt-5.6", "openai/gpt-5.5", "deepseek/deepseek-v4-flash", "deepseek/deepseek-v4-pro", "z-ai/glm-5.2", "moonshotai/kimi-k3", "google/gemini-3.6-flash", "meta-llama/llama-3.3-70b-instruct"},
	},
	{
		Name: "groq", Label: "Groq", BaseURL: "https://api.groq.com/openai/v1",
		DefaultModel: "llama-3.3-70b-versatile",
		Doc:          "https://console.groq.com/keys", Weight: 50,
		Models: []string{"llama-3.3-70b-versatile", "llama-3.1-8b-instant", "mixtral-8x7b-32768", "gemma2-9b-it"},
	},
	{
		// 自定义: 不预填任何参数, 用户手动填写 BaseURL / 模型 ID(任意 OpenAI 兼容端点)
		Name: "custom", Label: "自定义服务商", BaseURL: "",
		DefaultModel: "", Doc: "", Weight: 50,
	},
}

// BuiltinPresets 返回内置厂商预设的副本(调用方修改不会影响注册表)。
func BuiltinPresets() []Preset {
	out := make([]Preset, 0, len(builtinPresets))
	for _, p := range builtinPresets {
		p.Builtin = true
		out = append(out, p)
	}
	return out
}

// MergePresets 合并「内置预设」与「用户自定义预设」。
// 同名时以自定义为准(实现覆盖), 自定义中新增的名称追加到末尾;
// 返回结果保持内置预设在前, 便于 UI 下拉把常用厂商排在前面。
func MergePresets(custom []Preset) []Preset {
	merged := BuiltinPresets()
	idx := make(map[string]int, len(merged))
	for i, p := range merged {
		idx[p.Name] = i
	}
	for _, p := range custom {
		if p.Name == "" {
			continue
		}
		p.Builtin = false
		if i, ok := idx[p.Name]; ok {
			// 覆盖内置项: 保留内置标记, 其余字段用自定义值
			p.Builtin = merged[i].Builtin
			p.Label = orString(p.Label, merged[i].Label)
			merged[i] = p
			continue
		}
		idx[p.Name] = len(merged)
		merged = append(merged, p)
	}
	return merged
}

// GetPreset 按名称查找预设(内置与自定义合并后)。
func GetPreset(name string, custom []Preset) (Preset, bool) {
	for _, p := range MergePresets(custom) {
		if p.Name == name {
			return p, true
		}
	}
	return Preset{}, false
}

func orString(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
