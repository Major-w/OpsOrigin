package store

import (
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/opsorigin/core/internal/itsm/model"
)

// 本文件是 memStore/PGStore 字段覆盖不一致事故的防漏守卫：
// MemoryStore 保存整个 struct（字段天然全覆盖），PGStore 靠手写列清单 ——
// 曾因「加字段忘改 SQL」导致 hold_at/hold_seconds/approval_chain 等在 PG
// 永不落库（挂起补偿、SLA 标记、多级审批链全部静默失效）。
// 规则：model.Ticket 的每个字段都必须以 snake_case 列名出现在
// INSERT、UPDATE 及全部四个 SELECT（Get/List/ListActive/ListOnHold）中；
// 唯一例外是 created_at（创建时间不可变，UPDATE 不写）。

var snakeRe1 = regexp.MustCompile(`([A-Z]+)([A-Z][a-z])`)
var snakeRe2 = regexp.MustCompile(`([a-z0-9])([A-Z])`)

// 缩写+复数( IDs) 的字段无法用通用驼峰规则转换，手工对齐列名。
var columnOverrides = map[string]string{
	"CIIDs":            "ci_ids",
	"RelatedTicketIDs": "related_ticket_ids",
}

func ticketColumnNames(t *testing.T) []string {
	t.Helper()
	typ := reflect.TypeOf(model.Ticket{})
	cols := make([]string, 0, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		if c, ok := columnOverrides[name]; ok {
			cols = append(cols, c)
			continue
		}
		c := snakeRe1.ReplaceAllString(name, "${1}_${2}")
		c = snakeRe2.ReplaceAllString(c, "${1}_${2}")
		cols = append(cols, strings.ToLower(c))
	}
	return cols
}

func readPGSource(t *testing.T) string {
	t.Helper()
	src, err := os.ReadFile("pg.go")
	if err != nil {
		t.Fatalf("read pg.go: %v", err)
	}
	return string(src)
}

// tokensOf 提取 SQL 片段中的全部标识符，避免 "id" 误命中 "tenant_id" 的子串。
func tokensOf(block string) map[string]bool {
	set := map[string]bool{}
	for _, tok := range regexp.MustCompile(`[a-z_][a-z0-9_]*`).FindAllString(block, -1) {
		set[tok] = true
	}
	return set
}

func mustContain(t *testing.T, label string, block string, cols []string, skip map[string]bool) {
	t.Helper()
	set := tokensOf(block)
	for _, c := range cols {
		if skip[c] {
			continue
		}
		if !set[c] {
			t.Errorf("%s 缺少列 %q（model.Ticket 加了字段但 pg.go 的 SQL 没同步）", label, c)
		}
	}
}

func TestPGTicketColumnsMatchModel(t *testing.T) {
	src := readPGSource(t)
	cols := ticketColumnNames(t)

	// 1) INSERT：全字段必写。
	m := regexp.MustCompile(`INSERT INTO itsm_tickets(?s:.*?)VALUES\s*\(`).FindStringSubmatch(src)
	if m == nil {
		t.Fatal("未找到 INSERT INTO itsm_tickets 语句")
	}
	mustContain(t, "INSERT", m[0], cols, nil)

	// 2) UPDATE：除 created_at 外全字段必写（tenant_id 走 WHERE 条件，块截取止于 WHERE）。
	m = regexp.MustCompile(`UPDATE itsm_tickets SET(?s:.*?)WHERE id=`).FindStringSubmatch(src)
	if m == nil {
		t.Fatal("未找到 UPDATE itsm_tickets 语句")
	}
	mustContain(t, "UPDATE", m[0], cols, map[string]bool{"created_at": true, "tenant_id": true})

	// 3) 四个 SELECT（Get/List/ListActive/ListOnHold）：全字段必读。
	selRe := regexp.MustCompile(`SELECT id, tenant_id, title(?s:.*?)FROM itsm_tickets`)
	blocks := selRe.FindAllString(src, -1)
	if len(blocks) != 4 {
		t.Fatalf("期望 4 个工单 SELECT（Get/List/ListActive/ListOnHold），实际 %d 个", len(blocks))
	}
	labels := []string{"SELECT#1(Get)", "SELECT#2(List)", "SELECT#3(ListActive)", "SELECT#4(ListOnHold)"}
	for i, b := range blocks {
		mustContain(t, labels[i], b, cols, nil)
	}
}
