package agentic

// AutonomyLevel 代理自治分级(对齐行业四档模型):
//   - notify:            仅通知人工(不生成建议, 不执行);
//   - suggest:           生成处置建议供人工参考(不执行);
//   - act_with_approval: 执行但高危动作走审批闸门(默认);
//   - autonomous:        自主执行(仅保留 blocked 危险词拦截)。
type AutonomyLevel string

const (
	AutonomyNotify          AutonomyLevel = "notify"
	AutonomySuggest         AutonomyLevel = "suggest"
	AutonomyActWithApproval AutonomyLevel = "act_with_approval"
	AutonomyAutonomous      AutonomyLevel = "autonomous"
)

// DefaultAutonomy 默认档位: 审批后执行(平衡自治与安全)。
const DefaultAutonomy = AutonomyActWithApproval

// SetAutonomy 设置引擎级自治档位(线程安全)。
func (e *Engine) SetAutonomy(level AutonomyLevel) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if level == "" {
		level = DefaultAutonomy
	}
	e.autonomy = level
}

// GetAutonomy 返回当前自治档位(未设置时返回默认)。
func (e *Engine) GetAutonomy() AutonomyLevel {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.autonomy == "" {
		return DefaultAutonomy
	}
	return e.autonomy
}
