// Package plugin 提供监控插件目录 —— 声明「某种技术栈该采什么指标、怎么连」。
//
// 设计沿用平台既有的注册表范式（cloud.Provider / discovery.Registry）：
// 插件是**声明式描述**，不含采集实现；真正的采集动作仍由监控采集任务与探针执行。
// 这样做的好处是插件目录可以独立演进（新增一个中间件只是加一条声明），
// 而调度与执行链路不必跟着改。
//
// 插件与监控目标的绑定通过 MonitorTarget.Labels["plugin"] = <key> 完成，
// 复用既有目标存储，不引入新表。
package plugin

import (
	"sort"
	"sync"
)

// Category 插件分类，用于前端目录分组。
type Category string

const (
	CategoryOS         Category = "os"
	CategoryDatabase   Category = "database"
	CategoryMiddleware Category = "middleware"
	CategoryWeb        Category = "web"
	CategoryContainer  Category = "container"
	CategoryNetwork    Category = "network"
	CategoryHardware   Category = "hardware"
)

// ConfigField 插件启用时需要用户填写的配置项描述（前端据此渲染表单）。
type ConfigField struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Type        string `json:"type"` // text | password | number | select
	Required    bool   `json:"required"`
	Default     string `json:"default,omitempty"`
	Placeholder string `json:"placeholder,omitempty"`
	Help        string `json:"help,omitempty"`
}

// Plugin 一个监控插件的完整声明。
type Plugin struct {
	Key         string        `json:"key"`
	Name        string        `json:"name"`
	Category    Category      `json:"category"`
	Description string        `json:"description"`
	DefaultPort int           `json:"default_port,omitempty"`
	Protocol    string        `json:"protocol"` // 采集侧协议：tcp / http / exporter / agent / snmp / ipmi
	Metrics     []string      `json:"metrics"`  // 代表性指标，便于用户判断是否够用
	Config      []ConfigField `json:"config,omitempty"`
	// Experimental 标记尚未在生产环境充分验证的插件，前端会给出提示。
	Experimental bool `json:"experimental,omitempty"`
}

// Registry 插件注册表：并发安全，装配期写、运行期只读。
type Registry struct {
	mu      sync.RWMutex
	plugins map[string]*Plugin
}

func NewRegistry() *Registry {
	return &Registry{plugins: make(map[string]*Plugin)}
}

// Register 注册插件；同 Key 覆盖，便于用自定义插件替换内置声明。
func (r *Registry) Register(p *Plugin) {
	if p == nil || p.Key == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.plugins[p.Key] = p
}

// Get 按 key 取插件。
func (r *Registry) Get(key string) (*Plugin, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.plugins[key]
	return p, ok
}

// List 返回全部插件，按分类再按名称稳定排序，保证前端展示顺序确定。
func (r *Registry) List() []*Plugin {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Plugin, 0, len(r.plugins))
	for _, p := range r.plugins {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Category != out[j].Category {
			return out[i].Category < out[j].Category
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// portField 大多数插件都要填地址与端口，抽出来避免逐个重复。
func portField(def string) ConfigField {
	return ConfigField{Key: "port", Label: "端口", Type: "number", Required: true, Default: def}
}

func addrField() ConfigField {
	return ConfigField{Key: "address", Label: "地址", Type: "text", Required: true, Placeholder: "10.0.0.1 或 db.internal"}
}

func userPassFields() []ConfigField {
	return []ConfigField{
		{Key: "username", Label: "用户名", Type: "text", Required: true, Help: "建议使用只读账号"},
		{Key: "password", Label: "密码", Type: "password", Required: true},
	}
}

// NewDefaultRegistry 构建内置插件目录。
// 新增插件只需在此追加一条声明，前端目录页自动出现，无需改任何渲染代码。
func NewDefaultRegistry() *Registry {
	r := NewRegistry()

	// —— 操作系统 / 主机 ——
	r.Register(&Plugin{
		Key: "host-basic", Name: "主机基础指标", Category: CategoryOS, Protocol: "agent",
		Description: "由主机 Agent 上报 CPU、内存、磁盘、网络、负载等基础指标，纳管主机时开启监控采集即自动生效。",
		Metrics:     []string{"cpu.usage", "mem.usage", "disk.usage", "disk.io", "net.in", "net.out", "load1", "load5"},
	})
	r.Register(&Plugin{
		Key: "host-process", Name: "进程与端口存活", Category: CategoryOS, Protocol: "agent",
		Description: "监控指定进程是否存活、端口是否监听，适合守护关键服务不被误杀。",
		Metrics:     []string{"process.alive", "process.cpu", "process.mem", "port.listen"},
		Config: []ConfigField{
			{Key: "process_name", Label: "进程名", Type: "text", Required: true, Placeholder: "nginx"},
			{Key: "ports", Label: "端口列表", Type: "text", Placeholder: "80,443（逗号分隔）"},
		},
	})

	// —— 数据库 ——
	r.Register(&Plugin{
		Key: "mysql", Name: "MySQL", Category: CategoryDatabase, Protocol: "tcp", DefaultPort: 3306,
		Description: "采集连接数、QPS、慢查询、主从延迟、InnoDB 缓冲池等核心指标。",
		Metrics:     []string{"mysql.threads_connected", "mysql.qps", "mysql.slow_queries", "mysql.repl_delay", "mysql.innodb_buffer_pool_hit"},
		Config:      append([]ConfigField{addrField(), portField("3306")}, userPassFields()...),
	})
	r.Register(&Plugin{
		Key: "postgresql", Name: "PostgreSQL", Category: CategoryDatabase, Protocol: "tcp", DefaultPort: 5432,
		Description: "采集会话数、事务提交回滚、缓存命中率、复制延迟与死锁计数。",
		Metrics:     []string{"pg.connections", "pg.xact_commit", "pg.xact_rollback", "pg.cache_hit_ratio", "pg.replication_lag", "pg.deadlocks"},
		Config:      append([]ConfigField{addrField(), portField("5432")}, userPassFields()...),
	})
	r.Register(&Plugin{
		Key: "redis", Name: "Redis", Category: CategoryDatabase, Protocol: "tcp", DefaultPort: 6379,
		Description: "采集内存占用、命中率、连接数、阻塞客户端与持久化状态。",
		Metrics:     []string{"redis.used_memory", "redis.hit_rate", "redis.connected_clients", "redis.blocked_clients", "redis.rdb_last_save"},
		Config: []ConfigField{addrField(), portField("6379"),
			{Key: "password", Label: "密码", Type: "password", Help: "未设置 requirepass 可留空"}},
	})
	r.Register(&Plugin{
		Key: "mongodb", Name: "MongoDB", Category: CategoryDatabase, Protocol: "tcp", DefaultPort: 27017,
		Description: "采集连接数、操作计数、副本集状态与锁等待。",
		Metrics:     []string{"mongo.connections", "mongo.opcounters", "mongo.replset_health", "mongo.global_lock"},
		Config:      append([]ConfigField{addrField(), portField("27017")}, userPassFields()...),
	})
	r.Register(&Plugin{
		Key: "oracle", Name: "Oracle", Category: CategoryDatabase, Protocol: "tcp", DefaultPort: 1521,
		Description: "采集表空间使用率、会话数、等待事件与归档日志状态。",
		Metrics:     []string{"oracle.tablespace_usage", "oracle.sessions", "oracle.wait_events", "oracle.archive_usage"},
		Config: append([]ConfigField{addrField(), portField("1521"),
			{Key: "service_name", Label: "服务名", Type: "text", Required: true, Placeholder: "ORCL"}}, userPassFields()...),
		Experimental: true,
	})

	// —— 中间件 ——
	r.Register(&Plugin{
		Key: "kafka", Name: "Kafka", Category: CategoryMiddleware, Protocol: "tcp", DefaultPort: 9092,
		Description: "采集 Topic 吞吐、分区状态与消费组积压（Lag），积压是消息链路最有价值的先行指标。",
		Metrics:     []string{"kafka.messages_in", "kafka.bytes_in", "kafka.under_replicated", "kafka.consumer_lag"},
		Config: []ConfigField{addrField(), portField("9092"),
			{Key: "consumer_groups", Label: "关注的消费组", Type: "text", Placeholder: "留空表示全部"}},
	})
	r.Register(&Plugin{
		Key: "rabbitmq", Name: "RabbitMQ", Category: CategoryMiddleware, Protocol: "http", DefaultPort: 15672,
		Description: "通过管理 API 采集队列深度、消费速率、连接与信道数。",
		Metrics:     []string{"rabbitmq.queue_depth", "rabbitmq.publish_rate", "rabbitmq.ack_rate", "rabbitmq.connections"},
		Config:      append([]ConfigField{addrField(), portField("15672")}, userPassFields()...),
	})
	r.Register(&Plugin{
		Key: "elasticsearch", Name: "Elasticsearch", Category: CategoryMiddleware, Protocol: "http", DefaultPort: 9200,
		Description: "采集集群健康度、分片分配、JVM 堆与索引/检索速率。",
		Metrics:     []string{"es.cluster_status", "es.unassigned_shards", "es.jvm_heap_used", "es.indexing_rate", "es.search_latency"},
		Config:      []ConfigField{addrField(), portField("9200")},
	})
	r.Register(&Plugin{
		Key: "zookeeper", Name: "ZooKeeper", Category: CategoryMiddleware, Protocol: "tcp", DefaultPort: 2181,
		Description: "采集节点角色、延迟分位、未完成请求数与 Watch 数量。",
		Metrics:     []string{"zk.server_state", "zk.avg_latency", "zk.outstanding_requests", "zk.watch_count"},
		Config:      []ConfigField{addrField(), portField("2181")},
	})
	r.Register(&Plugin{
		Key: "jvm", Name: "JVM 应用", Category: CategoryMiddleware, Protocol: "http", DefaultPort: 8080,
		Description: "对接 Actuator / JMX Exporter，采集堆内存、GC 次数与耗时、线程数。",
		Metrics:     []string{"jvm.heap_used", "jvm.gc_count", "jvm.gc_time", "jvm.threads", "jvm.class_loaded"},
		Config: []ConfigField{addrField(), portField("8080"),
			{Key: "metrics_path", Label: "指标路径", Type: "text", Default: "/actuator/prometheus"}},
	})

	// —— Web / 应用 ——
	r.Register(&Plugin{
		Key: "nginx", Name: "Nginx", Category: CategoryWeb, Protocol: "http", DefaultPort: 80,
		Description: "基于 stub_status 采集活跃连接、请求速率与各状态连接分布。",
		Metrics:     []string{"nginx.active_connections", "nginx.requests_rate", "nginx.reading", "nginx.writing", "nginx.waiting"},
		Config: []ConfigField{addrField(), portField("80"),
			{Key: "status_path", Label: "状态页路径", Type: "text", Default: "/nginx_status"}},
	})
	r.Register(&Plugin{
		Key: "http-endpoint", Name: "HTTP 接口拨测", Category: CategoryWeb, Protocol: "http", DefaultPort: 443,
		Description: "周期请求指定 URL，校验状态码、响应耗时与关键字，适合对外服务可用性兜底。",
		Metrics:     []string{"http.status_code", "http.response_time", "http.body_match", "http.ssl_days_left"},
		Config: []ConfigField{
			{Key: "url", Label: "URL", Type: "text", Required: true, Placeholder: "https://api.example.com/health"},
			{Key: "expect_code", Label: "期望状态码", Type: "number", Default: "200"},
			{Key: "expect_keyword", Label: "期望关键字", Type: "text", Placeholder: "可留空"},
		},
	})
	r.Register(&Plugin{
		Key: "prometheus-exporter", Name: "Prometheus Exporter", Category: CategoryWeb, Protocol: "exporter", DefaultPort: 9100,
		Description: "通用兜底插件：任何暴露 /metrics 的 Exporter 都能接入，指标按原名落库。",
		Metrics:     []string{"（由 Exporter 自身决定）"},
		Config: []ConfigField{addrField(), portField("9100"),
			{Key: "metrics_path", Label: "指标路径", Type: "text", Default: "/metrics"},
			{Key: "metric_prefix", Label: "指标前缀", Type: "text", Placeholder: "可留空，用于避免不同 Exporter 指标重名"}},
	})

	// —— 容器 / 编排 ——
	r.Register(&Plugin{
		Key: "kubernetes", Name: "Kubernetes", Category: CategoryContainer, Protocol: "http", DefaultPort: 6443,
		Description: "采集节点/Pod 状态、资源请求与限制、重启次数与调度失败事件。",
		Metrics:     []string{"k8s.node_ready", "k8s.pod_phase", "k8s.container_restarts", "k8s.cpu_request", "k8s.pending_pods"},
		Config: []ConfigField{addrField(), portField("6443"),
			{Key: "token", Label: "ServiceAccount Token", Type: "password", Required: true}},
	})
	r.Register(&Plugin{
		Key: "docker", Name: "Docker", Category: CategoryContainer, Protocol: "agent",
		Description: "由容器 Agent 采集容器 CPU、内存、网络与重启次数。",
		Metrics:     []string{"docker.container_count", "docker.cpu_usage", "docker.mem_usage", "docker.restart_count"},
	})

	// —— 网络 / 硬件 ——
	r.Register(&Plugin{
		Key: "snmp-network", Name: "网络设备 (SNMP)", Category: CategoryNetwork, Protocol: "snmp", DefaultPort: 161,
		Description: "通过 SNMP 采集交换机/路由器/防火墙的接口流量、丢包、CPU 与内存。",
		Metrics:     []string{"snmp.if_in_octets", "snmp.if_out_octets", "snmp.if_errors", "snmp.cpu_usage", "snmp.mem_usage"},
		Config: []ConfigField{addrField(), portField("161"),
			{Key: "version", Label: "SNMP 版本", Type: "select", Default: "v2c", Help: "v2c / v3"},
			{Key: "community", Label: "Community", Type: "password", Placeholder: "v2c 必填"}},
	})
	r.Register(&Plugin{
		Key: "ipmi", Name: "服务器带外 (IPMI)", Category: CategoryHardware, Protocol: "ipmi", DefaultPort: 623,
		Description:  "采集温度、风扇转速、电源状态与硬件告警，操作系统挂了也能看到。",
		Metrics:      []string{"ipmi.temperature", "ipmi.fan_speed", "ipmi.power_status", "ipmi.sel_events"},
		Config:       append([]ConfigField{addrField(), portField("623")}, userPassFields()...),
		Experimental: true,
	})

	return r
}
