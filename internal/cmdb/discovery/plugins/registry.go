package plugins

import "github.com/opsorigin/core/internal/cmdb/discovery"

// NewRegistry 构建并注册全部内置发现插件，供 cmd/core 启动时注入 cmdb.Service。
func NewRegistry() *discovery.Registry {
	r := discovery.NewRegistry()
	r.Register(&ServerDiscoverer{})     // server / host：物理服务器、裸金属（探针 Agent 本地采集）
	r.Register(&NetworkDiscoverer{})    // snmp / network：交换机/路由器/防火墙（SNMP walk）
	r.Register(&DatabaseDiscoverer{})   // database：MySQL / PostgreSQL / Oracle 探测
	r.Register(&MiddlewareDiscoverer{}) // middleware：Redis/Nginx/ES/Kafka/ZK/RabbitMQ 探测
	r.Register(&AppDiscoverer{})        // application / app：端口 + HTTP 指纹探测
	return r
}
