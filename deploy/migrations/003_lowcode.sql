-- OpsOrigin Core — 低代码引擎表
-- 创建低代码引擎所需的数据库表

-- ============================================================
-- 低代码：数据模型定义表
-- ============================================================
CREATE TABLE IF NOT EXISTS lowcode_models (
    id          TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL DEFAULT 'default',
    name        TEXT NOT NULL,
    description TEXT,
    table_name  TEXT NOT NULL,
    schema      JSONB NOT NULL DEFAULT '[]',
    labels      JSONB DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_lowcode_models_tenant ON lowcode_models(tenant_id);
CREATE INDEX idx_lowcode_models_name ON lowcode_models(name);

-- 行级安全策略 (RLS) — 多租户隔离
ALTER TABLE lowcode_models ENABLE ROW LEVEL SECURITY;
CREATE POLICY lowcode_models_tenant_isolation ON lowcode_models
    USING (tenant_id = current_setting('app.tenant_id')::TEXT);

-- ============================================================
-- 低代码：模型实例表
-- ============================================================
CREATE TABLE IF NOT EXISTS lowcode_instances (
    id          TEXT PRIMARY KEY,
    model_id    TEXT NOT NULL,
    tenant_id   TEXT NOT NULL DEFAULT 'default',
    data        JSONB NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_lowcode_instances_model ON lowcode_instances(model_id);
CREATE INDEX idx_lowcode_instances_tenant ON lowcode_instances(tenant_id);

-- 行级安全策略 (RLS) — 多租户隔离
ALTER TABLE lowcode_instances ENABLE ROW LEVEL SECURITY;
CREATE POLICY lowcode_instances_tenant_isolation ON lowcode_instances
    USING (tenant_id = current_setting('app.tenant_id')::TEXT);

-- ============================================================
-- 低代码：页面定义表
-- ============================================================
CREATE TABLE IF NOT EXISTS lowcode_pages (
    id          TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL DEFAULT 'default',
    name        TEXT NOT NULL,
    description TEXT,
    type        TEXT NOT NULL DEFAULT 'form',
    model_id    TEXT,
    components  JSONB NOT NULL DEFAULT '[]',
    layout      JSONB,
    labels      JSONB DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_lowcode_pages_tenant ON lowcode_pages(tenant_id);
CREATE INDEX idx_lowcode_pages_type ON lowcode_pages(type);
CREATE INDEX idx_lowcode_pages_model ON lowcode_pages(model_id);

-- 行级安全策略 (RLS) — 多租户隔离
ALTER TABLE lowcode_pages ENABLE ROW LEVEL SECURITY;
CREATE POLICY lowcode_pages_tenant_isolation ON lowcode_pages
    USING (tenant_id = current_setting('app.tenant_id')::TEXT);

-- ============================================================
-- 低代码：UI组件库表
-- ============================================================
CREATE TABLE IF NOT EXISTS lowcode_components (
    id          TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL DEFAULT 'default',
    name        TEXT NOT NULL,
    type        TEXT NOT NULL,
    category    TEXT NOT NULL DEFAULT 'basic',
    icon        TEXT,
    props_schema JSONB NOT NULL DEFAULT '{}',
    default_props JSONB DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_lowcode_components_tenant ON lowcode_components(tenant_id);
CREATE INDEX idx_lowcode_components_type ON lowcode_components(type);
CREATE INDEX idx_lowcode_components_category ON lowcode_components(category);

-- 行级安全策略 (RLS) — 多租户隔离
ALTER TABLE lowcode_components ENABLE ROW LEVEL SECURITY;
CREATE POLICY lowcode_components_tenant_isolation ON lowcode_components
    USING (tenant_id = current_setting('app.tenant_id')::TEXT);

-- ============================================================
-- 低代码：表单验证规则表
-- ============================================================
CREATE TABLE IF NOT EXISTS lowcode_form_rules (
    id          TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL DEFAULT 'default',
    name        TEXT NOT NULL,
    model_id    TEXT,
    rules       JSONB NOT NULL DEFAULT '[]',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_lowcode_form_rules_tenant ON lowcode_form_rules(tenant_id);
CREATE INDEX idx_lowcode_form_rules_model ON lowcode_form_rules(model_id);

-- 行级安全策略 (RLS) — 多租户隔离
ALTER TABLE lowcode_form_rules ENABLE ROW LEVEL SECURITY;
CREATE POLICY lowcode_form_rules_tenant_isolation ON lowcode_form_rules
    USING (tenant_id = current_setting('app.tenant_id')::TEXT);

-- ============================================================
-- 初始化默认UI组件
-- ============================================================
INSERT INTO lowcode_components (id, tenant_id, name, type, category, icon, props_schema, default_props) VALUES
-- 基础组件
('comp-input', 'default', '输入框', 'input', 'basic', 'Edit', '{"placeholder":{"type":"string"},"type":{"type":"string","enum":["text","number","password","email"]}}', '{"type":"text"}'),
('comp-textarea', 'default', '文本域', 'textarea', 'basic', 'AlignLeft', '{"rows":{"type":"number"}}', '{"rows":3}'),
('comp-select', 'default', '下拉选择', 'select', 'basic', 'ChevronDown', '{"options":{"type":"array"}}', '{}'),
('comp-radio', 'default', '单选框', 'radio', 'basic', 'Circle', '{"options":{"type":"array"}}', '{}'),
('comp-checkbox', 'default', '复选框', 'checkbox', 'basic', 'CheckSquare', '{}', '{}'),
('comp-switch', 'default', '开关', 'switch', 'basic', 'ToggleLeft', '{}', '{}'),
('comp-date', 'default', '日期选择', 'date', 'basic', 'Calendar', '{"format":{"type":"string"}}', '{"format":"YYYY-MM-DD"}'),
('comp-time', 'default', '时间选择', 'time', 'basic', 'Clock', '{"format":{"type":"string"}}', '{"format":"HH:mm:ss"}'),
('comp-datetime', 'default', '日期时间', 'datetime', 'basic', 'CalendarClock', '{}', '{}'),
('comp-number', 'default', '数字输入', 'number', 'basic', 'Hash', '{"min":{"type":"number"},"max":{"type":"number"}}', '{}'),
('comp-slider', 'default', '滑块', 'slider', 'basic', 'Sliders', '{"min":{"type":"number"},"max":{"type":"number"}}', '{"min":0,"max":100}'),
('comp-rate', 'default', '评分', 'rate', 'basic', 'Star', '{"count":{"type":"number"}}', '{"count":5}'),
-- 布局组件
('comp-row', 'default', '行', 'row', 'layout', 'Rows', '{"gutter":{"type":"number"}}', '{"gutter":16}'),
('comp-col', 'default', '列', 'col', 'layout', 'Columns', '{"span":{"type":"number"}}', '{"span":12}'),
('comp-card', 'default', '卡片', 'card', 'layout', 'Square', '{"title":{"type":"string"}}', '{}'),
('comp-tabs', 'default', '标签页', 'tabs', 'layout', 'LayoutList', '{"items":{"type":"array"}}', '{}'),
('comp-collapse', 'default', '折叠面板', 'collapse', 'layout', 'ChevronDown', '{"items":{"type":"array"}}', '{}'),
-- 高级组件
('comp-table', 'default', '表格', 'table', 'advanced', 'Table', '{"columns":{"type":"array"}}', '{}'),
('comp-pagination', 'default', '分页', 'pagination', 'advanced', 'ChevronsLeftRight', '{"total":{"type":"number"}}', '{"pageSize":20}'),
('comp-upload', 'default', '上传', 'upload', 'advanced', 'Upload', '{"accept":{"type":"string"}}', '{}'),
('comp-tree', 'default', '树形控件', 'tree', 'advanced', 'GitBranch', '{"data":{"type":"array"}}', '{}'),
('comp-transfer', 'default', '穿梭框', 'transfer', 'advanced', 'ArrowLeftRight', '{"dataSource":{"type":"array"}}', '{}'),
('comp-mention', 'default', '提及', 'mention', 'advanced', 'AtSign', '{}', '{}')
ON CONFLICT (id) DO NOTHING;