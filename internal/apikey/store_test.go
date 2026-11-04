package apikey

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func newMockStore(t *testing.T) (*Store, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewStore(db), mock
}

func TestStore_Available(t *testing.T) {
	store, _ := newMockStore(t)
	if !store.Available() {
		t.Error("expected available with non-nil db")
	}
	nilStore := &Store{}
	if nilStore.Available() {
		t.Error("expected unavailable with nil db")
	}
}

// TestStore_GetByHash_NullTimes 回归验证：NULL 时间列必须扫描为 nil 指针，
// 否则会踩到项目此前的 NULL 裸扫坑（rows.Scan 报错导致整条查询失败）。
func TestStore_GetByHash_NullTimes(t *testing.T) {
	store, mock := newMockStore(t)
	now := time.Now()
	mock.ExpectQuery(`(?s)SELECT id, tenant_id, user_id, name, key_prefix, expires_at.*key_hash=\$1`).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "tenant_id", "user_id", "name", "key_prefix",
			"expires_at", "last_used_at", "use_count", "revoked_at", "created_at",
		}).AddRow("k1", "t1", "u1", "n1", "osk_abc", nil, nil, int64(0), nil, now))

	k, err := store.GetByHash(context.Background(), "osk_x")
	if err != nil {
		t.Fatalf("GetByHash: %v", err)
	}
	if k.ExpiresAt != nil || k.RevokedAt != nil || k.LastUsedAt != nil {
		t.Errorf("expected nil time pointers for NULL columns, got exp=%v rev=%v lu=%v",
			k.ExpiresAt, k.RevokedAt, k.LastUsedAt)
	}
	if k.UseCount != 0 {
		t.Errorf("use_count: got %d want 0", k.UseCount)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

func TestStore_GetByHash_WithTimes(t *testing.T) {
	store, mock := newMockStore(t)
	exp := time.Now().Add(24 * time.Hour)
	rev := time.Now().Add(-1 * time.Hour)
	mock.ExpectQuery(`(?s)SELECT id, tenant_id.*key_hash=\$1`).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "tenant_id", "user_id", "name", "key_prefix",
			"expires_at", "last_used_at", "use_count", "revoked_at", "created_at",
		}).AddRow("k1", "t1", "u1", "n1", "osk_abc", exp, nil, int64(3), rev, time.Now()))

	k, err := store.GetByHash(context.Background(), "osk_x")
	if err != nil {
		t.Fatalf("GetByHash: %v", err)
	}
	if k.ExpiresAt == nil || !k.ExpiresAt.Equal(exp) {
		t.Errorf("expires_at not parsed: %v", k.ExpiresAt)
	}
	if k.RevokedAt == nil || !k.RevokedAt.Equal(rev) {
		t.Errorf("revoked_at not parsed: %v", k.RevokedAt)
	}
	if k.UseCount != 3 {
		t.Errorf("use_count: got %d want 3", k.UseCount)
	}
}

func TestStore_GetByHash_NotFound(t *testing.T) {
	store, mock := newMockStore(t)
	mock.ExpectQuery(`(?s)SELECT id, tenant_id.*key_hash=\$1`).WillReturnError(sql.ErrNoRows)
	if _, err := store.GetByHash(context.Background(), "osk_missing"); err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestStore_Revoke(t *testing.T) {
	store, mock := newMockStore(t)

	// 命中 1 行 → true
	mock.ExpectExec(`UPDATE api_keys SET revoked_at`).WillReturnResult(sqlmock.NewResult(0, 1))
	ok, err := store.Revoke(context.Background(), "t1", "k1", "u1")
	if err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if !ok {
		t.Error("expected ok=true when a row was affected")
	}

	// 命中 0 行 → false
	mock.ExpectExec(`UPDATE api_keys SET revoked_at`).WillReturnResult(sqlmock.NewResult(0, 0))
	ok, err = store.Revoke(context.Background(), "t1", "k1", "u1")
	if err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if ok {
		t.Error("expected ok=false when no row affected")
	}
}

func TestStore_Create(t *testing.T) {
	store, mock := newMockStore(t)
	mock.ExpectExec(`INSERT INTO api_keys`).WillReturnResult(sqlmock.NewResult(1, 1))

	k := &Key{ID: "k1", TenantID: "t1", UserID: "u1", Name: "n1"}
	if err := store.Create(context.Background(), k, "osk_x"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Create 内应自动补 KeyPrefix（displayPrefix(raw)）
	if k.KeyPrefix != "osk_x" {
		t.Errorf("KeyPrefix auto-fill: got %q want %q", k.KeyPrefix, "osk_x")
	}
}

// 确保 ErrNotFound 与 sql.ErrNoRows 的语义关系（避免误判）。
func TestStore_ErrNotFoundIsNotNoRowsWrapper(t *testing.T) {
	if errors.Is(ErrNotFound, sql.ErrNoRows) {
		t.Error("ErrNotFound should be its own sentinel, not wrapping sql.ErrNoRows")
	}
}
