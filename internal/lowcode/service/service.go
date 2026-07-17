package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/opsorigin/core/internal/lowcode/component"
	"github.com/opsorigin/core/internal/lowcode/model"
	"github.com/opsorigin/core/internal/lowcode/version"
)

// Store 低代码存储接口
type Store interface {
	// 数据模型管理
	CreateDataModel(ctx context.Context, m *model.DataModel) error
	GetDataModel(ctx context.Context, id, tenantID string) (*model.DataModel, error)
	ListDataModels(ctx context.Context, tenantID string, limit, offset int) ([]*model.DataModel, int, error)
	UpdateDataModel(ctx context.Context, m *model.DataModel) error
	DeleteDataModel(ctx context.Context, id, tenantID string) error

	// 模型实例管理
	CreateInstance(ctx context.Context, inst *model.ModelInstance) error
	GetInstance(ctx context.Context, id, tenantID string) (*model.ModelInstance, error)
	ListInstances(ctx context.Context, modelID, tenantID string, limit, offset int) ([]*model.ModelInstance, int, error)
	UpdateInstance(ctx context.Context, inst *model.ModelInstance) error
	DeleteInstance(ctx context.Context, id, tenantID string) error

	// 页面管理
	CreatePage(ctx context.Context, p *model.PageDefinition) error
	GetPage(ctx context.Context, id, tenantID string) (*model.PageDefinition, error)
	ListPages(ctx context.Context, tenantID string, limit, offset int) ([]*model.PageDefinition, int, error)
	UpdatePage(ctx context.Context, p *model.PageDefinition) error
	DeletePage(ctx context.Context, id, tenantID string) error
}

// Service 低代码服务
type Service struct {
	store    Store
	market   *component.MarketService
	version  *version.VersionService
}

// NewService 创建低代码服务
func NewService(store Store) *Service {
	return &Service{
		store:   store,
		market:  component.NewMarketService(),
		version: version.NewVersionService(),
	}
}

// GetMarket 获取组件市场服务
func (s *Service) GetMarket() *component.MarketService {
	return s.market
}

// GetVersion 获取版本管理服务
func (s *Service) GetVersion() *version.VersionService {
	return s.version
}

// ========== 数据模型管理 ==========

// CreateDataModel 创建数据模型
func (s *Service) CreateDataModel(ctx context.Context, m *model.DataModel) error {
	if m.ID == "" {
		m.ID = uuid.New().String()
	}
	if m.TenantID == "" {
		m.TenantID = "default"
	}
	if m.TableName == "" {
		m.TableName = "lc_" + m.Name
	}

	now := time.Now()
	m.CreatedAt = now
	m.UpdatedAt = now

	// 为每个字段生成ID
	for i := range m.Fields {
		if m.Fields[i].ID == "" {
			m.Fields[i].ID = uuid.New().String()
		}
		if m.Fields[i].SortOrder == 0 {
			m.Fields[i].SortOrder = i + 1
		}
	}

	return s.store.CreateDataModel(ctx, m)
}

// GetDataModel 获取数据模型
func (s *Service) GetDataModel(ctx context.Context, id, tenantID string) (*model.DataModel, error) {
	return s.store.GetDataModel(ctx, id, tenantID)
}

// ListDataModels 列出数据模型
func (s *Service) ListDataModels(ctx context.Context, tenantID string, limit, offset int) ([]*model.DataModel, int, error) {
	return s.store.ListDataModels(ctx, tenantID, limit, offset)
}

// UpdateDataModel 更新数据模型
func (s *Service) UpdateDataModel(ctx context.Context, m *model.DataModel) error {
	m.UpdatedAt = time.Now()
	return s.store.UpdateDataModel(ctx, m)
}

// DeleteDataModel 删除数据模型
func (s *Service) DeleteDataModel(ctx context.Context, id, tenantID string) error {
	return s.store.DeleteDataModel(ctx, id, tenantID)
}

// ========== 模型实例管理 ==========

// CreateInstance 创建模型实例
func (s *Service) CreateInstance(ctx context.Context, inst *model.ModelInstance) error {
	if inst.ID == "" {
		inst.ID = uuid.New().String()
	}
	if inst.TenantID == "" {
		inst.TenantID = "default"
	}

	now := time.Now()
	inst.CreatedAt = now
	inst.UpdatedAt = now

	// 验证数据
	if err := s.validateInstanceData(ctx, inst); err != nil {
		return err
	}

	return s.store.CreateInstance(ctx, inst)
}

// GetInstance 获取模型实例
func (s *Service) GetInstance(ctx context.Context, id, tenantID string) (*model.ModelInstance, error) {
	return s.store.GetInstance(ctx, id, tenantID)
}

// ListInstances 列出模型实例
func (s *Service) ListInstances(ctx context.Context, modelID, tenantID string, limit, offset int) ([]*model.ModelInstance, int, error) {
	return s.store.ListInstances(ctx, modelID, tenantID, limit, offset)
}

// UpdateInstance 更新模型实例
func (s *Service) UpdateInstance(ctx context.Context, inst *model.ModelInstance) error {
	inst.UpdatedAt = time.Now()

	// 验证数据
	if err := s.validateInstanceData(ctx, inst); err != nil {
		return err
	}

	return s.store.UpdateInstance(ctx, inst)
}

// DeleteInstance 删除模型实例
func (s *Service) DeleteInstance(ctx context.Context, id, tenantID string) error {
	return s.store.DeleteInstance(ctx, id, tenantID)
}

// validateInstanceData 验证实例数据
func (s *Service) validateInstanceData(ctx context.Context, inst *model.ModelInstance) error {
	m, err := s.store.GetDataModel(ctx, inst.ModelID, inst.TenantID)
	if err != nil {
		return err
	}
	if m == nil {
		return fmt.Errorf("model not found: %s", inst.ModelID)
	}

	// 创建字段映射
	fieldMap := make(map[string]model.FieldDefinition)
	for _, f := range m.Fields {
		fieldMap[f.Name] = f
	}

	// 验证必填字段
	for _, f := range m.Fields {
		if f.Required {
			if _, exists := inst.Data[f.Name]; !exists {
				return fmt.Errorf("field %s is required", f.Name)
			}
		}
	}

	return nil
}

// ========== 页面管理 ==========

// CreatePage 创建页面
func (s *Service) CreatePage(ctx context.Context, p *model.PageDefinition) error {
	if p.ID == "" {
		p.ID = uuid.New().String()
	}
	if p.TenantID == "" {
		p.TenantID = "default"
	}

	now := time.Now()
	p.CreatedAt = now
	p.UpdatedAt = now

	// 为每个组件生成ID
	s.generateComponentIDs(p.Components)

	return s.store.CreatePage(ctx, p)
}

// generateComponentIDs 生成组件ID
func (s *Service) generateComponentIDs(components []model.UIComponent) {
	for i := range components {
		if components[i].ID == "" {
			components[i].ID = uuid.New().String()
		}
		if len(components[i].Children) > 0 {
			s.generateComponentIDs(components[i].Children)
		}
	}
}

// GetPage 获取页面
func (s *Service) GetPage(ctx context.Context, id, tenantID string) (*model.PageDefinition, error) {
	return s.store.GetPage(ctx, id, tenantID)
}

// ListPages 列出页面
func (s *Service) ListPages(ctx context.Context, tenantID string, limit, offset int) ([]*model.PageDefinition, int, error) {
	return s.store.ListPages(ctx, tenantID, limit, offset)
}

// UpdatePage 更新页面
func (s *Service) UpdatePage(ctx context.Context, p *model.PageDefinition) error {
	p.UpdatedAt = time.Now()
	return s.store.UpdatePage(ctx, p)
}

// DeletePage 删除页面
func (s *Service) DeletePage(ctx context.Context, id, tenantID string) error {
	return s.store.DeletePage(ctx, id, tenantID)
}

// ========== 表单引擎 ==========

// ValidateForm 验证表单数据
func (s *Service) ValidateForm(ctx context.Context, data *model.FormData) ([]string, error) {
	var errors []string

	// 获取数据模型
	m, err := s.store.GetDataModel(ctx, data.ModelID, "default")
	if err != nil {
		return nil, err
	}
	if m == nil {
		return nil, fmt.Errorf("model not found: %s", data.ModelID)
	}

	// 创建字段映射
	fieldMap := make(map[string]model.FieldDefinition)
	for _, f := range m.Fields {
		fieldMap[f.Name] = f
	}

	// 验证每个字段
	for fieldName, value := range data.Data {
		field, exists := fieldMap[fieldName]
		if !exists {
			continue
		}

		// 验证必填
		if field.Required && (value == nil || value == "") {
			errors = append(errors, fmt.Sprintf("%s is required", field.Label))
			continue
		}

		// 验证字段类型
		if err := s.validateFieldType(field, value); err != nil {
			errors = append(errors, err.Error())
		}
	}

	return errors, nil
}

// validateFieldType 验证字段类型
func (s *Service) validateFieldType(field model.FieldDefinition, value any) error {
	if value == nil {
		return nil
	}

	switch field.Type {
	case model.FieldTypeNumber:
		// 验证数字类型
		switch v := value.(type) {
		case float64, int, int64:
			// 验证范围
			if field.Validation != nil {
				numVal := toFloat64(v)
				if field.Validation.Min != nil && numVal < *field.Validation.Min {
					return fmt.Errorf("%s must be >= %v", field.Label, *field.Validation.Min)
				}
				if field.Validation.Max != nil && numVal > *field.Validation.Max {
					return fmt.Errorf("%s must be <= %v", field.Label, *field.Validation.Max)
				}
			}
		default:
			return fmt.Errorf("%s must be a number", field.Label)
		}
	case model.FieldTypeText, model.FieldTypeEmail, model.FieldTypePhone:
		// 验证字符串类型
		strVal, ok := value.(string)
		if !ok {
			return fmt.Errorf("%s must be a string", field.Label)
		}
		// 验证长度
		if field.Validation != nil {
			if field.Validation.MinLength != nil && len(strVal) < *field.Validation.MinLength {
				return fmt.Errorf("%s must be >= %d characters", field.Label, *field.Validation.MinLength)
			}
			if field.Validation.MaxLength != nil && len(strVal) > *field.Validation.MaxLength {
				return fmt.Errorf("%s must be <= %d characters", field.Label, *field.Validation.MaxLength)
			}
		}
	case model.FieldTypeSelect, model.FieldTypeMultiSelect:
		// 验证选项
		strVal, ok := value.(string)
		if !ok {
			// 尝试数组
			arrVal, ok := value.([]any)
			if !ok {
				return fmt.Errorf("%s must be a string or array", field.Label)
			}
			for _, v := range arrVal {
				strV, ok := v.(string)
				if !ok {
					return fmt.Errorf("%s must contain strings", field.Label)
				}
				if !s.isValidOption(field.Options, strV) {
					return fmt.Errorf("%s has invalid option: %s", field.Label, strV)
				}
			}
		} else {
			if !s.isValidOption(field.Options, strVal) {
				return fmt.Errorf("%s has invalid option: %s", field.Label, strVal)
			}
		}
	}

	return nil
}

// isValidOption 验证选项是否有效
func (s *Service) isValidOption(options []model.FieldOption, value string) bool {
	for _, opt := range options {
		if opt.Value == value {
			return true
		}
	}
	return len(options) == 0 // 如果没有选项，则任何值都有效
}

// toFloat64 转换为float64
func toFloat64(v any) float64 {
	switch val := v.(type) {
	case float64:
		return val
	case int:
		return float64(val)
	case int64:
		return float64(val)
	default:
		return 0
	}
}

// ========== 工具函数 ==========

// GenerateCreateTableSQL 生成创建表SQL
func (s *Service) GenerateCreateTableSQL(m *model.DataModel) string {
	sql := fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (\n", m.TableName)
	sql += "    id TEXT PRIMARY KEY,\n"
	sql += "    tenant_id TEXT NOT NULL DEFAULT 'default',\n"

	for _, field := range m.Fields {
		sqlType := s.getSQLType(field)
		nullStr := "NULL"
		if field.Required {
			nullStr = "NOT NULL"
		}
		sql += fmt.Sprintf("    %s %s %s,\n", field.Name, sqlType, nullStr)
	}

	sql += "    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),\n"
	sql += "    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()\n"
	sql += ");"

	return sql
}

// getSQLType 获取SQL类型
func (s *Service) getSQLType(field model.FieldDefinition) string {
	switch field.Type {
	case model.FieldTypeText, model.FieldTypeEmail, model.FieldTypePhone, model.FieldTypeURL:
		return "TEXT"
	case model.FieldTypeNumber:
		return "NUMERIC"
	case model.FieldTypeBoolean:
		return "BOOLEAN"
	case model.FieldTypeDate:
		return "DATE"
	case model.FieldTypeDateTime:
		return "TIMESTAMPTZ"
	case model.FieldTypeJSON:
		return "JSONB"
	case model.FieldTypeFile:
		return "TEXT"
	default:
		return "TEXT"
	}
}

// ========== PostgreSQL Store 实现 ==========

// PostgresStore PostgreSQL存储实现
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore 创建PostgreSQL存储
func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

// CreateDataModel 创建数据模型
func (s *PostgresStore) CreateDataModel(ctx context.Context, m *model.DataModel) error {
	schemaJSON, err := json.Marshal(m.Fields)
	if err != nil {
		return err
	}

	labelsJSON, err := json.Marshal(m.Labels)
	if err != nil {
		return err
	}

	query := `
		INSERT INTO lowcode_models (id, tenant_id, name, description, table_name, schema, labels, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`

	_, err = s.db.ExecContext(ctx, query,
		m.ID, m.TenantID, m.Name, m.Description, m.TableName,
		schemaJSON, labelsJSON, m.CreatedAt, m.UpdatedAt)
	return err
}

// GetDataModel 获取数据模型
func (s *PostgresStore) GetDataModel(ctx context.Context, id, tenantID string) (*model.DataModel, error) {
	query := `
		SELECT id, tenant_id, name, description, table_name, schema, labels, created_at, updated_at
		FROM lowcode_models
		WHERE id = $1 AND tenant_id = $2`

	m := &model.DataModel{}
	var schemaJSON, labelsJSON []byte

	err := s.db.QueryRowContext(ctx, query, id, tenantID).Scan(
		&m.ID, &m.TenantID, &m.Name, &m.Description, &m.TableName,
		&schemaJSON, &labelsJSON, &m.CreatedAt, &m.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	if schemaJSON != nil {
		json.Unmarshal(schemaJSON, &m.Fields)
	}
	if labelsJSON != nil {
		json.Unmarshal(labelsJSON, &m.Labels)
	}

	return m, nil
}

// ListDataModels 列出数据模型
func (s *PostgresStore) ListDataModels(ctx context.Context, tenantID string, limit, offset int) ([]*model.DataModel, int, error) {
	// 获取总数
	var total int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM lowcode_models WHERE tenant_id = $1`, tenantID).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	// 获取列表
	query := `
		SELECT id, tenant_id, name, description, table_name, schema, labels, created_at, updated_at
		FROM lowcode_models
		WHERE tenant_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3`

	rows, err := s.db.QueryContext(ctx, query, tenantID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var models []*model.DataModel
	for rows.Next() {
		m := &model.DataModel{}
		var schemaJSON, labelsJSON []byte

		err := rows.Scan(
			&m.ID, &m.TenantID, &m.Name, &m.Description, &m.TableName,
			&schemaJSON, &labelsJSON, &m.CreatedAt, &m.UpdatedAt)
		if err != nil {
			return nil, 0, err
		}

		if schemaJSON != nil {
			json.Unmarshal(schemaJSON, &m.Fields)
		}
		if labelsJSON != nil {
			json.Unmarshal(labelsJSON, &m.Labels)
		}

		models = append(models, m)
	}

	return models, total, nil
}

// UpdateDataModel 更新数据模型
func (s *PostgresStore) UpdateDataModel(ctx context.Context, m *model.DataModel) error {
	schemaJSON, err := json.Marshal(m.Fields)
	if err != nil {
		return err
	}

	labelsJSON, err := json.Marshal(m.Labels)
	if err != nil {
		return err
	}

	query := `
		UPDATE lowcode_models
		SET name = $1, description = $2, table_name = $3, schema = $4, labels = $5, updated_at = $6
		WHERE id = $7 AND tenant_id = $8`

	_, err = s.db.ExecContext(ctx, query,
		m.Name, m.Description, m.TableName, schemaJSON, labelsJSON,
		m.UpdatedAt, m.ID, m.TenantID)
	return err
}

// DeleteDataModel 删除数据模型
func (s *PostgresStore) DeleteDataModel(ctx context.Context, id, tenantID string) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM lowcode_models WHERE id = $1 AND tenant_id = $2`, id, tenantID)
	return err
}

// CreateInstance 创建模型实例
func (s *PostgresStore) CreateInstance(ctx context.Context, inst *model.ModelInstance) error {
	dataJSON, err := json.Marshal(inst.Data)
	if err != nil {
		return err
	}

	query := `
		INSERT INTO lowcode_instances (id, model_id, tenant_id, data, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)`

	_, err = s.db.ExecContext(ctx, query,
		inst.ID, inst.ModelID, inst.TenantID, dataJSON,
		inst.CreatedAt, inst.UpdatedAt)
	return err
}

// GetInstance 获取模型实例
func (s *PostgresStore) GetInstance(ctx context.Context, id, tenantID string) (*model.ModelInstance, error) {
	query := `
		SELECT id, model_id, tenant_id, data, created_at, updated_at
		FROM lowcode_instances
		WHERE id = $1 AND tenant_id = $2`

	inst := &model.ModelInstance{}
	var dataJSON []byte

	err := s.db.QueryRowContext(ctx, query, id, tenantID).Scan(
		&inst.ID, &inst.ModelID, &inst.TenantID, &dataJSON,
		&inst.CreatedAt, &inst.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	if dataJSON != nil {
		json.Unmarshal(dataJSON, &inst.Data)
	}

	return inst, nil
}

// ListInstances 列出模型实例
func (s *PostgresStore) ListInstances(ctx context.Context, modelID, tenantID string, limit, offset int) ([]*model.ModelInstance, int, error) {
	// 获取总数
	var total int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM lowcode_instances WHERE model_id = $1 AND tenant_id = $2`,
		modelID, tenantID).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	// 获取列表
	query := `
		SELECT id, model_id, tenant_id, data, created_at, updated_at
		FROM lowcode_instances
		WHERE model_id = $1 AND tenant_id = $2
		ORDER BY created_at DESC
		LIMIT $3 OFFSET $4`

	rows, err := s.db.QueryContext(ctx, query, modelID, tenantID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var instances []*model.ModelInstance
	for rows.Next() {
		inst := &model.ModelInstance{}
		var dataJSON []byte

		err := rows.Scan(
			&inst.ID, &inst.ModelID, &inst.TenantID, &dataJSON,
			&inst.CreatedAt, &inst.UpdatedAt)
		if err != nil {
			return nil, 0, err
		}

		if dataJSON != nil {
			json.Unmarshal(dataJSON, &inst.Data)
		}

		instances = append(instances, inst)
	}

	return instances, total, nil
}

// UpdateInstance 更新模型实例
func (s *PostgresStore) UpdateInstance(ctx context.Context, inst *model.ModelInstance) error {
	dataJSON, err := json.Marshal(inst.Data)
	if err != nil {
		return err
	}

	query := `
		UPDATE lowcode_instances
		SET data = $1, updated_at = $2
		WHERE id = $3 AND tenant_id = $4`

	_, err = s.db.ExecContext(ctx, query, dataJSON, inst.UpdatedAt, inst.ID, inst.TenantID)
	return err
}

// DeleteInstance 删除模型实例
func (s *PostgresStore) DeleteInstance(ctx context.Context, id, tenantID string) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM lowcode_instances WHERE id = $1 AND tenant_id = $2`, id, tenantID)
	return err
}

// CreatePage 创建页面
func (s *PostgresStore) CreatePage(ctx context.Context, p *model.PageDefinition) error {
	componentsJSON, err := json.Marshal(p.Components)
	if err != nil {
		return err
	}

	layoutJSON, err := json.Marshal(p.Layout)
	if err != nil {
		return err
	}

	labelsJSON, err := json.Marshal(p.Labels)
	if err != nil {
		return err
	}

	query := `
		INSERT INTO lowcode_pages (id, tenant_id, name, description, type, model_id, components, layout, labels, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`

	_, err = s.db.ExecContext(ctx, query,
		p.ID, p.TenantID, p.Name, p.Description, p.Type, p.ModelID,
		componentsJSON, layoutJSON, labelsJSON, p.CreatedAt, p.UpdatedAt)
	return err
}

// GetPage 获取页面
func (s *PostgresStore) GetPage(ctx context.Context, id, tenantID string) (*model.PageDefinition, error) {
	query := `
		SELECT id, tenant_id, name, description, type, model_id, components, layout, labels, created_at, updated_at
		FROM lowcode_pages
		WHERE id = $1 AND tenant_id = $2`

	p := &model.PageDefinition{}
	var componentsJSON, layoutJSON, labelsJSON []byte

	err := s.db.QueryRowContext(ctx, query, id, tenantID).Scan(
		&p.ID, &p.TenantID, &p.Name, &p.Description, &p.Type, &p.ModelID,
		&componentsJSON, &layoutJSON, &labelsJSON, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	if componentsJSON != nil {
		json.Unmarshal(componentsJSON, &p.Components)
	}
	if layoutJSON != nil {
		json.Unmarshal(layoutJSON, &p.Layout)
	}
	if labelsJSON != nil {
		json.Unmarshal(labelsJSON, &p.Labels)
	}

	return p, nil
}

// ListPages 列出页面
func (s *PostgresStore) ListPages(ctx context.Context, tenantID string, limit, offset int) ([]*model.PageDefinition, int, error) {
	// 获取总数
	var total int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM lowcode_pages WHERE tenant_id = $1`, tenantID).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	// 获取列表
	query := `
		SELECT id, tenant_id, name, description, type, model_id, components, layout, labels, created_at, updated_at
		FROM lowcode_pages
		WHERE tenant_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3`

	rows, err := s.db.QueryContext(ctx, query, tenantID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var pages []*model.PageDefinition
	for rows.Next() {
		p := &model.PageDefinition{}
		var componentsJSON, layoutJSON, labelsJSON []byte

		err := rows.Scan(
			&p.ID, &p.TenantID, &p.Name, &p.Description, &p.Type, &p.ModelID,
			&componentsJSON, &layoutJSON, &labelsJSON, &p.CreatedAt, &p.UpdatedAt)
		if err != nil {
			return nil, 0, err
		}

		if componentsJSON != nil {
			json.Unmarshal(componentsJSON, &p.Components)
		}
		if layoutJSON != nil {
			json.Unmarshal(layoutJSON, &p.Layout)
		}
		if labelsJSON != nil {
			json.Unmarshal(labelsJSON, &p.Labels)
		}

		pages = append(pages, p)
	}

	return pages, total, nil
}

// UpdatePage 更新页面
func (s *PostgresStore) UpdatePage(ctx context.Context, p *model.PageDefinition) error {
	componentsJSON, err := json.Marshal(p.Components)
	if err != nil {
		return err
	}

	layoutJSON, err := json.Marshal(p.Layout)
	if err != nil {
		return err
	}

	labelsJSON, err := json.Marshal(p.Labels)
	if err != nil {
		return err
	}

	query := `
		UPDATE lowcode_pages
		SET name = $1, description = $2, type = $3, model_id = $4, components = $5, layout = $6, labels = $7, updated_at = $8
		WHERE id = $9 AND tenant_id = $10`

	_, err = s.db.ExecContext(ctx, query,
		p.Name, p.Description, p.Type, p.ModelID, componentsJSON,
		layoutJSON, labelsJSON, p.UpdatedAt, p.ID, p.TenantID)
	return err
}

// DeletePage 删除页面
func (s *PostgresStore) DeletePage(ctx context.Context, id, tenantID string) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM lowcode_pages WHERE id = $1 AND tenant_id = $2`, id, tenantID)
	return err
}