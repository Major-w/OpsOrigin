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
)

// migrateP2 建 P2 相关表：字段规则 / 订阅 / 订阅投递日志 / IDC 机房机柜 U 位。
func (r *Repository) migrateP2(ctx context.Context) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS cmdb_field_rule (
			id            TEXT PRIMARY KEY,
			tenant_id     TEXT NOT NULL DEFAULT 'default',
			model_type_id TEXT NOT NULL,
			field         TEXT NOT NULL,
			permission    TEXT NOT NULL,
			action        TEXT NOT NULL,
			created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			UNIQUE (tenant_id, model_type_id, field, permission)
		)`,
		`CREATE TABLE IF NOT EXISTS cmdb_subscription (
			id            TEXT PRIMARY KEY,
			tenant_id     TEXT NOT NULL DEFAULT 'default',
			name          TEXT NOT NULL,
			model_type_id TEXT,
			event_type    TEXT NOT NULL DEFAULT '*',
			channel       TEXT NOT NULL,
			target        TEXT NOT NULL DEFAULT '',
			enabled       BOOLEAN NOT NULL DEFAULT TRUE,
			created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS cmdb_subscription_delivery (
			id         TEXT PRIMARY KEY,
			sub_id     TEXT NOT NULL,
			tenant_id  TEXT NOT NULL DEFAULT 'default',
			event_type TEXT NOT NULL,
			ci_id      TEXT NOT NULL,
			payload    TEXT NOT NULL DEFAULT '',
			status     TEXT NOT NULL,
			error      TEXT,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS cmdb_idc (
			id          TEXT PRIMARY KEY,
			tenant_id   TEXT NOT NULL DEFAULT 'default',
			name        TEXT NOT NULL,
			location    TEXT,
			description TEXT,
			created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS cmdb_rack (
			id         TEXT PRIMARY KEY,
			tenant_id  TEXT NOT NULL DEFAULT 'default',
			idc_id     TEXT NOT NULL,
			name       TEXT NOT NULL,
			row        TEXT,
			col        TEXT,
			max_units  INT NOT NULL DEFAULT 42,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			UNIQUE (tenant_id, idc_id, name)
		)`,
		`CREATE TABLE IF NOT EXISTS cmdb_rack_unit (
			rack_id TEXT NOT NULL,
			unit    INT  NOT NULL,
			ci_id   TEXT,
			label   TEXT,
			PRIMARY KEY (rack_id, unit)
		)`,
	}
	for _, s := range stmts {
		if _, err := r.db.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("migrate p2: %w", err)
		}
	}
	return nil
}

// ============================================================
// P2-1 统计
// ============================================================

// Stats 聚合 IT 资源统计（模型/实例/关系/分类/状态/属性）。
func (r *Repository) Stats(ctx context.Context, tenantID string) (*model.CMDBStats, error) {
	// 预初始化各聚合切片为空数组(非 nil), 避免无数据时序列化为 JSON null,
	// 否则前端直接访问 .length 会抛 TypeError 导致整页崩溃(资源统计页报错)。
	st := &model.CMDBStats{
		ByCategory: []model.CategoryStat{},
		ByStatus:   []model.StatusStat{},
		ByType:     []model.TypeStat{},
		ByAttr:     []model.AttrStat{},
	}
	row := r.db.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM cmdb_ci_model_type WHERE tenant_id=$1),
		(SELECT COUNT(*) FROM cmdb_ci WHERE tenant_id=$1 AND status<>'deleted'),
		(SELECT COUNT(*) FROM cmdb_ci_relation WHERE tenant_id=$1)`, tenantID)
	if err := row.Scan(&st.TotalModels, &st.TotalInstances, &st.TotalRelations); err != nil {
		return nil, fmt.Errorf("stats totals: %w", err)
	}

	// 按分类聚合（模型数 + 实例数）
	rows, err := r.db.QueryContext(ctx, `
		SELECT COALESCE(mt.category,'未分类') AS cat,
		       COUNT(DISTINCT mt.id) AS models,
		       COUNT(DISTINCT c.id) AS instances
		FROM cmdb_ci_model_type mt
		LEFT JOIN cmdb_ci c ON c.type_id = mt.id AND c.tenant_id=$1 AND c.status<>'deleted'
		WHERE mt.tenant_id=$1
		GROUP BY COALESCE(mt.category,'未分类')
		ORDER BY instances DESC`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("stats by category: %w", err)
	}
	for rows.Next() {
		cs := model.CategoryStat{}
		if err := rows.Scan(&cs.Category, &cs.Models, &cs.Instances); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan category stat: %w", err)
		}
		st.ByCategory = append(st.ByCategory, cs)
	}
	rows.Close()

	// 按状态聚合
	rows, err = r.db.QueryContext(ctx, `
		SELECT status, COUNT(*) FROM cmdb_ci WHERE tenant_id=$1 AND status<>'deleted' GROUP BY status ORDER BY count DESC`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("stats by status: %w", err)
	}
	for rows.Next() {
		ss := model.StatusStat{}
		if err := rows.Scan(&ss.Status, &ss.Count); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan status stat: %w", err)
		}
		st.ByStatus = append(st.ByStatus, ss)
	}
	rows.Close()

	// 按模型类型聚合（Top 12 实例数）
	rows, err = r.db.QueryContext(ctx, `
		SELECT mt.id, mt.name, COALESCE(mt.category,'未分类'), COUNT(c.id)
		FROM cmdb_ci_model_type mt
		LEFT JOIN cmdb_ci c ON c.type_id = mt.id AND c.tenant_id=$1 AND c.status<>'deleted'
		WHERE mt.tenant_id=$1
		GROUP BY mt.id, mt.name, mt.category
		ORDER BY COUNT(c.id) DESC LIMIT 12`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("stats by type: %w", err)
	}
	for rows.Next() {
		ts := model.TypeStat{}
		if err := rows.Scan(&ts.TypeID, &ts.TypeName, &ts.Category, &ts.Count); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan type stat: %w", err)
		}
		st.ByType = append(st.ByType, ts)
	}
	rows.Close()

	// 按属性聚合：统计出现频率最高的属性键（排除系统元数据），取其 top 2 键的值分布
	rows, err = r.db.QueryContext(ctx, `
		SELECT attributes FROM cmdb_ci WHERE tenant_id=$1 AND status<>'deleted' AND attributes::text <> '{}' LIMIT 2000`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("stats attrs: %w", err)
	}
	skip := map[string]bool{"resource_id": true, "provider": true}
	freq := map[string]int{}
	values := map[string]map[string]int{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan attrs: %w", err)
		}
		var attrs map[string]any
		json.Unmarshal(raw, &attrs)
		for k, v := range attrs {
			if skip[k] || k == "" {
				continue
			}
			if s, ok := v.(string); ok && s != "" && len(s) < 64 {
				freq[k]++
				if values[k] == nil {
					values[k] = map[string]int{}
				}
				values[k][s]++
			}
		}
	}
	rows.Close()
	// top 2 属性键，每键 top 5 值
	type kv struct {
		key string
		n   int
	}
	var ks []kv
	for k, n := range freq {
		ks = append(ks, kv{k, n})
	}
	for i := 0; i < len(ks); i++ {
		for j := i + 1; j < len(ks); j++ {
			if ks[j].n > ks[i].n {
				ks[i], ks[j] = ks[j], ks[i]
			}
		}
	}
	if len(ks) > 2 {
		ks = ks[:2]
	}
	for _, k := range ks {
		var vs []kv
		for v, n := range values[k.key] {
			vs = append(vs, kv{v, n})
		}
		for i := 0; i < len(vs); i++ {
			for j := i + 1; j < len(vs); j++ {
				if vs[j].n > vs[i].n {
					vs[i], vs[j] = vs[j], vs[i]
				}
			}
		}
		if len(vs) > 5 {
			vs = vs[:5]
		}
		for _, v := range vs {
			st.ByAttr = append(st.ByAttr, model.AttrStat{Key: k.key, Value: v.key, Count: v.n})
		}
	}
	return st, nil
}

// ============================================================
// P2-2 IDC / Rack / U 位
// ============================================================

func (r *Repository) CreateIDCRoom(ctx context.Context, room *model.IDCRoom) error {
	room.ID = uuid.New().String()
	room.CreatedAt = time.Now()
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO cmdb_idc (id, tenant_id, name, location, description, created_at)
		VALUES ($1,$2,$3,$4,$5,$6)`,
		room.ID, room.TenantID, room.Name, nullIfEmpty(room.Location), nullIfEmpty(room.Description), room.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert idc: %w", err)
	}
	return nil
}

func (r *Repository) ListIDCRooms(ctx context.Context, tenantID string) ([]*model.IDCRoom, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, tenant_id, name, COALESCE(location,''), COALESCE(description,''), created_at
		FROM cmdb_idc WHERE tenant_id=$1 ORDER BY created_at`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list idc: %w", err)
	}
	defer rows.Close()
	out := make([]*model.IDCRoom, 0)
	for rows.Next() {
		room := &model.IDCRoom{}
		var loc, desc sql.NullString
		if err := rows.Scan(&room.ID, &room.TenantID, &room.Name, &loc, &desc, &room.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan idc: %w", err)
		}
		room.Location = loc.String
		room.Description = desc.String
		out = append(out, room)
	}
	return out, rows.Err()
}

func (r *Repository) DeleteIDCRoom(ctx context.Context, id, tenantID string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM cmdb_rack_unit WHERE rack_id IN (SELECT id FROM cmdb_rack WHERE idc_id=$1 AND tenant_id=$2)`, id, tenantID); err != nil {
		return fmt.Errorf("cascade rack units: %w", err)
	}
	if _, err := r.db.ExecContext(ctx, `DELETE FROM cmdb_rack WHERE idc_id=$1 AND tenant_id=$2`, id, tenantID); err != nil {
		return fmt.Errorf("cascade racks: %w", err)
	}
	if _, err := r.db.ExecContext(ctx, `DELETE FROM cmdb_idc WHERE id=$1 AND tenant_id=$2`, id, tenantID); err != nil {
		return fmt.Errorf("delete idc: %w", err)
	}
	return nil
}

func (r *Repository) CreateRack(ctx context.Context, rack *model.Rack) error {
	rack.ID = uuid.New().String()
	rack.CreatedAt = time.Now()
	if rack.MaxUnits <= 0 {
		rack.MaxUnits = 42
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO cmdb_rack (id, tenant_id, idc_id, name, row, col, max_units, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		rack.ID, rack.TenantID, rack.IDCRoomID, rack.Name, nullIfEmpty(rack.Row), nullIfEmpty(rack.Col), rack.MaxUnits, rack.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert rack: %w", err)
	}
	return nil
}

func (r *Repository) ListRacks(ctx context.Context, tenantID, idcID string) ([]*model.Rack, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, tenant_id, idc_id, name, COALESCE(row,''), COALESCE(col,''), max_units, created_at
		FROM cmdb_rack WHERE tenant_id=$1 AND idc_id=$2 ORDER BY name`, tenantID, idcID)
	if err != nil {
		return nil, fmt.Errorf("list racks: %w", err)
	}
	defer rows.Close()
	out := make([]*model.Rack, 0)
	for rows.Next() {
		rack := &model.Rack{}
		var rw, col sql.NullString
		if err := rows.Scan(&rack.ID, &rack.TenantID, &rack.IDCRoomID, &rack.Name, &rw, &col, &rack.MaxUnits, &rack.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan rack: %w", err)
		}
		rack.Row = rw.String
		rack.Col = col.String
		out = append(out, rack)
	}
	return out, rows.Err()
}

func (r *Repository) DeleteRack(ctx context.Context, id, tenantID string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM cmdb_rack_unit WHERE rack_id=$1`, id); err != nil {
		return fmt.Errorf("cascade rack units: %w", err)
	}
	if _, err := r.db.ExecContext(ctx, `DELETE FROM cmdb_rack WHERE id=$1 AND tenant_id=$2`, id, tenantID); err != nil {
		return fmt.Errorf("delete rack: %w", err)
	}
	return nil
}

// ListRackUnits 返回机柜全部 U 位（含空闲）。
func (r *Repository) ListRackUnits(ctx context.Context, rackID, tenantID string) ([]*model.RackUnit, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT u.rack_id, u.unit, COALESCE(u.ci_id,''), COALESCE(u.label,'')
		FROM cmdb_rack_unit u
		JOIN cmdb_rack ra ON ra.id = u.rack_id AND ra.tenant_id=$2
		WHERE u.rack_id=$1 ORDER BY u.unit`, rackID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list rack units: %w", err)
	}
	defer rows.Close()
	out := make([]*model.RackUnit, 0)
	for rows.Next() {
		u := &model.RackUnit{}
		var ci, label sql.NullString
		if err := rows.Scan(&u.RackID, &u.Unit, &ci, &label); err != nil {
			return nil, fmt.Errorf("scan rack unit: %w", err)
		}
		u.CIID = ci.String
		u.Label = label.String
		out = append(out, u)
	}
	return out, rows.Err()
}

// AssignRackUnits 批量 upsert U 位占用；检测同一 CI 已占用其它机柜 U 位（非法占用）。
func (r *Repository) AssignRackUnits(ctx context.Context, rackID, tenantID string, units []*model.RackUnit) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// 校验机柜归属
	var maxUnits int
	if err := tx.QueryRowContext(ctx, `SELECT max_units FROM cmdb_rack WHERE id=$1 AND tenant_id=$2`, rackID, tenantID).Scan(&maxUnits); err != nil {
		return errors.New("rack not found")
	}
	for _, u := range units {
		if u.Unit < 1 || u.Unit > maxUnits {
			return fmt.Errorf("U 位 %d 超出机柜范围 1-%d", u.Unit, maxUnits)
		}
		if u.CIID != "" {
			// 非法占用检测：该 CI 是否已占用其它机柜的 U 位
			var other string
			err := tx.QueryRowContext(ctx, `
				SELECT u.rack_id FROM cmdb_rack_unit u
				WHERE u.ci_id=$1 AND u.rack_id<>$2 LIMIT 1`, u.CIID, rackID).Scan(&other)
			if err == nil {
				return fmt.Errorf("CI 已占用其它机柜 %s 的 U 位（非法占用）", other)
			}
			if !errors.Is(err, sql.ErrNoRows) && err != nil {
				return fmt.Errorf("check unit conflict: %w", err)
			}
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO cmdb_rack_unit (rack_id, unit, ci_id, label) VALUES ($1,$2,$3,$4)
			ON CONFLICT (rack_id, unit) DO UPDATE SET ci_id=EXCLUDED.ci_id, label=EXCLUDED.label`,
			rackID, u.Unit, nullIfEmpty(u.CIID), nullIfEmpty(u.Label)); err != nil {
			return fmt.Errorf("upsert rack unit: %w", err)
		}
	}
	return tx.Commit()
}

// ============================================================
// P2-4 字段级规则权限
// ============================================================

func (r *Repository) CreateFieldRule(ctx context.Context, rule *model.FieldRule) error {
	rule.ID = uuid.New().String()
	rule.CreatedAt = time.Now()
	if _, err := r.db.ExecContext(ctx, `
		INSERT INTO cmdb_field_rule (id, tenant_id, model_type_id, field, permission, action, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		rule.ID, rule.TenantID, rule.ModelTypeID, rule.Field, rule.Permission, string(rule.Action), rule.CreatedAt); err != nil {
		return fmt.Errorf("insert field rule: %w", err)
	}
	return nil
}

func (r *Repository) ListFieldRules(ctx context.Context, tenantID, modelTypeID string) ([]*model.FieldRule, error) {
	query := `SELECT id, tenant_id, model_type_id, field, permission, action, created_at FROM cmdb_field_rule WHERE tenant_id=$1`
	args := []any{tenantID}
	if modelTypeID != "" {
		query += ` AND model_type_id=$2`
		args = append(args, modelTypeID)
	}
	query += ` ORDER BY model_type_id, field`
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list field rules: %w", err)
	}
	defer rows.Close()
	out := make([]*model.FieldRule, 0)
	for rows.Next() {
		rule := &model.FieldRule{}
		var action string
		if err := rows.Scan(&rule.ID, &rule.TenantID, &rule.ModelTypeID, &rule.Field, &rule.Permission, &action, &rule.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan field rule: %w", err)
		}
		rule.Action = model.FieldRuleAction(action)
		out = append(out, rule)
	}
	return out, rows.Err()
}

func (r *Repository) DeleteFieldRule(ctx context.Context, id, tenantID string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM cmdb_field_rule WHERE id=$1 AND tenant_id=$2`, id, tenantID)
	if err != nil {
		return fmt.Errorf("delete field rule: %w", err)
	}
	return nil
}

// ============================================================
// P2-5 消息订阅
// ============================================================

func (r *Repository) CreateSubscription(ctx context.Context, sub *model.Subscription) error {
	sub.ID = uuid.New().String()
	sub.CreatedAt = time.Now()
	if sub.EventType == "" {
		sub.EventType = model.SubEventCIAll
	}
	if _, err := r.db.ExecContext(ctx, `
		INSERT INTO cmdb_subscription (id, tenant_id, name, model_type_id, event_type, channel, target, enabled, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		sub.ID, sub.TenantID, sub.Name, nullIfEmpty(sub.ModelTypeID), string(sub.EventType), string(sub.Channel), sub.Target, sub.Enabled, sub.CreatedAt); err != nil {
		return fmt.Errorf("insert subscription: %w", err)
	}
	return nil
}

func (r *Repository) ListSubscriptions(ctx context.Context, tenantID string) ([]*model.Subscription, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, tenant_id, name, COALESCE(model_type_id,''), event_type, channel, target, enabled, created_at
		FROM cmdb_subscription WHERE tenant_id=$1 ORDER BY created_at`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list subscriptions: %w", err)
	}
	defer rows.Close()
	out := make([]*model.Subscription, 0)
	for rows.Next() {
		sub := &model.Subscription{}
		var mt, target sql.NullString
		if err := rows.Scan(&sub.ID, &sub.TenantID, &sub.Name, &mt, &sub.EventType, &sub.Channel, &target, &sub.Enabled, &sub.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan subscription: %w", err)
		}
		sub.ModelTypeID = mt.String
		sub.Target = target.String
		out = append(out, sub)
	}
	return out, rows.Err()
}

func (r *Repository) UpdateSubscription(ctx context.Context, sub *model.Subscription) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE cmdb_subscription SET name=$2, model_type_id=$3, event_type=$4, channel=$5, target=$6, enabled=$7
		WHERE id=$1 AND tenant_id=$8`,
		sub.ID, sub.Name, nullIfEmpty(sub.ModelTypeID), string(sub.EventType), string(sub.Channel), sub.Target, sub.Enabled, sub.TenantID)
	if err != nil {
		return fmt.Errorf("update subscription: %w", err)
	}
	return nil
}

func (r *Repository) DeleteSubscription(ctx context.Context, id, tenantID string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM cmdb_subscription_delivery WHERE sub_id=$1 AND tenant_id=$2`, id, tenantID); err != nil {
		return fmt.Errorf("cascade deliveries: %w", err)
	}
	if _, err := r.db.ExecContext(ctx, `DELETE FROM cmdb_subscription WHERE id=$1 AND tenant_id=$2`, id, tenantID); err != nil {
		return fmt.Errorf("delete subscription: %w", err)
	}
	return nil
}

// ListEnabledSubscriptions 列出启用订阅（供事件分发）。
func (r *Repository) ListEnabledSubscriptions(ctx context.Context, tenantID string) ([]*model.Subscription, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, tenant_id, name, COALESCE(model_type_id,''), event_type, channel, target, enabled, created_at
		FROM cmdb_subscription WHERE tenant_id=$1 AND enabled=TRUE`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list enabled subscriptions: %w", err)
	}
	defer rows.Close()
	out := make([]*model.Subscription, 0)
	for rows.Next() {
		sub := &model.Subscription{}
		var mt, target sql.NullString
		if err := rows.Scan(&sub.ID, &sub.TenantID, &sub.Name, &mt, &sub.EventType, &sub.Channel, &target, &sub.Enabled, &sub.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan subscription: %w", err)
		}
		sub.ModelTypeID = mt.String
		sub.Target = target.String
		out = append(out, sub)
	}
	return out, rows.Err()
}

// RecordSubscriptionDelivery 写投递日志。
func (r *Repository) RecordSubscriptionDelivery(ctx context.Context, d *model.SubscriptionDelivery) error {
	if d.ID == "" {
		d.ID = uuid.New().String()
	}
	if d.CreatedAt.IsZero() {
		d.CreatedAt = time.Now()
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO cmdb_subscription_delivery (id, sub_id, tenant_id, event_type, ci_id, payload, status, error, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		d.ID, d.SubID, d.TenantID, d.EventType, d.CIID, d.Payload, d.Status, nullIfEmpty(d.Error), d.CreatedAt)
	if err != nil {
		return fmt.Errorf("record delivery: %w", err)
	}
	return nil
}

func (r *Repository) ListSubscriptionDeliveries(ctx context.Context, tenantID string, limit int) ([]*model.SubscriptionDelivery, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, sub_id, tenant_id, event_type, ci_id, payload, status, COALESCE(error,''), created_at
		FROM cmdb_subscription_delivery WHERE tenant_id=$1 ORDER BY created_at DESC LIMIT $2`, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("list deliveries: %w", err)
	}
	defer rows.Close()
	out := make([]*model.SubscriptionDelivery, 0)
	for rows.Next() {
		d := &model.SubscriptionDelivery{}
		var e sql.NullString
		if err := rows.Scan(&d.ID, &d.SubID, &d.TenantID, &d.EventType, &d.CIID, &d.Payload, &d.Status, &e, &d.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan delivery: %w", err)
		}
		d.Error = e.String
		out = append(out, d)
	}
	return out, rows.Err()
}

// —— 行级数据权限(P2-6) ——

// SaveDataPolicy 保存一条数据权限规则。
func (r *Repository) SaveDataPolicy(ctx context.Context, p *model.DataPolicy) error {
	if p.ID == "" {
		p.ID = uuid.New().String()
	}
	condJSON, err := json.Marshal(p.Condition)
	if err != nil {
		return fmt.Errorf("marshal policy condition: %w", err)
	}
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO cmdb_data_policy (id, tenant_id, role, model_type_id, condition, action, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (tenant_id, role, model_type_id, action) DO UPDATE
		SET condition = EXCLUDED.condition`,
		p.ID, p.TenantID, p.Role, p.ModelTypeID, condJSON, p.Action, time.Now())
	if err != nil {
		return fmt.Errorf("save data policy: %w", err)
	}
	return nil
}

// ListDataPolicies 列出数据权限规则(可按角色/模型过滤)。
func (r *Repository) ListDataPolicies(ctx context.Context, tenantID, role, modelTypeID string) ([]*model.DataPolicy, error) {
	q := `SELECT id, tenant_id, role, model_type_id, condition, action, created_at
		FROM cmdb_data_policy WHERE tenant_id = $1`
	args := []any{tenantID}
	if role != "" {
		q += ` AND role = $` + fmt.Sprint(len(args)+1)
		args = append(args, role)
	}
	if modelTypeID != "" {
		q += ` AND model_type_id = $` + fmt.Sprint(len(args)+1)
		args = append(args, modelTypeID)
	}
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list data policies: %w", err)
	}
	defer rows.Close()
	var out []*model.DataPolicy
	for rows.Next() {
		p := &model.DataPolicy{}
		var condJSON []byte
		if err := rows.Scan(&p.ID, &p.TenantID, &p.Role, &p.ModelTypeID, &condJSON, &p.Action, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan data policy: %w", err)
		}
		if len(condJSON) > 0 {
			json.Unmarshal(condJSON, &p.Condition)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// DeleteDataPolicy 删除数据权限规则。
func (r *Repository) DeleteDataPolicy(ctx context.Context, id, tenantID string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM cmdb_data_policy WHERE id = $1 AND tenant_id = $2`, id, tenantID)
	if err != nil {
		return fmt.Errorf("delete data policy: %w", err)
	}
	return nil
}
