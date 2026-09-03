package ai

import (
	"net/http"
	"sync"

	"github.com/opsorigin/core/pkg/httputil"
	"github.com/opsorigin/core/pkg/llm"
)

// 用户自定义/覆盖的厂商预设。由系统设置(OPSORIGIN_LLM_PRESETS)热更新时替换,
// 启动时由 main.go 用配置文件中的值初始化。读写都用 presetsMu 保护,
// 避免与热更新并发时出现数据竞争。
var (
	presetsMu     sync.RWMutex
	customPresets []llm.Preset
)

// SetCustomPresets 热替换用户自定义厂商预设。
// 同名项覆盖内置预设的字段(用于修正私有化部署/内网网关的接入参数), 不同名项作为新厂商追加。
func SetCustomPresets(ps []llm.Preset) {
	presetsMu.Lock()
	customPresets = ps
	presetsMu.Unlock()
}

// customPresetsSnapshot 返回自定义预设的副本。
func customPresetsSnapshot() []llm.Preset {
	presetsMu.RLock()
	defer presetsMu.RUnlock()
	if len(customPresets) == 0 {
		return nil
	}
	out := make([]llm.Preset, len(customPresets))
	copy(out, customPresets)
	return out
}

// handleListLLMPresets 返回「内置 + 用户自定义」合并后的 LLM 厂商预设列表。
// 前端设置页据此渲染服务商下拉, 选中后自动填充 Base URL / 模型 / Embedding 模型,
// 避免用户手工填写各家不同的 OpenAI 兼容端点(智谱是 /v4、千帆是 /v2 等)。
//
// GET /api/v1/ai/llm-presets
func (s *Service) handleListLLMPresets(w http.ResponseWriter, r *http.Request) {
	presets := llm.MergePresets(customPresetsSnapshot())
	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"presets": presets,
		"total":   len(presets),
	})
}
