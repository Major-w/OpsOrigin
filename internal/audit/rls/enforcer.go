package rls

import (
	"fmt"
	"strings"
)

type Enforcer struct{}

func New() *Enforcer {
	return &Enforcer{}
}

func (e *Enforcer) Enforce(query string, tenantID string) (string, error) {
	if tenantID == "" {
		return "", fmt.Errorf("[ASSERT-AUDIT-02] tenant_id is required for query")
	}

	upper := strings.ToUpper(strings.TrimSpace(query))

	switch {
	case strings.HasPrefix(upper, "SELECT"):
		return e.enforceSelect(query, tenantID)
	case strings.HasPrefix(upper, "INSERT"):
		return e.enforceInsert(query, tenantID)
	case strings.HasPrefix(upper, "UPDATE"):
		return e.enforceUpdate(query, tenantID)
	case strings.HasPrefix(upper, "DELETE"):
		return e.enforceDelete(query, tenantID)
	}

	return query, nil
}

func (e *Enforcer) enforceSelect(query, tenantID string) (string, error) {
	upper := strings.ToUpper(query)

	if strings.Contains(upper, "WHERE") {
		if strings.Contains(upper, "TENANT_ID") || strings.Contains(upper, "tenant_id") {
			return query, nil
		}
		return query + fmt.Sprintf(" AND tenant_id = '%s'", tenantID), nil
	}

	lastSelect := strings.LastIndex(upper, "SELECT")
	fromIdx := strings.Index(upper[lastSelect:], "FROM")
	if fromIdx == -1 {
		return query, nil
	}

	insertPos := lastSelect + fromIdx + 4
	for insertPos < len(query) && query[insertPos] == ' ' {
		insertPos++
	}
	tableEnd := insertPos
	for tableEnd < len(query) && query[tableEnd] != ' ' && query[tableEnd] != '\n' {
		tableEnd++
	}

	return query[:tableEnd] + fmt.Sprintf(" WHERE tenant_id = '%s'", tenantID) + query[tableEnd:], nil
}

func (e *Enforcer) enforceInsert(query, tenantID string) (string, error) {
	upper := strings.ToUpper(query)
	valuesIdx := strings.Index(upper, "VALUES")
	if valuesIdx == -1 {
		return query, nil
	}

	parenOpen := strings.Index(upper[valuesIdx:], "(")
	if parenOpen == -1 {
		return query, nil
	}
	parenOpen += valuesIdx

	valuesClause := query[parenOpen:]
	if strings.Contains(upper[:valuesIdx], "TENANT_ID") || strings.Contains(query[:valuesIdx], "tenant_id") {
		return query, nil
	}

	return query[:parenOpen] + fmt.Sprintf("'%s', ", tenantID) + valuesClause[1:], nil
}

func (e *Enforcer) enforceUpdate(query, tenantID string) (string, error) {
	if !strings.Contains(query, "WHERE") {
		return "", fmt.Errorf("[ASSERT-AUDIT-02] UPDATE without WHERE clause is not allowed")
	}
	if strings.Contains(query, "tenant_id") || strings.Contains(query, "TENANT_ID") {
		return query, nil
	}
	return query + fmt.Sprintf(" AND tenant_id = '%s'", tenantID), nil
}

func (e *Enforcer) enforceDelete(query, tenantID string) (string, error) {
	if !strings.Contains(query, "WHERE") {
		return "", fmt.Errorf("[ASSERT-AUDIT-02] DELETE without WHERE clause is not allowed")
	}
	if strings.Contains(query, "tenant_id") || strings.Contains(query, "TENANT_ID") {
		return query, nil
	}
	return query + fmt.Sprintf(" AND tenant_id = '%s'", tenantID), nil
}
