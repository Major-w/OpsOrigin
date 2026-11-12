package ldapsync

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/go-ldap/ldap/v3"
)

// ---- fakes ----

// fakeDir 模拟目录搜索：按过滤器区分用户/组两次搜索结果。
type fakeDir struct {
	userEntries []*ldap.Entry
	groups      []*ldap.Entry
	dnResolved  map[string]string
	userAttr    string
	userFilter  string
	groupFilter string
	groupBaseDN string
	groupUsrAtt string
}

func (f *fakeDir) SearchEntries(base, filter string, attrs []string) ([]*ldap.Entry, error) {
	if filter == f.userFilter {
		return f.userEntries, nil
	}
	if filter == f.groupFilter {
		return f.groups, nil
	}
	return nil, fmt.Errorf("unexpected filter %q", filter)
}

func (f *fakeDir) ResolveDNAttrs(dns []string, attr string) map[string]string {
	out := map[string]string{}
	for _, dn := range dns {
		if v, ok := f.dnResolved[dn]; ok {
			out[dn] = v
		}
	}
	return out
}

func (f *fakeDir) UserAttrName() string {
	if f.userAttr != "" {
		return f.userAttr
	}
	return "uid"
}

func (f *fakeDir) DepartmentAttrName() string { return "department" }

func (f *fakeDir) SyncUserFilter() string { return f.userFilter }
func (f *fakeDir) GroupFilter() string    { return f.groupFilter }
func (f *fakeDir) GroupBaseDN() string {
	if f.groupBaseDN != "" {
		return f.groupBaseDN
	}
	return "dc=x"
}
func (f *fakeDir) GroupUserAttr() string {
	if f.groupUsrAtt != "" {
		return f.groupUsrAtt
	}
	return "member"
}

func ldapEntry(dn string, attrs map[string][]string) *ldap.Entry {
	return ldap.NewEntry(dn, attrs)
}

type fakeUsers struct {
	users    map[string]string // username → email（EnsureUser 落库结果）
	depts    map[string]string // username → department
	created  []string
	localIDs map[string]string // ListUsernameIDMap 返回
}

func (f *fakeUsers) EnsureUser(_ context.Context, _, username, email, department string) (string, bool, error) {
	if _, ok := f.users[username]; !ok {
		f.created = append(f.created, username)
	}
	f.users[username] = email
	if f.depts == nil {
		f.depts = map[string]string{}
	}
	f.depts[username] = department
	return "id-" + username, true, nil
}

func (f *fakeUsers) ListUsernameIDMap(_ context.Context, _ string) (map[string]string, error) {
	return f.localIDs, nil
}

type fakeGroups struct {
	groups map[string][]string // name → memberIDs
}

func (f *fakeGroups) EnsureGroup(_ context.Context, _, name, _ string, memberIDs []string) error {
	f.groups[name] = memberIDs
	return nil
}

// ---- tests ----

func TestSyncUsersOnly(t *testing.T) {
	dir := &fakeDir{
		userFilter: "(objectClass=person)",
		userEntries: []*ldap.Entry{
			ldapEntry("uid=alice,ou=people", map[string][]string{"uid": {"alice"}, "mail": {"alice@a.com"}, "department": {"门店A"}}),
			ldapEntry("uid=bob,ou=people", map[string][]string{"uid": {"bob"}}),                              // 无 mail/department
			ldapEntry("uid=alice,ou=external", map[string][]string{"uid": {"alice"}, "mail": {"dup@a.com"}}), // 重名取首个
			ldapEntry("uid=,ou=blank", map[string][]string{"uid": {""}}),                                     // 无用户名跳过
		},
	}
	fu := &fakeUsers{users: map[string]string{}}
	s := &Syncer{LDAP: dir, Users: fu}

	res, err := s.Sync(context.Background(), "default")
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if res.UsersTotal != 2 {
		t.Fatalf("users_total 应为 2（alice/bob 去重后），实际 %d", res.UsersTotal)
	}
	if res.UsersCreated != 2 {
		t.Fatalf("users_created 应为 2，实际 %d", res.UsersCreated)
	}
	if fu.users["alice"] != "alice@a.com" {
		t.Fatalf("alice 邮箱应取首个条目，实际 %q", fu.users["alice"])
	}
	if fu.depts["alice"] != "门店A" || fu.depts["bob"] != "" {
		t.Fatalf("部门同步不符：alice=%q bob=%q", fu.depts["alice"], fu.depts["bob"])
	}
	if _, ok := fu.users["bob"]; !ok {
		t.Fatal("bob 应被建档")
	}
	if res.GroupsTotal != 0 {
		t.Fatalf("未配置组过滤时 groups_total 应为 0，实际 %d", res.GroupsTotal)
	}
}

func TestSyncNoUserFilter(t *testing.T) {
	s := &Syncer{LDAP: &fakeDir{}, Users: &fakeUsers{users: map[string]string{}}}
	if _, err := s.Sync(context.Background(), "default"); err == nil {
		t.Fatal("未配置用户过滤器应报错")
	}
}

func TestSyncGroupsMemberUid(t *testing.T) {
	dir := &fakeDir{
		userFilter:  "(objectClass=person)",
		userEntries: []*ldap.Entry{ldapEntry("uid=alice", map[string][]string{"uid": {"alice"}})},
		groupFilter: "(objectClass=groupOfNames)",
		groupUsrAtt: "memberUid",
		groups: []*ldap.Entry{
			ldapEntry("cn=运维组", map[string][]string{"cn": {"运维组"}, "memberUid": {"alice", "ghost"}}), // ghost 本地不存在
		},
	}
	fu := &fakeUsers{users: map[string]string{}, localIDs: map[string]string{"alice": "id-alice"}}
	fg := &fakeGroups{groups: map[string][]string{}}
	s := &Syncer{LDAP: dir, Users: fu, Groups: fg}

	res, err := s.Sync(context.Background(), "default")
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	got := fg.groups["运维组"]
	if len(got) != 1 || got[0] != "id-alice" {
		t.Fatalf("组成员应只含本地存在的 alice，实际 %v", got)
	}
	if res.GroupsSynced != 1 || res.GroupsTotal != 1 {
		t.Fatalf("groups synced/total 应为 1/1，实际 %d/%d", res.GroupsSynced, res.GroupsTotal)
	}
	// ghost 应记 warning
	found := false
	for _, w := range res.Warnings {
		if strings.Contains(w, "ghost") {
			found = true
		}
	}
	if !found {
		t.Fatalf("本地不存在的 ghost 应记 warning，实际 %v", res.Warnings)
	}
}

func TestSyncGroupsDNMode(t *testing.T) {
	dnAlice := "cn=alice,ou=people"
	dir := &fakeDir{
		userFilter:  "(objectClass=person)",
		userEntries: []*ldap.Entry{},
		groupFilter: "(objectClass=group)",
		groupUsrAtt: "member", // DN 模式
		groups: []*ldap.Entry{
			ldapEntry("cn=IT组,ou=groups", map[string][]string{"cn": {"IT组"}, "member": {dnAlice, "cn=missing,ou=people"}}),
		},
		dnResolved: map[string]string{dnAlice: "alice"},
	}
	fu := &fakeUsers{users: map[string]string{}, localIDs: map[string]string{"alice": "id-alice"}}
	fg := &fakeGroups{groups: map[string][]string{}}
	s := &Syncer{LDAP: dir, Users: fu, Groups: fg}

	if _, err := s.Sync(context.Background(), "default"); err != nil {
		t.Fatalf("sync: %v", err)
	}
	got := fg.groups["IT组"]
	if len(got) != 1 || got[0] != "id-alice" {
		t.Fatalf("DN 模式成员应反查为 alice，实际 %v", got)
	}
}

func TestSyncEnsureUserCalledPerUser(t *testing.T) {
	dir := &fakeDir{
		userFilter: "(objectClass=person)",
		userEntries: []*ldap.Entry{
			ldapEntry("uid=x1", map[string][]string{"uid": {"x1"}}),
			ldapEntry("uid=x2", map[string][]string{"uid": {"x2"}}),
			ldapEntry("uid=x3", map[string][]string{"uid": {"x3"}}),
		},
	}
	fu := &fakeUsers{users: map[string]string{}}
	s := &Syncer{LDAP: dir, Users: fu}
	if _, err := s.Sync(context.Background(), "default"); err != nil {
		t.Fatalf("sync: %v", err)
	}
	sort.Strings(fu.created)
	if len(fu.created) != 3 {
		t.Fatalf("应逐用户 EnsureUser 3 次，实际 %v", fu.created)
	}
}

type fakeDepts struct {
	depts   map[string]string // name → source
	created []string
}

func (f *fakeDepts) EnsureDepartment(_ context.Context, _, name, source string) (string, bool, error) {
	if _, ok := f.depts[name]; !ok {
		f.created = append(f.created, name)
	}
	f.depts[name] = source
	return "dept-" + name, true, nil
}

func TestSyncDepartmentsMirror(t *testing.T) {
	dir := &fakeDir{
		userFilter: "(objectClass=person)",
		userEntries: []*ldap.Entry{
			ldapEntry("uid=alice", map[string][]string{"uid": {"alice"}, "department": {"门店A"}}),
			ldapEntry("uid=bob", map[string][]string{"uid": {"bob"}, "department": {"门店B"}}),
			ldapEntry("uid=carol", map[string][]string{"uid": {"carol"}, "department": {"门店A"}}), // 重值去重
			ldapEntry("uid=dave", map[string][]string{"uid": {"dave"}}),                          // 无部门
		},
	}
	fu := &fakeUsers{users: map[string]string{}}
	fd := &fakeDepts{depts: map[string]string{}}
	s := &Syncer{LDAP: dir, Users: fu, Depts: fd}

	res, err := s.Sync(context.Background(), "default")
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if len(fd.depts) != 2 {
		t.Fatalf("应按去重后的部门值建档 2 个，实际 %v", fd.depts)
	}
	if fd.depts["门店A"] != "ldap" || fd.depts["门店B"] != "ldap" {
		t.Fatalf("部门 source 应为 ldap，实际 %v", fd.depts)
	}
	for _, w := range res.Warnings {
		if strings.Contains(w, "department") {
			t.Fatalf("已接入 Depts 不应有 department warning: %v", res.Warnings)
		}
	}
}

func TestSyncDepartmentsNotWiredWarns(t *testing.T) {
	dir := &fakeDir{
		userFilter:  "(objectClass=person)",
		userEntries: []*ldap.Entry{ldapEntry("uid=alice", map[string][]string{"uid": {"alice"}, "department": {"门店A"}})},
	}
	s := &Syncer{LDAP: dir, Users: &fakeUsers{users: map[string]string{}}}
	res, err := s.Sync(context.Background(), "default")
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	found := false
	for _, w := range res.Warnings {
		if strings.Contains(w, "department writer not wired") {
			found = true
		}
	}
	if !found {
		t.Fatalf("未接入 Depts 应记 warning，实际 %v", res.Warnings)
	}
}
