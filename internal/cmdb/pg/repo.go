package pg

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/opsorigin/core/internal/cmdb/model"

	_ "github.com/lib/pq"
)

var (
	ErrAntiFlapping   = errors.New("channel weight is lower than existing")
	ErrValueUnchanged = errors.New("value diff is zero, update skipped")
)

type Repository struct {
	db *sql.DB
}

func NewRepository(dsn string) (*Repository, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	return &Repository{db: db}, nil
}

func (r *Repository) DB() *sql.DB {
	return r.db
}

func (r *Repository) Close() error {
	return r.db.Close()
}

func (r *Repository) Ping(ctx context.Context) error {
	return r.db.PingContext(ctx)
}

// Migrate 对 cmdb_ci_model_type 做幂等加列迁移（分类/图标/二级分类/菜单显示/内置标记）。
// 与审计表迁移同模式：启动期执行，失败阻断启动以暴露问题。
func (r *Repository) Migrate(ctx context.Context) error {
	cols := []string{
		`category TEXT`,
		`sub_category TEXT`,
		`icon TEXT`,
		`show_in_menu BOOLEAN NOT NULL DEFAULT TRUE`,
		`builtin BOOLEAN NOT NULL DEFAULT FALSE`,
		`author TEXT`,
		`abstract BOOLEAN NOT NULL DEFAULT FALSE`,
		`source TEXT`,
		`discovered_by TEXT`,
		`confidence DOUBLE PRECISION NOT NULL DEFAULT 0`,
		`evidence JSONB`,
		`version INTEGER NOT NULL DEFAULT 1`,
		`suggested_collectors JSONB`,
	}
	for _, c := range cols {
		if _, err := r.db.ExecContext(ctx, `ALTER TABLE cmdb_ci_model_type ADD COLUMN IF NOT EXISTS `+c); err != nil {
			return fmt.Errorf("migrate cmdb_ci_model_type column %q: %w", c, err)
		}
	}
	// 存量模型回填默认分类（platform），空分类在展示时按 platform 处理。
	if _, err := r.db.ExecContext(ctx, `UPDATE cmdb_ci_model_type SET category='platform' WHERE category IS NULL OR category=''`); err != nil {
		return fmt.Errorf("backfill cmdb_ci_model_type category: %w", err)
	}
	// 方案 C 治理回填：存量大类(database/middleware)标记为抽象父类, 禁止直接实例化。
	// 与 deploy/migrations/017 一致; 运行时走本路径(017 仅全新库/手动对齐用)。
	if _, err := r.db.ExecContext(ctx, `UPDATE cmdb_ci_model_type SET abstract = TRUE
		WHERE id IN ('database', 'middleware') AND abstract IS DISTINCT FROM TRUE`); err != nil {
		return fmt.Errorf("backfill abstract model types: %w", err)
	}
	// 方案 C 存量迁移：把大类 CI 按 engine/middleware_type 属性下沉为具体引擎子类 TypeID。
	// 幂等：仅作用于仍处于父类(database/middleware)的行；下沉后 type_id 不再命中，重复执行无副作用。
	// 未知 engine 保留父类不迁移，避免制造孤儿类型。
	for _, stmt := range []string{
		`UPDATE cmdb_ci SET type_id = attributes->>'engine'
		 WHERE type_id = 'database' AND attributes ? 'engine'
		   AND attributes->>'engine' IN ('mysql','oracle','postgresql')`,
		`UPDATE cmdb_ci SET type_id = attributes->>'middleware_type'
		 WHERE type_id = 'middleware' AND attributes ? 'middleware_type'
		   AND attributes->>'middleware_type' IN ('redis','kafka','nginx','elasticsearch','zookeeper','rabbitmq')`,
	} {
		if _, err := r.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("sink legacy ci type_id to engine subclass: %w", err)
		}
	}
	// 保存的查询策略表（P1-3「我想查」）+ 采集动态目标引用（P1-4）
	if _, err := r.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS cmdb_saved_query (
			id         TEXT PRIMARY KEY,
			tenant_id  TEXT NOT NULL DEFAULT 'default',
			name       TEXT NOT NULL,
			type_id    TEXT,
			query      JSONB NOT NULL DEFAULT '{}'::jsonb,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`); err != nil {
		return fmt.Errorf("create cmdb_saved_query: %w", err)
	}
	// 采集任务动态目标字段（P1-4）：target_mode(static|query) + target_query(保存查询 ID)
	for _, c := range []string{
		`target_mode TEXT NOT NULL DEFAULT 'static'`,
		`target_query TEXT`,
	} {
		if _, err := r.db.ExecContext(ctx, `ALTER TABLE cmdb_collection_task ADD COLUMN IF NOT EXISTS `+c); err != nil {
			return fmt.Errorf("migrate cmdb_collection_task column %q: %w", c, err)
		}
	}
	// 模型分类表 + 内置种子 + 存量分类名迁移（#2 可扩展分类）
	if err := r.migrateModelCategory(ctx); err != nil {
		return err
	}
	// P2 大块：统计/IDC/字段规则/消息订阅表
	if err := r.migrateP2(ctx); err != nil {
		return err
	}
	return nil
}

func (r *Repository) CreateCIModelType(ctx context.Context, mt *model.CIModelType) error {
	// 保留调用方指定的固定 ID(内置模型如 server/business_service 需固定 ID,
	// 发现插件按 CIType 映射 type_id 落库); 未指定时生成 UUID(用户自建模型)。
	if mt.ID == "" {
		mt.ID = uuid.New().String()
	}
	now := time.Now()
	mt.CreatedAt = now
	mt.UpdatedAt = now

	schemaJSON, err := json.Marshal(mt.Schema)
	if err != nil {
		return fmt.Errorf("marshal schema: %w", err)
	}

	_, err = r.db.ExecContext(ctx, `
		INSERT INTO cmdb_ci_model_type (id, tenant_id, name, description, parent_id, schema, category, sub_category, icon, show_in_menu, builtin, author, abstract, source, discovered_by, confidence, evidence, version, suggested_collectors, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21)`,
		mt.ID, mt.TenantID, mt.Name, nullIfEmpty(mt.Description), nullIfEmpty(mt.ParentID),
		schemaJSON, nullIfEmpty(mt.Category), nullIfEmpty(mt.SubCategory), nullIfEmpty(mt.Icon),
		mt.ShowInMenu, mt.Builtin, nullIfEmpty(mt.Author),
		mt.Abstract, nullIfEmpty(mt.Source), nullIfEmpty(mt.DiscoveredBy),
		mt.Confidence, toJSON(mt.Evidence), mt.Version, toJSON(mt.SuggestedCollectors),
		mt.CreatedAt, mt.UpdatedAt)
	if err != nil {
		return fmt.Errorf("insert ci model type: %w", err)
	}
	return nil
}

func (r *Repository) GetCIModelType(ctx context.Context, id, tenantID string) (*model.CIModelType, error) {
	mt := &model.CIModelType{}
	var schemaJSON []byte
	var desc, parentID, category, subCategory, icon, author, source, discoveredBy, evidenceJSON, collectorsJSON sql.NullString
	var showInMenu, builtin, abstract sql.NullBool
	var confidence sql.NullFloat64
	var version sql.NullInt64

	err := r.db.QueryRowContext(ctx, `
		SELECT id, tenant_id, name, description, parent_id, schema, category, sub_category, icon, show_in_menu, builtin, author, abstract, source, discovered_by, confidence, evidence, version, suggested_collectors, created_at, updated_at
		FROM cmdb_ci_model_type WHERE id = $1 AND tenant_id = $2`, id, tenantID).Scan(
		&mt.ID, &mt.TenantID, &mt.Name, &desc, &parentID, &schemaJSON, &category, &subCategory, &icon, &showInMenu, &builtin, &author, &abstract, &source, &discoveredBy, &confidence, &evidenceJSON, &version, &collectorsJSON, &mt.CreatedAt, &mt.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("query ci model type: %w", err)
	}
	mt.Description = desc.String
	mt.ParentID = parentID.String
	mt.Category = category.String
	mt.SubCategory = subCategory.String
	mt.Icon = icon.String
	mt.Author = author.String
	mt.ShowInMenu = showInMenu.Bool
	mt.Builtin = builtin.Bool
	mt.Abstract = abstract.Bool
	mt.Source = source.String
	mt.DiscoveredBy = discoveredBy.String
	mt.Confidence = confidence.Float64
	mt.Version = int(version.Int64)
	if evidenceJSON.Valid && evidenceJSON.String != "" {
		_ = json.Unmarshal([]byte(evidenceJSON.String), &mt.Evidence)
	}
	if collectorsJSON.Valid && collectorsJSON.String != "" {
		_ = json.Unmarshal([]byte(collectorsJSON.String), &mt.SuggestedCollectors)
	}
	if err := json.Unmarshal(schemaJSON, &mt.Schema); err != nil {
		return nil, fmt.Errorf("unmarshal schema: %w", err)
	}
	return mt, nil
}

func (r *Repository) CreateCI(ctx context.Context, ci *model.CIModel, traceID string) error {
	ci.ID = uuid.New().String()
	now := time.Now()
	ci.CreatedAt = now
	ci.UpdatedAt = now

	attrsJSON, err := json.Marshal(ci.Attributes)
	if err != nil {
		return fmt.Errorf("marshal attributes: %w", err)
	}
	labelsJSON, err := json.Marshal(ci.Labels)
	if err != nil {
		return fmt.Errorf("marshal labels: %w", err)
	}
	if ci.AttributeMeta == nil {
		// 兜底：按 Channel 推导来源（人工→manual 等）；采集路径已在调用前注入具体 source。
		ci.AttributeMeta = model.BuildAttrMeta(ci.Attributes, model.SourceFromChannelWeight(ci.Channel), time.Now())
	}
	metaJSON, err := marshalAttrMeta(ci.AttributeMeta)
	if err != nil {
		return fmt.Errorf("marshal attribute_meta: %w", err)
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO cmdb_ci (id, type_id, tenant_id, name, description, status, attributes, labels, channel, created_at, updated_at, attribute_meta)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		ci.ID, ci.TypeID, ci.TenantID, ci.Name, nullIfEmpty(ci.Description),
		string(ci.Status), attrsJSON, labelsJSON, int(ci.Channel), ci.CreatedAt, ci.UpdatedAt, metaJSON)
	if err != nil {
		return fmt.Errorf("insert ci: %w", err)
	}

	payload, _ := json.Marshal(map[string]any{
		"ci_id":   ci.ID,
		"type_id": ci.TypeID,
		"name":    ci.Name,
	})
	if err := r.insertOutbox(ctx, tx, ci.TenantID, model.EventCICreated, "ci", ci.ID, string(payload), traceID); err != nil {
		return err
	}

	return tx.Commit()
}

// BulkCreateCI 批量创建 CI, 在单一事务内完成(决策 B 异步导入的落库侧)。
// 与 CreateCI 不同, 它一次性写入 N 条并各带 outbox 事件, 显著提升大批量导入吞吐。
func (r *Repository) BulkCreateCI(ctx context.Context, cis []*model.CIModel, traceID string) error {
	if len(cis) == 0 {
		return nil
	}
	now := time.Now()
	for _, ci := range cis {
		if ci.ID == "" {
			ci.ID = uuid.New().String()
		}
		ci.CreatedAt = now
		ci.UpdatedAt = now
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	for _, ci := range cis {
		attrsJSON, err := json.Marshal(ci.Attributes)
		if err != nil {
			return fmt.Errorf("marshal attributes: %w", err)
		}
		labelsJSON, err := json.Marshal(ci.Labels)
		if err != nil {
			return fmt.Errorf("marshal labels: %w", err)
		}
		if ci.AttributeMeta == nil {
			ci.AttributeMeta = model.BuildAttrMeta(ci.Attributes, model.SourceFromChannelWeight(ci.Channel), time.Now())
		}
		metaJSON, err := marshalAttrMeta(ci.AttributeMeta)
		if err != nil {
			return fmt.Errorf("marshal attribute_meta: %w", err)
		}
		_, err = tx.ExecContext(ctx, `
		INSERT INTO cmdb_ci (id, type_id, tenant_id, name, description, status, attributes, labels, channel, created_at, updated_at, attribute_meta)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
			ci.ID, ci.TypeID, ci.TenantID, ci.Name, nullIfEmpty(ci.Description),
			string(ci.Status), attrsJSON, labelsJSON, int(ci.Channel), ci.CreatedAt, ci.UpdatedAt, metaJSON)
		if err != nil {
			return fmt.Errorf("insert ci (bulk): %w", err)
		}
		payload, _ := json.Marshal(map[string]any{
			"ci_id":   ci.ID,
			"type_id": ci.TypeID,
			"name":    ci.Name,
		})
		if err := r.insertOutbox(ctx, tx, ci.TenantID, model.EventCICreated, "ci", ci.ID, string(payload), traceID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// incomingFieldSource 推断本次更新中某字段的来源（用于字段级 anti-flapping 比较）。
// 采集链路已在 AttributeMeta 中显式注入具体来源(cloud_api/agent)，否则回退到 channel 推导。
func incomingFieldSource(ci *model.CIModel, k string, ch model.ChannelWeight) model.SourceKind {
	if ci.AttributeMeta != nil {
		if m, ok := ci.AttributeMeta[k]; ok {
			return m.Source
		}
	}
	return model.SourceFromChannelWeight(ch)
}

// existingFieldSource 推断既有 CI 中某字段的来源；无字段级 meta 时回退到 CI 级 channel。
func existingFieldSource(existing *model.CIModel, k string) model.SourceKind {
	if existing.AttributeMeta != nil {
		if m, ok := existing.AttributeMeta[k]; ok {
			return m.Source
		}
	}
	return model.SourceFromChannelWeight(existing.Channel)
}

func (r *Repository) UpdateCI(ctx context.Context, ci *model.CIModel, channel model.ChannelWeight, traceID string) error {
	existing, err := r.getCIByID(ctx, ci.ID)
	if err != nil {
		return err
	}
	if existing == nil {
		return errors.New("ci not found")
	}

	// 字段级 anti-flapping：不再整 CI 拦截。
	// 仅当某字段的「既有来源权重」高于「本次来源权重」时跳过该字段，其余字段照常刷新——
	// 人工(manual,100)字段天然高于采集(≤30)，故不被覆盖，但同一 CI 上采集可继续刷新其它非人工字段。
	changed := false
	changeLog := make([]model.AttributeChange, 0)
	appliedKeys := make(map[string]bool)

	ci.UpdatedAt = time.Now()

	for k, newVal := range ci.Attributes {
		oldVal, exists := existing.Attributes[k]
		if exists && deepEqual(oldVal, newVal) {
			continue
		}
		// 字段级信任权重比较：低信任来源不得覆盖高信任字段
		if !model.ShouldOverwriteField(incomingFieldSource(ci, k, channel), existingFieldSource(existing, k)) {
			continue
		}
		changed = true
		appliedKeys[k] = true
		changeLog = append(changeLog, model.AttributeChange{
			CIID:      ci.ID,
			AttrName:  k,
			OldValue:  oldVal,
			NewValue:  newVal,
			Channel:   channel,
			ChangedAt: ci.UpdatedAt,
		})
	}

	if !changed {
		return nil
	}

	// CI 级 channel 列记录该 CI 的最高信任来源；采集(低权重)不得把人工(高权重)留下的
	// 整 CI 信任等级拉低。逐字段的 changeLog 仍使用真实入参 channel，如实记录本次来源。
	effectiveChannel := channel
	if existing.Channel > effectiveChannel {
		effectiveChannel = existing.Channel
	}

	attrsJSON, err := json.Marshal(ci.Attributes)
	if err != nil {
		return fmt.Errorf("marshal attributes: %w", err)
	}
	labelsJSON, err := json.Marshal(ci.Labels)
	if err != nil {
		return fmt.Errorf("marshal labels: %w", err)
	}

	// 字段级 meta：仅更新发生变化的字段的 meta，未变化字段保留既有来源。
	merged := make(map[string]model.AttrMeta, len(existing.AttributeMeta))
	for k, v := range existing.AttributeMeta {
		merged[k] = v
	}
	fallback := model.SourceFromChannelWeight(channel)
	for k := range appliedKeys {
		if m, ok := ci.AttributeMeta[k]; ok {
			merged[k] = m
		} else {
			merged[k] = model.AttrMeta{
				Source:     fallback,
				Confidence: model.DefaultConfidence(fallback),
				LastSeenAt: ci.UpdatedAt,
			}
		}
	}
	metaJSON, err := marshalAttrMeta(merged)
	if err != nil {
		return fmt.Errorf("marshal attribute_meta: %w", err)
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `
		UPDATE cmdb_ci SET name=$1, description=$2, status=$3, attributes=$4, labels=$5, channel=$6, updated_at=$7, attribute_meta=$8
		WHERE id=$9 AND tenant_id=$10`,
		ci.Name, nullIfEmpty(ci.Description), string(ci.Status), attrsJSON, labelsJSON,
		int(effectiveChannel), ci.UpdatedAt, metaJSON, ci.ID, ci.TenantID)
	if err != nil {
		return fmt.Errorf("update ci: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return errors.New("ci not found or tenant mismatch")
	}

	for _, ch := range changeLog {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO cmdb_ci_audit (ci_id, attr_name, old_value, new_value, channel, trace_id, changed_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			ci.ID, ch.AttrName, toJSON(ch.OldValue), toJSON(ch.NewValue), int(ch.Channel), traceID, ch.ChangedAt)
		if err != nil {
			return fmt.Errorf("insert audit log: %w", err)
		}
	}

	payload, _ := json.Marshal(map[string]any{
		"ci_id":      ci.ID,
		"change_log": changeLog,
		"attributes": ci.Attributes,
	})
	if err := r.insertOutbox(ctx, tx, ci.TenantID, model.EventCIUpdated, "ci", ci.ID, string(payload), traceID); err != nil {
		return err
	}

	return tx.Commit()
}

func (r *Repository) GetCI(ctx context.Context, id, tenantID string) (*model.CIModel, error) {
	return r.getCIByIDAndTenant(ctx, id, tenantID)
}

// UpdateCIStatus 仅更新 CI 状态（用于归档/恢复），并写 outbox 事件以便图同步。
// 与 UpdateCI 解耦，避免归档动作触发大字段重写与字段级 anti-flapping 比较。
func (r *Repository) UpdateCIStatus(ctx context.Context, id, tenantID string, status model.CIStatus, traceID string) error {
	existing, err := r.getCIByID(ctx, id)
	if err != nil {
		return err
	}
	if existing == nil {
		return errors.New("ci not found")
	}
	if existing.Status == status {
		return nil
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `UPDATE cmdb_ci SET status=$1, updated_at=NOW() WHERE id=$2 AND tenant_id=$3`,
		string(status), id, tenantID); err != nil {
		return fmt.Errorf("update ci status: %w", err)
	}

	payload, _ := json.Marshal(map[string]any{
		"ci_id":      id,
		"status":     string(status),
		"old_status": string(existing.Status),
	})
	if err := r.insertOutbox(ctx, tx, tenantID, model.EventCIUpdated, "ci", id, string(payload), traceID); err != nil {
		return err
	}
	return tx.Commit()
}

// ListCIFiltered 与 ListCIPaginated 类似，但支持归档过滤模式：
//   - archivedMode="archived"：仅返回已归档 CI
//   - archivedMode="all"：返回全部（含归档）
//   - 其它（含空）：排除已归档，仅返回 active/inactive
//
// 普通实例列表默认排除归档，归档视图用 "archived"，运维总览可用 "all"。
func (r *Repository) ListCIFiltered(ctx context.Context, tenantID, typeID, archivedMode, sortBy, sortOrder string, limit, offset int) ([]*model.CIModel, error) {
	query := `SELECT id, type_id, tenant_id, name, description, status, attributes, labels, channel, created_at, updated_at, attribute_meta
		FROM cmdb_ci WHERE tenant_id = $1`
	args := []any{tenantID}
	argIdx := 2

	if typeID != "" {
		query += fmt.Sprintf(` AND type_id = $%d`, argIdx)
		args = append(args, typeID)
		argIdx++
	}

	switch archivedMode {
	case "archived":
		query += ` AND status = 'archived'`
	case "all":
		// 不过滤
	default:
		query += ` AND status <> 'archived'`
	}

	orderCol := "created_at"
	if sortBy == "name" {
		orderCol = "name"
	} else if sortBy == "updated_at" {
		orderCol = "updated_at"
	}
	orderDir := "DESC"
	if sortOrder == "asc" {
		orderDir = "ASC"
	}
	query += fmt.Sprintf(` ORDER BY %s %s`, orderCol, orderDir)

	if limit > 0 {
		query += fmt.Sprintf(` LIMIT $%d`, argIdx)
		args = append(args, limit)
		argIdx++
	} else {
		query += ` LIMIT 100`
	}
	if offset > 0 {
		query += fmt.Sprintf(` OFFSET $%d`, argIdx)
		args = append(args, offset)
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list ci filtered: %w", err)
	}
	defer rows.Close()

	var cis []*model.CIModel
	for rows.Next() {
		ci, err := scanCI(rows)
		if err != nil {
			return nil, err
		}
		cis = append(cis, ci)
	}
	return cis, rows.Err()
}

func (r *Repository) DeleteCI(ctx context.Context, id, tenantID, traceID string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `DELETE FROM cmdb_ci_relation WHERE source_id=$1 AND tenant_id=$2`, id, tenantID)
	if err != nil {
		return fmt.Errorf("cascade delete source relations: %w", err)
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM cmdb_ci_relation WHERE target_id=$1 AND tenant_id=$2`, id, tenantID)
	if err != nil {
		return fmt.Errorf("cascade delete target relations: %w", err)
	}
	// 级联删除审计与版本历史（否则有变更记录的 CI 会因外键无法删除——既有 bug）
	if _, err := tx.ExecContext(ctx, `DELETE FROM cmdb_ci_audit WHERE ci_id=$1`, id); err != nil {
		return fmt.Errorf("cascade delete ci audit: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM cmdb_ci_version WHERE ci_id=$1`, id); err != nil {
		return fmt.Errorf("cascade delete ci versions: %w", err)
	}

	result, err := tx.ExecContext(ctx, `DELETE FROM cmdb_ci WHERE id=$1 AND tenant_id=$2`, id, tenantID)
	if err != nil {
		return fmt.Errorf("delete ci: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return errors.New("ci not found")
	}

	if err := r.insertOutbox(ctx, tx, tenantID, model.EventCIDeleted, "ci", id, `{"ci_id":"`+id+`"}`, traceID); err != nil {
		return err
	}

	return tx.Commit()
}

func (r *Repository) GetCIAudit(ctx context.Context, ciID, tenantID string) ([]*model.AttributeChange, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, ci_id, attr_name, old_value, new_value, channel, trace_id, changed_at
		FROM cmdb_ci_audit a
		JOIN cmdb_ci c ON a.ci_id = c.id
		WHERE a.ci_id = $1 AND c.tenant_id = $2
		ORDER BY a.changed_at DESC`, ciID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("query ci audit: %w", err)
	}
	defer rows.Close()

	var changes []*model.AttributeChange
	for rows.Next() {
		var ch model.AttributeChange
		var oldVal, newVal sql.NullString
		var id int
		var traceID string
		if err := rows.Scan(&id, &ch.CIID, &ch.AttrName, &oldVal, &newVal, &ch.Channel, &traceID, &ch.ChangedAt); err != nil {
			return nil, fmt.Errorf("scan audit: %w", err)
		}
		if oldVal.Valid {
			ch.OldValue = oldVal.String
		}
		if newVal.Valid {
			ch.NewValue = newVal.String
		}
		changes = append(changes, &ch)
	}
	return changes, rows.Err()
}

func (r *Repository) ListCI(ctx context.Context, tenantID string, typeID string) ([]*model.CIModel, error) {
	return r.ListCIPaginated(ctx, tenantID, typeID, "", "", 0, 0)
}

// CountCIByAttrValue 统计「同租户 + 同模型类型」下某属性字段取指定值的 CI 数,
// 用于模型唯一性约束(Unique)的写入拦截。excludeID 非空时排除自身(更新场景)。
func (r *Repository) CountCIByAttrValue(ctx context.Context, tenantID, typeID, attrKey, value, excludeID string) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx, `
		SELECT count(*) FROM cmdb_ci
		WHERE tenant_id = $1 AND type_id = $2 AND status <> 'deleted'
		  AND attributes->>$3 = $4 AND id <> $5`,
		tenantID, typeID, attrKey, value, excludeID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count ci by attr: %w", err)
	}
	return n, nil
}

// GetCIByID 按 ID 取 CI(供引用型字段校验目标 CI 存在性), 与 GetCI 等价。
func (r *Repository) GetCIByID(ctx context.Context, id, tenantID string) (*model.CIModel, error) {
	return r.GetCI(ctx, id, tenantID)
}

// ListCIByQuery lists CIs filtered by optional type and a free-text query,
// pushing the filter down to SQL (ILIKE) instead of loading the whole table
// into memory and filtering in Go (see report issue 3.3).
func (r *Repository) ListCIByQuery(ctx context.Context, tenantID, typeID, q string) ([]*model.CIModel, error) {
	query := `SELECT id, type_id, tenant_id, name, description, status, attributes, labels, channel, created_at, updated_at, attribute_meta
	FROM cmdb_ci WHERE tenant_id = $1`
	args := []any{tenantID}
	argIdx := 2

	if typeID != "" {
		query += fmt.Sprintf(` AND type_id = $%d`, argIdx)
		args = append(args, typeID)
		argIdx++
	}
	if q != "" {
		// ILIKE gives case-insensitive substring matching. The pattern is a
		// bound parameter, so user input cannot break out of the clause.
		// attributes::text 将 JSONB 序列化为文本参与匹配, 支持按属性值检索。
		query += fmt.Sprintf(` AND (name ILIKE $%d OR description ILIKE $%d OR type_id ILIKE $%d OR attributes::text ILIKE $%d)`, argIdx, argIdx+1, argIdx+2, argIdx+3)
		like := "%" + q + "%"
		args = append(args, like, like, like, like)
		argIdx += 4
	}

	query += ` ORDER BY created_at DESC LIMIT 100`

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list ci by query: %w", err)
	}
	defer rows.Close()

	var cis []*model.CIModel
	for rows.Next() {
		ci, err := scanCI(rows)
		if err != nil {
			return nil, err
		}
		cis = append(cis, ci)
	}
	return cis, rows.Err()
}

func (r *Repository) ListCIPaginated(ctx context.Context, tenantID, typeID, sortBy, sortOrder string, limit, offset int) ([]*model.CIModel, error) {
	query := `SELECT id, type_id, tenant_id, name, description, status, attributes, labels, channel, created_at, updated_at, attribute_meta
		FROM cmdb_ci WHERE tenant_id = $1`
	args := []any{tenantID}
	argIdx := 2

	if typeID != "" {
		query += fmt.Sprintf(` AND type_id = $%d`, argIdx)
		args = append(args, typeID)
		argIdx++
	}

	orderCol := "created_at"
	if sortBy == "name" {
		orderCol = "name"
	} else if sortBy == "updated_at" {
		orderCol = "updated_at"
	}
	orderDir := "DESC"
	if sortOrder == "asc" {
		orderDir = "ASC"
	}
	query += fmt.Sprintf(` ORDER BY %s %s`, orderCol, orderDir)

	if limit > 0 {
		query += fmt.Sprintf(` LIMIT $%d`, argIdx)
		args = append(args, limit)
		argIdx++
	} else {
		query += ` LIMIT 100`
	}
	if offset > 0 {
		query += fmt.Sprintf(` OFFSET $%d`, argIdx)
		args = append(args, offset)
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list ci: %w", err)
	}
	defer rows.Close()

	var cis []*model.CIModel
	for rows.Next() {
		ci, err := scanCI(rows)
		if err != nil {
			return nil, err
		}
		cis = append(cis, ci)
	}
	return cis, rows.Err()
}

func (r *Repository) CreateRelation(ctx context.Context, rel *model.CIRelation, traceID string) error {
	rel.ID = uuid.New().String()
	rel.CreatedAt = time.Now()

	propsJSON, err := json.Marshal(rel.Properties)
	if err != nil {
		return fmt.Errorf("marshal properties: %w", err)
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO cmdb_ci_relation (id, tenant_id, source_id, target_id, relation_type, properties, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		rel.ID, rel.TenantID, rel.SourceID, rel.TargetID, rel.RelationType, propsJSON, rel.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert relation: %w", err)
	}

	payload, _ := json.Marshal(rel)
	if err := r.insertOutbox(ctx, tx, rel.TenantID, model.EventRelationCreated, "relation", rel.ID, string(payload), traceID); err != nil {
		return err
	}

	return tx.Commit()
}

func (r *Repository) DeleteRelation(ctx context.Context, id, tenantID, traceID string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `DELETE FROM cmdb_ci_relation WHERE id=$1 AND tenant_id=$2`, id, tenantID)
	if err != nil {
		return fmt.Errorf("delete relation: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return errors.New("relation not found")
	}

	if err := r.insertOutbox(ctx, tx, tenantID, model.EventRelationDeleted, "relation", id, `{"relation_id":"`+id+`"}`, traceID); err != nil {
		return err
	}

	return tx.Commit()
}

func (r *Repository) ListRelations(ctx context.Context, tenantID string) ([]*model.CIRelation, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, tenant_id, source_id, target_id, relation_type, properties, created_at
		FROM cmdb_ci_relation WHERE tenant_id = $1 ORDER BY created_at`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list relations: %w", err)
	}
	defer rows.Close()

	var rels []*model.CIRelation
	for rows.Next() {
		rel := &model.CIRelation{}
		var propsJSON []byte
		if err := rows.Scan(&rel.ID, &rel.TenantID, &rel.SourceID, &rel.TargetID, &rel.RelationType, &propsJSON, &rel.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan relation: %w", err)
		}
		if propsJSON != nil {
			json.Unmarshal(propsJSON, &rel.Properties)
		}
		rels = append(rels, rel)
	}
	return rels, rows.Err()
}

func (r *Repository) GetUnprocessedOutbox(ctx context.Context, batchSize int) ([]*model.OutboxRecord, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, tenant_id, event_type, entity_type, entity_id, payload, trace_id, created_at, processed_at
		FROM cmdb_outbox WHERE processed_at IS NULL
		ORDER BY created_at ASC LIMIT $1 FOR UPDATE SKIP LOCKED`, batchSize)
	if err != nil {
		return nil, fmt.Errorf("query outbox: %w", err)
	}
	defer rows.Close()

	var records []*model.OutboxRecord
	for rows.Next() {
		rec := &model.OutboxRecord{}
		var processedAt sql.NullTime
		if err := rows.Scan(&rec.ID, &rec.TenantID, &rec.EventType, &rec.EntityType, &rec.EntityID,
			&rec.Payload, &rec.TraceID, &rec.CreatedAt, &processedAt); err != nil {
			return nil, fmt.Errorf("scan outbox: %w", err)
		}
		if processedAt.Valid {
			rec.ProcessedAt = &processedAt.Time
		}
		records = append(records, rec)
	}
	return records, rows.Err()
}

func (r *Repository) MarkOutboxProcessed(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE cmdb_outbox SET processed_at = NOW() WHERE id = $1`, id)
	return err
}

func (r *Repository) insertOutbox(ctx context.Context, tx *sql.Tx, tenantID string, eventType model.OutboxEventType,
	entityType, entityID, payload, traceID string) error {

	id := uuid.New().String()
	_, err := tx.ExecContext(ctx, `
		INSERT INTO cmdb_outbox (id, tenant_id, event_type, entity_type, entity_id, payload, trace_id, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())`,
		id, tenantID, string(eventType), entityType, entityID, payload, traceID)
	if err != nil {
		return fmt.Errorf("insert outbox: %w", err)
	}
	return nil
}

func (r *Repository) getCIByID(ctx context.Context, id string) (*model.CIModel, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, type_id, tenant_id, name, description, status, attributes, labels, channel, created_at, updated_at, attribute_meta
		FROM cmdb_ci WHERE id = $1`, id)
	return scanCI(row)
}

func (r *Repository) getCIByIDAndTenant(ctx context.Context, id, tenantID string) (*model.CIModel, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, type_id, tenant_id, name, description, status, attributes, labels, channel, created_at, updated_at, attribute_meta
		FROM cmdb_ci WHERE id = $1 AND tenant_id = $2`, id, tenantID)
	return scanCI(row)
}

type scannable interface {
	Scan(dest ...any) error
}

func scanCI(row scannable) (*model.CIModel, error) {
	ci := &model.CIModel{}
	var desc sql.NullString
	var attrsJSON, labelsJSON, metaJSON []byte
	var channel int

	err := row.Scan(&ci.ID, &ci.TypeID, &ci.TenantID, &ci.Name, &desc, &ci.Status,
		&attrsJSON, &labelsJSON, &channel, &ci.CreatedAt, &ci.UpdatedAt, &metaJSON)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("scan ci: %w", err)
	}
	ci.Description = desc.String
	ci.Channel = model.ChannelWeight(channel)

	if attrsJSON != nil {
		json.Unmarshal(attrsJSON, &ci.Attributes)
	}
	if ci.Attributes == nil {
		ci.Attributes = make(map[string]any)
	}
	if labelsJSON != nil {
		json.Unmarshal(labelsJSON, &ci.Labels)
	}
	if metaJSON != nil {
		json.Unmarshal(metaJSON, &ci.AttributeMeta)
	}
	if ci.AttributeMeta == nil {
		ci.AttributeMeta = make(map[string]model.AttrMeta)
	}
	return ci, nil
}

// marshalAttrMeta 序列化字段级来源/置信度元数据；nil 时返回 "{}" 以符合
// cmdb_ci.attribute_meta 列的 NOT NULL 约束。
func marshalAttrMeta(m map[string]model.AttrMeta) ([]byte, error) {
	if m == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(m)
}

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func toJSON(v any) *string {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		s := fmt.Sprintf("%v", v)
		return &s
	}
	s := string(b)
	return &s
}

func deepEqual(a, b any) bool {
	aj, _ := json.Marshal(a)
	bj, _ := json.Marshal(b)
	return string(aj) == string(bj)
}

// ListCIModelTypes 列出所有CI模型类型
func (r *Repository) ListCIModelTypes(ctx context.Context, tenantID string, limit, offset int) ([]*model.CIModelType, error) {
	query := `
		SELECT id, tenant_id, name, description, parent_id, schema, category, sub_category, icon, show_in_menu, builtin, author, abstract, source, discovered_by, confidence, evidence, version, suggested_collectors, created_at, updated_at
		FROM cmdb_ci_model_type
		WHERE tenant_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3`

	rows, err := r.db.QueryContext(ctx, query, tenantID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("query ci model types: %w", err)
	}
	defer rows.Close()

	var types []*model.CIModelType
	for rows.Next() {
		mt := &model.CIModelType{}
		var schemaJSON []byte
		var desc, parentID, category, subCategory, icon, author, source, discoveredBy, evidenceJSON, collectorsJSON sql.NullString
		var showInMenu, builtin, abstract sql.NullBool
		var confidence sql.NullFloat64
		var version sql.NullInt64

		err := rows.Scan(&mt.ID, &mt.TenantID, &mt.Name, &desc, &parentID, &schemaJSON, &category, &subCategory, &icon, &showInMenu, &builtin, &author, &abstract, &source, &discoveredBy, &confidence, &evidenceJSON, &version, &collectorsJSON, &mt.CreatedAt, &mt.UpdatedAt)
		if err != nil {
			return nil, fmt.Errorf("scan ci model type: %w", err)
		}
		mt.Description = desc.String
		mt.ParentID = parentID.String
		mt.Category = category.String
		mt.SubCategory = subCategory.String
		mt.Icon = icon.String
		mt.Author = author.String
		mt.ShowInMenu = showInMenu.Bool
		mt.Builtin = builtin.Bool
		mt.Abstract = abstract.Bool
		mt.Source = source.String
		mt.DiscoveredBy = discoveredBy.String
		mt.Confidence = confidence.Float64
		mt.Version = int(version.Int64)
		if evidenceJSON.Valid && evidenceJSON.String != "" {
			_ = json.Unmarshal([]byte(evidenceJSON.String), &mt.Evidence)
		}
		if collectorsJSON.Valid && collectorsJSON.String != "" {
			_ = json.Unmarshal([]byte(collectorsJSON.String), &mt.SuggestedCollectors)
		}
		if schemaJSON != nil {
			json.Unmarshal(schemaJSON, &mt.Schema)
		}
		types = append(types, mt)
	}
	return types, nil
}

// CountCIModelTypes 统计CI模型类型数量
func (r *Repository) CountCIModelTypes(ctx context.Context, tenantID string) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM cmdb_ci_model_type WHERE tenant_id = $1`, tenantID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count ci model types: %w", err)
	}
	return count, nil
}

// UpdateCIModelType 更新CI模型类型
func (r *Repository) UpdateCIModelType(ctx context.Context, mt *model.CIModelType) error {
	mt.UpdatedAt = time.Now()

	schemaJSON, err := json.Marshal(mt.Schema)
	if err != nil {
		return fmt.Errorf("marshal schema: %w", err)
	}

	_, err = r.db.ExecContext(ctx, `
		UPDATE cmdb_ci_model_type
		SET name = $1, description = $2, parent_id = $3, schema = $4, category = $5, sub_category = $6, icon = $7, show_in_menu = $8, abstract = $9, source = $10, discovered_by = $11, confidence = $12, evidence = $13, version = $14, suggested_collectors = $15, updated_at = $16
		WHERE id = $17 AND tenant_id = $18`,
		mt.Name, nullIfEmpty(mt.Description), nullIfEmpty(mt.ParentID),
		schemaJSON, nullIfEmpty(mt.Category), nullIfEmpty(mt.SubCategory), nullIfEmpty(mt.Icon),
		mt.ShowInMenu, mt.Abstract, nullIfEmpty(mt.Source), nullIfEmpty(mt.DiscoveredBy),
		mt.Confidence, toJSON(mt.Evidence), mt.Version, toJSON(mt.SuggestedCollectors),
		mt.UpdatedAt, mt.ID, mt.TenantID)
	if err != nil {
		return fmt.Errorf("update ci model type: %w", err)
	}
	return nil
}

// GetCIModelTypeByName 按名称查模型类型（用于导入重名检测）。
func (r *Repository) GetCIModelTypeByName(ctx context.Context, name, tenantID string) (*model.CIModelType, error) {
	mt := &model.CIModelType{}
	var schemaJSON []byte
	var desc, parentID, category, subCategory, icon, author, source, discoveredBy, evidenceJSON, collectorsJSON sql.NullString
	var showInMenu, builtin, abstract sql.NullBool
	var confidence sql.NullFloat64
	var version sql.NullInt64

	err := r.db.QueryRowContext(ctx, `
		SELECT id, tenant_id, name, description, parent_id, schema, category, sub_category, icon, show_in_menu, builtin, author, abstract, source, discovered_by, confidence, evidence, version, suggested_collectors, created_at, updated_at
		FROM cmdb_ci_model_type WHERE name = $1 AND tenant_id = $2`, name, tenantID).Scan(
		&mt.ID, &mt.TenantID, &mt.Name, &desc, &parentID, &schemaJSON, &category, &subCategory, &icon, &showInMenu, &builtin, &author, &abstract, &source, &discoveredBy, &confidence, &evidenceJSON, &version, &collectorsJSON, &mt.CreatedAt, &mt.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("query ci model type by name: %w", err)
	}
	mt.Description = desc.String
	mt.ParentID = parentID.String
	mt.Category = category.String
	mt.SubCategory = subCategory.String
	mt.Icon = icon.String
	mt.Author = author.String
	mt.ShowInMenu = showInMenu.Bool
	mt.Builtin = builtin.Bool
	mt.Abstract = abstract.Bool
	mt.Source = source.String
	mt.DiscoveredBy = discoveredBy.String
	mt.Confidence = confidence.Float64
	mt.Version = int(version.Int64)
	if evidenceJSON.Valid && evidenceJSON.String != "" {
		_ = json.Unmarshal([]byte(evidenceJSON.String), &mt.Evidence)
	}
	if collectorsJSON.Valid && collectorsJSON.String != "" {
		_ = json.Unmarshal([]byte(collectorsJSON.String), &mt.SuggestedCollectors)
	}
	if schemaJSON != nil {
		json.Unmarshal(schemaJSON, &mt.Schema)
	}
	return mt, nil
}

// DeleteCIModelType 删除CI模型类型
func (r *Repository) DeleteCIModelType(ctx context.Context, id, tenantID string) error {
	_, err := r.db.ExecContext(ctx, `
		DELETE FROM cmdb_ci_model_type WHERE id = $1 AND tenant_id = $2`, id, tenantID)
	if err != nil {
		return fmt.Errorf("delete ci model type: %w", err)
	}
	return nil
}

// CountCIByTypeID 统计使用指定模型类型的CI数量
func (r *Repository) CountCIByTypeID(ctx context.Context, typeID, tenantID string) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM cmdb_ci WHERE type_id = $1 AND tenant_id = $2`, typeID, tenantID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count ci by type id: %w", err)
	}
	return count, nil
}

// ============================================================
// Version History
// ============================================================

// SaveCIVersion 保存CI版本快照
func (r *Repository) SaveCIVersion(ctx context.Context, v *model.CIVersion) error {
	v.ID = uuid.New().String()
	attrsJSON, err := v.MarshalAttributes()
	if err != nil {
		return fmt.Errorf("marshal attributes: %w", err)
	}
	labelsJSON, err := v.MarshalLabels()
	if err != nil {
		return fmt.Errorf("marshal labels: %w", err)
	}
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO cmdb_ci_version (id, ci_id, tenant_id, version, name, type_id, status, attributes, labels, operator, change_type, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		v.ID, v.CIID, v.TenantID, v.Version, v.Name, v.TypeID, v.Status, attrsJSON, labelsJSON, v.Operator, v.ChangeType, v.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert ci version: %w", err)
	}
	return nil
}

// ListCIVersions 获取CI的历史版本列表
func (r *Repository) ListCIVersions(ctx context.Context, ciID, tenantID string, limit, offset int) ([]*model.CIVersion, int, error) {
	if limit <= 0 {
		limit = 20
	}
	var total int
	err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM cmdb_ci_version WHERE ci_id = $1 AND tenant_id = $2`, ciID, tenantID).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("count ci versions: %w", err)
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT id, ci_id, tenant_id, version, name, type_id, status, attributes, labels, operator, change_type, created_at
		FROM cmdb_ci_version WHERE ci_id = $1 AND tenant_id = $2
		ORDER BY version DESC LIMIT $3 OFFSET $4`, ciID, tenantID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list ci versions: %w", err)
	}
	defer rows.Close()

	var versions []*model.CIVersion
	for rows.Next() {
		v := &model.CIVersion{}
		var attrsJSON, labelsJSON []byte
		if err := rows.Scan(&v.ID, &v.CIID, &v.TenantID, &v.Version, &v.Name, &v.TypeID, &v.Status, &attrsJSON, &labelsJSON, &v.Operator, &v.ChangeType, &v.CreatedAt); err != nil {
			return nil, 0, fmt.Errorf("scan ci version: %w", err)
		}
		v.UnmarshalAttributes(attrsJSON)
		v.UnmarshalLabels(labelsJSON)
		versions = append(versions, v)
	}
	return versions, total, rows.Err()
}

// ListAllCIHistory 获取所有CI的变更历史（跨CI）
func (r *Repository) ListAllCIHistory(ctx context.Context, tenantID string, changeType string, limit, offset int) ([]*model.CIVersion, int, error) {
	if limit <= 0 {
		limit = 50
	}
	var total int
	query := `SELECT COUNT(*) FROM cmdb_ci_version WHERE tenant_id = $1`
	args := []any{tenantID}
	if changeType != "" {
		query += ` AND change_type = $2`
		args = append(args, changeType)
	}
	err := r.db.QueryRowContext(ctx, query, args...).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("count history: %w", err)
	}

	query = `SELECT id, ci_id, tenant_id, version, name, type_id, status, attributes, labels, operator, change_type, created_at
		FROM cmdb_ci_version WHERE tenant_id = $1`
	args = []any{tenantID}
	if changeType != "" {
		query += ` AND change_type = $2`
		args = append(args, changeType)
	}
	query += ` ORDER BY created_at DESC LIMIT $` + fmt.Sprintf("%d", len(args)+1) + ` OFFSET $` + fmt.Sprintf("%d", len(args)+2)
	args = append(args, limit, offset)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list history: %w", err)
	}
	defer rows.Close()

	var versions []*model.CIVersion
	for rows.Next() {
		v := &model.CIVersion{}
		var attrsJSON, labelsJSON []byte
		if err := rows.Scan(&v.ID, &v.CIID, &v.TenantID, &v.Version, &v.Name, &v.TypeID, &v.Status, &attrsJSON, &labelsJSON, &v.Operator, &v.ChangeType, &v.CreatedAt); err != nil {
			return nil, 0, fmt.Errorf("scan version: %w", err)
		}
		v.UnmarshalAttributes(attrsJSON)
		v.UnmarshalLabels(labelsJSON)
		versions = append(versions, v)
	}
	return versions, total, rows.Err()
}

// GetCIVersion 获取CI的指定版本
func (r *Repository) GetCIVersion(ctx context.Context, id string) (*model.CIVersion, error) {
	v := &model.CIVersion{}
	var attrsJSON, labelsJSON []byte
	err := r.db.QueryRowContext(ctx, `
		SELECT id, ci_id, tenant_id, version, name, type_id, status, attributes, labels, operator, change_type, created_at
		FROM cmdb_ci_version WHERE id = $1`, id).Scan(&v.ID, &v.CIID, &v.TenantID, &v.Version, &v.Name, &v.TypeID, &v.Status, &attrsJSON, &labelsJSON, &v.Operator, &v.ChangeType, &v.CreatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get ci version: %w", err)
	}
	v.UnmarshalAttributes(attrsJSON)
	v.UnmarshalLabels(labelsJSON)
	return v, nil
}

// GetLatestVersionNumber 获取CI最新版本号
func (r *Repository) GetLatestVersionNumber(ctx context.Context, ciID string) (int, error) {
	var ver int
	err := r.db.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(version), 0) FROM cmdb_ci_version WHERE ci_id = $1`, ciID).Scan(&ver)
	if err != nil {
		return 0, fmt.Errorf("get latest version: %w", err)
	}
	return ver, nil
}

// ============================================================
// Discovery Tasks
// ============================================================

// CreateDiscoveryTask 创建发现任务
func (r *Repository) CreateDiscoveryTask(ctx context.Context, t *model.DiscoveryTask) error {
	t.ID = uuid.New().String()
	now := time.Now()
	t.CreatedAt = now
	t.UpdatedAt = now
	t.Status = "idle"

	configJSON, err := t.MarshalConfig()
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	_, err = r.db.ExecContext(ctx, `
		INSERT INTO cmdb_discovery_task (id, tenant_id, name, type, target, status, config, found_count, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		t.ID, t.TenantID, t.Name, t.Type, t.Target, t.Status, configJSON, t.FoundCount, t.CreatedAt, t.UpdatedAt)
	if err != nil {
		return fmt.Errorf("insert discovery task: %w", err)
	}
	return nil
}

// GetDiscoveryTask 获取发现任务
func (r *Repository) GetDiscoveryTask(ctx context.Context, id, tenantID string) (*model.DiscoveryTask, error) {
	t := &model.DiscoveryTask{}
	var configJSON []byte
	var lastRunAt sql.NullTime
	err := r.db.QueryRowContext(ctx, `
		SELECT id, tenant_id, name, type, target, status, config, found_count, last_run_at, created_at, updated_at
		FROM cmdb_discovery_task WHERE id = $1 AND tenant_id = $2`, id, tenantID).Scan(
		&t.ID, &t.TenantID, &t.Name, &t.Type, &t.Target, &t.Status, &configJSON, &t.FoundCount, &lastRunAt, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get discovery task: %w", err)
	}
	t.UnmarshalConfig(configJSON)
	if lastRunAt.Valid {
		t.LastRunAt = &lastRunAt.Time
	}
	return t, nil
}

// ListDiscoveryTasks 获取发现任务列表
func (r *Repository) ListDiscoveryTasks(ctx context.Context, tenantID string) ([]*model.DiscoveryTask, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, tenant_id, name, type, target, status, config, found_count, last_run_at, created_at, updated_at
		FROM cmdb_discovery_task WHERE tenant_id = $1 ORDER BY created_at DESC`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list discovery tasks: %w", err)
	}
	defer rows.Close()

	var tasks []*model.DiscoveryTask
	for rows.Next() {
		t := &model.DiscoveryTask{}
		var configJSON []byte
		var lastRunAt sql.NullTime
		if err := rows.Scan(&t.ID, &t.TenantID, &t.Name, &t.Type, &t.Target, &t.Status, &configJSON, &t.FoundCount, &lastRunAt, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan discovery task: %w", err)
		}
		t.UnmarshalConfig(configJSON)
		if lastRunAt.Valid {
			t.LastRunAt = &lastRunAt.Time
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

// UpdateDiscoveryTask 更新发现任务
func (r *Repository) UpdateDiscoveryTask(ctx context.Context, t *model.DiscoveryTask) error {
	t.UpdatedAt = time.Now()
	configJSON, err := t.MarshalConfig()
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	_, err = r.db.ExecContext(ctx, `
		UPDATE cmdb_discovery_task SET name=$1, type=$2, target=$3, status=$4, config=$5, found_count=$6, last_run_at=$7, updated_at=$8
		WHERE id=$9 AND tenant_id=$10`,
		t.Name, t.Type, t.Target, t.Status, configJSON, t.FoundCount, t.LastRunAt, t.UpdatedAt, t.ID, t.TenantID)
	if err != nil {
		return fmt.Errorf("update discovery task: %w", err)
	}
	return nil
}

// DeleteDiscoveryTask 删除发现任务
func (r *Repository) DeleteDiscoveryTask(ctx context.Context, id, tenantID string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM cmdb_discovery_task WHERE id = $1 AND tenant_id = $2`, id, tenantID)
	if err != nil {
		return fmt.Errorf("delete discovery task: %w", err)
	}
	return nil
}

// ============================================================
// Discovery Results
// ============================================================

// CreateDiscoveryResult 创建发现结果
func (r *Repository) CreateDiscoveryResult(ctx context.Context, res *model.DiscoveryResult) error {
	res.ID = uuid.New().String()
	attrsJSON, err := res.MarshalAttributes()
	if err != nil {
		return fmt.Errorf("marshal attributes: %w", err)
	}
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO cmdb_discovery_result (id, task_id, tenant_id, ci_name, ci_type, ip_address, attributes, source, synced, synced_ci_id, discovered_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		res.ID, res.TaskID, res.TenantID, res.CIName, res.CIType, res.IPAddress, attrsJSON, res.Source, res.Synced, nullIfEmpty(res.SyncedCIID), res.DiscoveredAt)
	if err != nil {
		return fmt.Errorf("insert discovery result: %w", err)
	}
	return nil
}

// ListDiscoveryResults 获取发现结果列表
func (r *Repository) ListDiscoveryResults(ctx context.Context, taskID, tenantID string) ([]*model.DiscoveryResult, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, task_id, tenant_id, ci_name, ci_type, ip_address, attributes, source, synced, COALESCE(synced_ci_id,''), discovered_at
		FROM cmdb_discovery_result WHERE task_id = $1 AND tenant_id = $2 ORDER BY discovered_at DESC`, taskID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list discovery results: %w", err)
	}
	defer rows.Close()

	var results []*model.DiscoveryResult
	for rows.Next() {
		res := &model.DiscoveryResult{}
		var attrsJSON []byte
		if err := rows.Scan(&res.ID, &res.TaskID, &res.TenantID, &res.CIName, &res.CIType, &res.IPAddress, &attrsJSON, &res.Source, &res.Synced, &res.SyncedCIID, &res.DiscoveredAt); err != nil {
			return nil, fmt.Errorf("scan discovery result: %w", err)
		}
		res.UnmarshalAttributes(attrsJSON)
		results = append(results, res)
	}
	return results, rows.Err()
}

// MarkDiscoveryResultSynced 标记发现结果已同步
func (r *Repository) MarkDiscoveryResultSynced(ctx context.Context, id, ciID string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE cmdb_discovery_result SET synced = TRUE, synced_ci_id = $1 WHERE id = $2`, ciID, id)
	if err != nil {
		return fmt.Errorf("mark discovery result synced: %w", err)
	}
	return nil
}

// SyncAllDiscoveryResults 批量标记所有未同步结果为已同步
func (r *Repository) SyncAllDiscoveryResults(ctx context.Context, taskID, tenantID string) (int64, error) {
	result, err := r.db.ExecContext(ctx, `
		UPDATE cmdb_discovery_result SET synced = TRUE WHERE task_id = $1 AND tenant_id = $2 AND synced = FALSE`, taskID, tenantID)
	if err != nil {
		return 0, fmt.Errorf("sync all discovery results: %w", err)
	}
	return result.RowsAffected()
}

// CountDiscoveryStats 统计发现结果
func (r *Repository) CountDiscoveryStats(ctx context.Context, tenantID string) (total, synced int64, err error) {
	err = r.db.QueryRowContext(ctx, `
		SELECT COUNT(*), COUNT(*) FILTER (WHERE synced = TRUE) FROM cmdb_discovery_result WHERE tenant_id = $1`, tenantID).Scan(&total, &synced)
	if err != nil {
		return 0, 0, fmt.Errorf("count discovery stats: %w", err)
	}
	return total, synced, nil
}
