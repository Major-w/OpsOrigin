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
	ErrAntiFlapping  = errors.New("channel weight is lower than existing")
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

func (r *Repository) CreateCIModelType(ctx context.Context, mt *model.CIModelType) error {
	mt.ID = uuid.New().String()
	now := time.Now()
	mt.CreatedAt = now
	mt.UpdatedAt = now

	schemaJSON, err := json.Marshal(mt.Schema)
	if err != nil {
		return fmt.Errorf("marshal schema: %w", err)
	}

	_, err = r.db.ExecContext(ctx, `
		INSERT INTO cmdb_ci_model_type (id, tenant_id, name, description, parent_id, schema, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		mt.ID, mt.TenantID, mt.Name, nullIfEmpty(mt.Description), nullIfEmpty(mt.ParentID),
		schemaJSON, mt.CreatedAt, mt.UpdatedAt)
	if err != nil {
		return fmt.Errorf("insert ci model type: %w", err)
	}
	return nil
}

func (r *Repository) GetCIModelType(ctx context.Context, id, tenantID string) (*model.CIModelType, error) {
	mt := &model.CIModelType{}
	var schemaJSON []byte
	var desc, parentID sql.NullString

	err := r.db.QueryRowContext(ctx, `
		SELECT id, tenant_id, name, description, parent_id, schema, created_at, updated_at
		FROM cmdb_ci_model_type WHERE id = $1 AND tenant_id = $2`, id, tenantID).Scan(
		&mt.ID, &mt.TenantID, &mt.Name, &desc, &parentID, &schemaJSON, &mt.CreatedAt, &mt.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("query ci model type: %w", err)
	}
	mt.Description = desc.String
	mt.ParentID = parentID.String
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

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO cmdb_ci (id, type_id, tenant_id, name, description, status, attributes, labels, channel, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		ci.ID, ci.TypeID, ci.TenantID, ci.Name, nullIfEmpty(ci.Description),
		string(ci.Status), attrsJSON, labelsJSON, int(ci.Channel), ci.CreatedAt, ci.UpdatedAt)
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

func (r *Repository) UpdateCI(ctx context.Context, ci *model.CIModel, channel model.ChannelWeight, traceID string) error {
	existing, err := r.getCIByID(ctx, ci.ID)
	if err != nil {
		return err
	}
	if existing == nil {
		return errors.New("ci not found")
	}

	if channel < model.ChannelWeight(existing.Channel) {
		return fmt.Errorf("%w: got %d, existing %d (anti-flapping)",
			ErrAntiFlapping, channel, existing.Channel)
	}

	changed := false
	changeLog := make([]model.AttributeChange, 0)

	ci.UpdatedAt = time.Now()

	for k, newVal := range ci.Attributes {
		oldVal, exists := existing.Attributes[k]
		if exists && deepEqual(oldVal, newVal) {
			continue
		}
		changed = true
		changeLog = append(changeLog, model.AttributeChange{
			CIID:        ci.ID,
			AttrName:    k,
			OldValue:    oldVal,
			NewValue:    newVal,
			Channel:     channel,
			ChangedAt:   ci.UpdatedAt,
		})
	}

	if !changed {
		return nil
	}

	attrsJSON, err := json.Marshal(ci.Attributes)
	if err != nil {
		return fmt.Errorf("marshal attributes: %w", err)
	}
	labelsJSON, err := json.Marshal(ci.Labels)
	if err != nil {
		return fmt.Errorf("marshal labels: %w", err)
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `
		UPDATE cmdb_ci SET name=$1, description=$2, status=$3, attributes=$4, labels=$5, channel=$6, updated_at=$7
		WHERE id=$8 AND tenant_id=$9`,
		ci.Name, nullIfEmpty(ci.Description), string(ci.Status), attrsJSON, labelsJSON,
		int(channel), ci.UpdatedAt, ci.ID, ci.TenantID)
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
		"ci_id":       ci.ID,
		"change_log":  changeLog,
		"attributes":  ci.Attributes,
	})
	if err := r.insertOutbox(ctx, tx, ci.TenantID, model.EventCIUpdated, "ci", ci.ID, string(payload), traceID); err != nil {
		return err
	}

	return tx.Commit()
}

func (r *Repository) GetCI(ctx context.Context, id, tenantID string) (*model.CIModel, error) {
	return r.getCIByIDAndTenant(ctx, id, tenantID)
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

func (r *Repository) ListCIPaginated(ctx context.Context, tenantID, typeID, sortBy, sortOrder string, limit, offset int) ([]*model.CIModel, error) {
	query := `SELECT id, type_id, tenant_id, name, description, status, attributes, labels, channel, created_at, updated_at
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
		SELECT id, type_id, tenant_id, name, description, status, attributes, labels, channel, created_at, updated_at
		FROM cmdb_ci WHERE id = $1`, id)
	return scanCI(row)
}

func (r *Repository) getCIByIDAndTenant(ctx context.Context, id, tenantID string) (*model.CIModel, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, type_id, tenant_id, name, description, status, attributes, labels, channel, created_at, updated_at
		FROM cmdb_ci WHERE id = $1 AND tenant_id = $2`, id, tenantID)
	return scanCI(row)
}

type scannable interface {
	Scan(dest ...any) error
}

func scanCI(row scannable) (*model.CIModel, error) {
	ci := &model.CIModel{}
	var desc sql.NullString
	var attrsJSON, labelsJSON []byte
	var channel int

	err := row.Scan(&ci.ID, &ci.TypeID, &ci.TenantID, &ci.Name, &desc, &ci.Status,
		&attrsJSON, &labelsJSON, &channel, &ci.CreatedAt, &ci.UpdatedAt)
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
	return ci, nil
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
		SELECT id, tenant_id, name, description, parent_id, schema, created_at, updated_at
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
		var desc, parentID sql.NullString
		
		err := rows.Scan(&mt.ID, &mt.TenantID, &mt.Name, &desc, &parentID, &schemaJSON, &mt.CreatedAt, &mt.UpdatedAt)
		if err != nil {
			return nil, fmt.Errorf("scan ci model type: %w", err)
		}
		mt.Description = desc.String
		mt.ParentID = parentID.String
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
		SET name = $1, description = $2, parent_id = $3, schema = $4, updated_at = $5
		WHERE id = $6 AND tenant_id = $7`,
		mt.Name, nullIfEmpty(mt.Description), nullIfEmpty(mt.ParentID),
		schemaJSON, mt.UpdatedAt, mt.ID, mt.TenantID)
	if err != nil {
		return fmt.Errorf("update ci model type: %w", err)
	}
	return nil
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
