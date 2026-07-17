package cmdb

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleListCIWithoutRepositoryReturnsEmptyList(t *testing.T) {
	svc := &Service{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/cmdb/ci", nil)
	rr := httptest.NewRecorder()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("handleListCI panicked: %v", r)
		}
	}()

	svc.handleListCI(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}
	if body := rr.Body.String(); !strings.Contains(body, "[]") {
		t.Fatalf("expected empty array response, got %s", body)
	}
}
