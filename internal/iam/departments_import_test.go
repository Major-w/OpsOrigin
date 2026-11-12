package iam

// 部门批量导入测试：路径式 CSV 逐级建档、幂等复用、父级冲突报错。

import (
	"bytes"
	"context"
	"encoding/csv"
	"mime/multipart"
	"net/http/httptest"
	"strings"
	"testing"
)

// buildMultipartCSV 构造 multipart CSV 上传请求体，返回 (body, contentType)。
func buildMultipartCSV(t *testing.T, csvContent string) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "departments.csv")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write([]byte(csvContent)); err != nil {
		t.Fatal(err)
	}
	mw.Close()
	return &body, mw.FormDataContentType()
}

func postImportCSV(t *testing.T, s *Service, csvContent string) *httptest.ResponseRecorder {
	t.Helper()
	body, ctype := buildMultipartCSV(t, csvContent)
	req := httptest.NewRequest("POST", "/api/v1/iam/departments/import", body)
	req.Header.Set("Content-Type", ctype)
	rec := httptest.NewRecorder()
	s.handleImportDepartments(rec, req)
	return rec
}

func TestImportDepartmentsPaths(t *testing.T) {
	s := NewService(nil)

	rows := [][]string{
		{"path", "description"},
		{"总部/华东/门店A", "华东门店"},
		{"总部/华东/门店B", ""},
		{"总部/华南", ""},
	}
	content := new(bytes.Buffer)
	if err := csv.NewWriter(content).WriteAll(rows); err != nil {
		t.Fatal(err)
	}
	rec := postImportCSV(t, s, content.String())

	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	// created=5(总部/华东/门店A/门店B/华南)，reused=3(同 CSV 内前行已建的上级复用)
	if !strings.Contains(rec.Body.String(), `"created":5`) || !strings.Contains(rec.Body.String(), `"reused":3`) || !strings.Contains(rec.Body.String(), `"failed":0`) {
		t.Fatalf("应创建 5 个节点复用 3 次上级，实际 %s", rec.Body.String())
	}

	// 树形结构核对
	depts, err := s.store.ListDepartments(context.Background(), "default")
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]*Department{}
	for _, d := range depts {
		byName[d.Name] = d
	}
	hq := byName["总部"]
	if hq == nil || hq.ParentID != "" {
		t.Fatalf("总部应为根节点")
	}
	hd := byName["华东"]
	if hd == nil || hd.ParentID != hq.ID {
		t.Fatalf("华东应挂在总部下")
	}
	if byName["门店A"] == nil || byName["门店A"].ParentID != hd.ID {
		t.Fatalf("门店A 应挂在华东下")
	}
	if byName["门店A"].Description != "华东门店" {
		t.Fatalf("末级描述应写入，实际 %q", byName["门店A"].Description)
	}

	// 幂等重跑：全部复用（3 行路径共 8 个节点级）
	rec2 := postImportCSV(t, s, content.String())
	if !strings.Contains(rec2.Body.String(), `"created":0`) || !strings.Contains(rec2.Body.String(), `"reused":8`) {
		t.Fatalf("重跑应全部复用，实际 %s", rec2.Body.String())
	}
}

func TestImportDepartmentsParentConflict(t *testing.T) {
	s := NewService(nil)
	content := new(bytes.Buffer)
	if err := csv.NewWriter(content).WriteAll([][]string{
		{"path"},
		{"总部/华东"},
	}); err != nil {
		t.Fatal(err)
	}
	rec := postImportCSV(t, s, content.String())
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}

	// 同名部门但父级冲突：门店A 已挂在华东下，再以 总部/门店A 导入应报错
	conflict := new(bytes.Buffer)
	if err := csv.NewWriter(conflict).WriteAll([][]string{
		{"path"},
		{"华东/门店A"},    // 幂等复用
		{"总部/华东/门店A"}, // 复用
		{"总部/门店A"},    // 门店A 已存在但父为华东 → 冲突
	}); err != nil {
		t.Fatal(err)
	}
	// 行2「华东/门店A」：华东已存在但父级不同 → 冲突
	// 行4「总部/门店A」：门店A 已存在但父级不同 → 冲突
	rec2 := postImportCSV(t, s, conflict.String())
	if !strings.Contains(rec2.Body.String(), `"failed":2`) || !strings.Contains(rec2.Body.String(), "上级不一致") {
		t.Fatalf("父级冲突行应报错，实际 %s", rec2.Body.String())
	}
}
