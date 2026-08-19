package all_test

import (
	"testing"

	"github.com/opsorigin/core/internal/cloud"
	// 空白导入触发四个 Provider 的 init() 注册，验证 #1 blocker 已解除。
	_ "github.com/opsorigin/core/internal/cloud/providers/all"
)

// TestProvidersRegisteredViaAll 验证 providers/all 空白导入后，
// cloud.Names() 包含 aws / tencent / aliyun / huawei。
func TestProvidersRegisteredViaAll(t *testing.T) {
	names := cloud.Names()

	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}

	want := []string{"aws", "tencent", "aliyun", "huawei"}
	for _, name := range want {
		if !set[name] {
			t.Errorf("expected provider %q to be registered after importing providers/all; got names=%v", name, names)
		}
	}
}

// TestPrivateCloudProvidersRegistered 验证私有云 Provider（OpenStack/VMware/深信服超融合/ZStack）
// 经 providers/all 空白导入后均已注册，可作为「私有云资源接入类型」出现在云资源纳管界面。
func TestPrivateCloudProvidersRegistered(t *testing.T) {
	names := cloud.Names()
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}

	want := []string{"openstack", "vmware", "sangfor", "zstack", "proxmox"}
	for _, name := range want {
		if !set[name] {
			t.Errorf("expected private-cloud provider %q to be registered; got names=%v", name, names)
		}
	}
}

// TestProviderSupportedVersions 验证每个已注册 Provider 都声明了适配版本，
// 且纳管界面透出的「已验证/待测」状态符合预期（VMware 已验证，深信服/ZStack 待测）。
func TestProviderSupportedVersions(t *testing.T) {
	cases := map[string]cloud.VersionStatus{
		"vmware":    cloud.VersionVerified,
		"sangfor":   cloud.VersionUntested,
		"zstack":    cloud.VersionUntested,
		"proxmox":   cloud.VersionVerified,
		"openstack": cloud.VersionVerified,
		"aliyun":    cloud.VersionVerified,
		"tencent":   cloud.VersionVerified,
		"huawei":    cloud.VersionVerified,
		"aws":       cloud.VersionVerified,
		"volcano":   cloud.VersionUntested,
		"azure":     cloud.VersionUntested,
	}
	for name, wantStatus := range cases {
		p, err := cloud.Get(name)
		if err != nil {
			t.Fatalf("provider %q not registered: %v", name, err)
		}
		vs := p.SupportedVersions()
		if len(vs) == 0 {
			t.Errorf("provider %q declared no supported versions", name)
			continue
		}
		has := false
		for _, v := range vs {
			if v.Status == wantStatus {
				has = true
				break
			}
		}
		if !has {
			t.Errorf("provider %q has no version with status %q; got=%v", name, wantStatus, vs)
		}
	}
}
