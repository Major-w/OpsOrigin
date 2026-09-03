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
}

// builtinPresets 内置厂商预设。所有 BaseURL 均取自厂商官方文档的 OpenAI 兼容基址。
var builtinPresets = []Preset{
	{
		Name: "openai", Label: "OpenAI", BaseURL: "https://api.openai.com/v1",
		DefaultModel: "gpt-4o-mini", EmbeddingModel: "text-embedding-3-small",
		Doc: "https://platform.openai.com/api-keys", Weight: 60,
	},
	{
		Name: "deepseek", Label: "DeepSeek", BaseURL: "https://api.deepseek.com",
		DefaultModel: "deepseek-chat",
		Doc:          "https://platform.deepseek.com/api_keys", Weight: 60,
	},
	{
		Name: "qwen", Label: "通义千问(阿里百炼)", BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1",
		DefaultModel: "qwen-plus", EmbeddingModel: "text-embedding-v3",
		Doc: "https://bailian.console.aliyun.com/?tab=model#/api-key", Weight: 60,
	},
	{
		Name: "zhipu", Label: "智谱 GLM", BaseURL: "https://open.bigmodel.cn/api/paas/v4",
		DefaultModel: "glm-4.6v-flash", EmbeddingModel: "embedding-3",
		Doc: "https://open.bigmodel.cn/usercenter/apikeys", Weight: 60,
	},
	{
		Name: "kimi", Label: "Kimi(月之暗面)", BaseURL: "https://api.moonshot.cn/v1",
		DefaultModel: "moonshot-v1-8k",
		Doc:          "https://platform.moonshot.cn/console/api-keys", Weight: 60,
	},
	{
		Name: "doubao", Label: "豆包(火山方舟)", BaseURL: "https://ark.cn-beijing.volces.com/api/v3",
		DefaultModel: "doubao-seed-1-6-251015",
		Doc:          "https://console.volcengine.com/ark/region:ark+cn-beijing/apikey", Weight: 60,
	},
	{
		Name: "qianfan", Label: "文心(百度千帆)", BaseURL: "https://qianfan.baidubce.com/v2",
		DefaultModel: "ernie-4.0-turbo-8k",
		Doc:          "https://console.bce.baidu.com/qianfan/ais/console/apiKey", Weight: 60,
	},
	{
		Name: "hunyuan", Label: "腾讯混元", BaseURL: "https://api.hunyuan.cloud.tencent.com/v1",
		DefaultModel: "hunyuan-turbo",
		Doc:          "https://console.cloud.tencent.com/hunyuan/api-key", Weight: 60,
	},
	{
		Name: "siliconflow", Label: "硅基流动", BaseURL: "https://api.siliconflow.cn/v1",
		DefaultModel: "Qwen/Qwen2.5-7B-Instruct", EmbeddingModel: "BAAI/bge-m3",
		Doc: "https://cloud.siliconflow.cn/account/ak", Weight: 60,
	},
	{
		Name: "ollama", Label: "Ollama 本地", BaseURL: "http://host.docker.internal:11434/v1",
		DefaultModel: "qwen2.5", EmbeddingModel: "nomic-embed-text",
		Doc: "https://ollama.com", Weight: 60,
	},
	{
		Name: "openrouter", Label: "OpenRouter", BaseURL: "https://openrouter.ai/api/v1",
		DefaultModel: "openai/gpt-4o-mini",
		Doc:          "https://openrouter.ai/keys", Weight: 50,
	},
	{
		Name: "groq", Label: "Groq", BaseURL: "https://api.groq.com/openai/v1",
		DefaultModel: "llama-3.3-70b-versatile",
		Doc:          "https://console.groq.com/keys", Weight: 50,
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
