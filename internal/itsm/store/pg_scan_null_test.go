package store

import (
	"regexp"
	"strings"
	"testing"
)

// 守卫：写入侧会产出 NULL 的列，读取侧不得直扫进裸 string/int/bool。
//
// itsm_tickets 的多数文本列是「可空且无默认值」，而 INSERT/UPDATE 用
// nullable(ticket.X) 把空串写成 NULL —— 也就是说【只要建单时不填描述，
// 库里那一行就是 NULL】，不是罕见边缘情况。
//
// 直扫裸 string 时 database/sql 会返回
// `sql: Scan error on column index N, name "...": converting NULL to string is unsupported`。
// 失败点是 rows.Scan，所以整条查询直接报错：
//
//	itsm: SLA check failed / itsm: OLA ticket list failed（每分钟刷一次）
//	itsm: hold auto-resume check failed
//
// 即 SLA 预警/违约升级、OLA 超时升级、挂起到期自动恢复这些后台任务
// 【整体静默失效】——不影响任何接口连通性，日志里只有一行 ERROR，极易漏掉。
//
// 规则：凡是在 pg.go 里以 nullable(ticket.X) 形式参与写入的字段，
//  1. 读取侧不得出现 &t.X（裸扫）；
//  2. 每个工单结果集都必须有 t.X = nullableStr(...) 回填，数量与 Scan 数严格一致。
//
// 写入侧新增一个 nullable(...) 字段而忘了补读取侧，这里就会红。
func TestPGTicketNullableColumnsNeverScannedBare(t *testing.T) {
	src := readPGSource(t)

	writeRe := regexp.MustCompile(`nullable\(ticket\.([A-Za-z]+)\)`)
	fields := map[string]bool{}
	for _, m := range writeRe.FindAllStringSubmatch(src, -1) {
		fields[m[1]] = true
	}
	if len(fields) == 0 {
		t.Fatal("未从 pg.go 解析到 nullable(ticket.X) 写入参数，守卫失效需同步本测试")
	}

	// 工单结果集的 Scan 都以这三个参数起头（Get/List/ListPaged/ListActive/ListOnHold）。
	// 若新增 SELECT 用了不同的局部变量名，这里会报「Scan 数为 0」提醒同步本测试。
	const scanAnchor = "&t.Title, &desc, &t.RiskLevel, &t.Status"
	scans := strings.Count(src, scanAnchor)
	if scans == 0 {
		t.Fatalf("未找到工单 Scan（锚点 %q 未命中）；新增/改写查询后请同步本守卫", scanAnchor)
	}

	for field := range fields {
		if strings.Contains(src, "&t."+field) {
			t.Errorf("pg.go 把可空列 %s 直扫进裸类型（&t.%s）；"+
				"写入侧 nullable(ticket.%s) 会产出 NULL，遇到 NULL 行整条查询失败", field, field, field)
		}
		// 左侧必须是独立的接收者 t（前一个字符不是标识符字符），
		// 否则 subtask 的 st.Description 会被当成工单的 t.Description 计入。
		fillRe := regexp.MustCompile(`(?:^|[^A-Za-z0-9_])t\.` + field + ` = nullableStr\(`)
		if got := len(fillRe.FindAllStringIndex(src, -1)); got != scans {
			t.Errorf("字段 %s 的 nullableStr 回填有 %d 处，工单 Scan 有 %d 处；"+
				"漏掉的那几处遇到该列为 NULL 的工单就会整条查询失败", field, got, scans)
		}
	}
}
