package apikey

import (
	"context"
	"testing"
	"time"
)

// fakeKeyStore 实现 KeyStore，仅按明文 raw 返回预设 Key，不接 PG。
// 用于精准验证 ResolveAPIKey 的鉴权分支（fail-closed / 继承权限 / 降级）。
type fakeKeyStore struct {
	available bool
	byHash    map[string]*Key
	touched   [][]string
}

func (f *fakeKeyStore) Available() bool                        { return f.available }
func (f *fakeKeyStore) EnsureSchema(ctx context.Context) error { return nil }
func (f *fakeKeyStore) GetByHash(ctx context.Context, raw string) (*Key, error) {
	if k, ok := f.byHash[raw]; ok {
		return k, nil
	}
	return nil, ErrNotFound
}
func (f *fakeKeyStore) GetByID(ctx context.Context, _, _, _ string) (*Key, error) {
	return nil, ErrNotFound
}
func (f *fakeKeyStore) ListByUser(ctx context.Context, _, _ string, _ int) ([]*Key, error) {
	return nil, nil
}
func (f *fakeKeyStore) ListByTenant(ctx context.Context, _ string, _ int) ([]*Key, error) {
	return nil, nil
}
func (f *fakeKeyStore) Revoke(ctx context.Context, _, _, _ string) (bool, error) {
	return false, nil
}
func (f *fakeKeyStore) TouchUsage(ctx context.Context, ids []string) error {
	f.touched = append(f.touched, ids)
	return nil
}
func (f *fakeKeyStore) Create(ctx context.Context, _ *Key, _ string) error { return nil }

func TestResolveAPIKey(t *testing.T) {
	now := time.Now()
	active := &Key{ID: "k1", TenantID: "t1", UserID: "u1"}
	revoked := &Key{ID: "k2", TenantID: "t1", UserID: "u1", RevokedAt: &now}
	expired := &Key{ID: "k3", TenantID: "t1", UserID: "u1", ExpiresAt: &now}
	emptyTenant := &Key{ID: "k4", TenantID: "", UserID: "u1"}

	rawActive := KeyPrefix + "active"
	rawRevoked := KeyPrefix + "revoked"
	rawExpired := KeyPrefix + "expired"
	rawEmptyTenant := KeyPrefix + "emptyt"

	store := &fakeKeyStore{
		available: true,
		byHash: map[string]*Key{
			rawActive:      active,
			rawRevoked:     revoked,
			rawExpired:     expired,
			rawEmptyTenant: emptyTenant,
		},
	}

	adminResolver := func(ctx context.Context, _, _ string) (string, string, bool) {
		return "admin", "active", true
	}
	disabledResolver := func(ctx context.Context, _, _ string) (string, string, bool) {
		return "admin", "disabled", true
	}
	emptyRoleResolver := func(ctx context.Context, _, _ string) (string, string, bool) {
		return "", "active", true
	}

	cases := []struct {
		name       string
		raw        string
		resolver   userRefResolver
		wantOK     bool
		wantRole   string
		wantTenant string
	}{
		{"wrong prefix", "xxx_plain", adminResolver, false, "", ""},
		{"key not found", KeyPrefix + "missing", adminResolver, false, "", ""},
		{"revoked key", rawRevoked, adminResolver, false, "", ""},
		{"expired key", rawExpired, adminResolver, false, "", ""},
		{"nil resolver must fail-closed", rawActive, nil, false, "", ""},
		{"disabled user", rawActive, disabledResolver, false, "", ""},
		{"empty role degrades to viewer", rawActive, emptyRoleResolver, true, "viewer", "t1"},
		{"normal admin", rawActive, adminResolver, true, "admin", "t1"},
		{"empty tenant defaults to default", rawEmptyTenant, adminResolver, true, "admin", "default"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := NewService(store)
			svc.SetIdentityResolver(tc.resolver)
			id, ok := svc.ResolveAPIKey(context.Background(), tc.raw)
			if ok != tc.wantOK {
				t.Fatalf("ok=%v want %v", ok, tc.wantOK)
			}
			if !tc.wantOK {
				return
			}
			if id.Role != tc.wantRole {
				t.Errorf("role=%q want %q", id.Role, tc.wantRole)
			}
			if id.TenantID != tc.wantTenant {
				t.Errorf("tenant=%q want %q", id.TenantID, tc.wantTenant)
			}
			if id.UserID != "u1" {
				t.Errorf("user=%q want u1", id.UserID)
			}
		})
	}
}

func TestResolveAPIKey_StoreUnavailable(t *testing.T) {
	svc := NewService(&fakeKeyStore{available: false})
	svc.SetIdentityResolver(func(ctx context.Context, _, _ string) (string, string, bool) {
		return "admin", "active", true
	})
	if _, ok := svc.ResolveAPIKey(context.Background(), KeyPrefix+"x"); ok {
		t.Error("expected false when store unavailable")
	}
}
