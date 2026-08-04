// Package all 是一个聚合包，通过空白导入触发各云 Provider 的 init() 注册，
// 使 cloud.Names() / cloud.Get() 在运行时可用。
//
// 这是「云资源接出」的 #1 前置 blocker：四个 provider 包都仅在其自身 init()
// 中调用 cloud.Register，但全仓库此前没有任何包 import 它们，导致运行期
// cloud.Names() 为空。本包被 cmd/core/main.go 空白导入后，注册即生效。
package all

import (
	_ "github.com/opsorigin/core/internal/cloud/providers/aliyun"
	_ "github.com/opsorigin/core/internal/cloud/providers/aws"
	_ "github.com/opsorigin/core/internal/cloud/providers/huawei"
	_ "github.com/opsorigin/core/internal/cloud/providers/openstack"
	_ "github.com/opsorigin/core/internal/cloud/providers/proxmox"
	_ "github.com/opsorigin/core/internal/cloud/providers/sangfor"
	_ "github.com/opsorigin/core/internal/cloud/providers/tencent"
	_ "github.com/opsorigin/core/internal/cloud/providers/vmware"
	_ "github.com/opsorigin/core/internal/cloud/providers/zstack"
)
