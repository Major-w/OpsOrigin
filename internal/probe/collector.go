package probe

import (
	"context"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// derefInt32 安全解引用 *int32。
func derefInt32(p *int32) int32 {
	if p == nil {
		return 0
	}
	return *p
}

// Collector 使用 client-go 采集集群对象。
type Collector struct {
	cs *kubernetes.Clientset
}

// NewCollector 构造采集器：kubeconfig 为空时走 in-cluster 配置
// （DaemonSet 默认形态），否则用给定 kubeconfig（集群外运行）。
func NewCollector(kubeconfig string) (*Collector, error) {
	var cfg *rest.Config
	var err error
	if kubeconfig == "" {
		cfg, err = rest.InClusterConfig()
	} else {
		cfg, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	}
	if err != nil {
		return nil, err
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &Collector{cs: cs}, nil
}

// Collect 执行一次全量采集。
func (c *Collector) Collect(ctx context.Context) (*K8sReport, error) {
	rep := &K8sReport{CollectedAt: time.Now()}

	// 1) 节点
	nodes, err := c.cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	var readyNodes int
	for i := range nodes.Items {
		n := &nodes.Items[i]
		ready := false
		for _, cond := range n.Status.Conditions {
			if cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionTrue {
				ready = true
			}
		}
		if ready {
			readyNodes++
		}
		rep.Nodes = append(rep.Nodes, K8sNode{
			Name:       n.Name,
			Ready:      ready,
			Roles:      nodeRoles(n),
			Kubelet:    n.Status.NodeInfo.KubeletVersion,
			OSImage:   n.Status.NodeInfo.OSImage,
			CPUCapacity:  q(n.Status.Capacity.Cpu().String()),
			MemCapacity: q(n.Status.Capacity.Memory().String()),
		})
	}
	rep.Metrics = append(rep.Metrics, K8sMetric{
		Name: "node_ready", Value: float64(readyNodes), Unit: "count",
		Labels: map[string]string{"total": itoa(len(nodes.Items))}, Timestamp: time.Now(),
	})

	// 2) Pod
	pods, err := c.cs.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	var podRestarts int32
	var readyPods int
	for i := range pods.Items {
		p := &pods.Items[i]
		restarts := int32(0)
		ready := false
		for _, cs := range p.Status.ContainerStatuses {
			restarts += cs.RestartCount
			if cs.Ready {
				ready = true
			}
		}
		podRestarts += restarts
		if ready {
			readyPods++
		}
		rep.Pods = append(rep.Pods, K8sPod{
			Name:      p.Name,
			Namespace: p.Namespace,
			Node:      p.Spec.NodeName,
			Phase:     string(p.Status.Phase),
			Restarts:  restarts,
			Ready:     ready,
		})
	}
	rep.Metrics = append(rep.Metrics, K8sMetric{
		Name: "pod_restarts_total", Value: float64(podRestarts), Unit: "count", Timestamp: time.Now(),
	})

	// 3) 工作负载（Deployment / StatefulSet / DaemonSet）
	deps, err := c.cs.AppsV1().Deployments("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	var notReadyDeps int
	for i := range deps.Items {
		d := &deps.Items[i]
			rep.Workloads = append(rep.Workloads, K8sWorkload{
				Kind: "Deployment", Name: d.Name, Namespace: d.Namespace,
				Replicas: derefInt32(d.Spec.Replicas), Ready: d.Status.ReadyReplicas, Available: d.Status.AvailableReplicas,
			})
		if d.Status.ReadyReplicas < derefInt32(d.Spec.Replicas) {
			notReadyDeps++
		}
	}
	sts, err := c.cs.AppsV1().StatefulSets("").List(ctx, metav1.ListOptions{})
	if err == nil {
		for i := range sts.Items {
			s := &sts.Items[i]
				rep.Workloads = append(rep.Workloads, K8sWorkload{
					Kind: "StatefulSet", Name: s.Name, Namespace: s.Namespace,
					Replicas: derefInt32(s.Spec.Replicas), Ready: s.Status.ReadyReplicas, Available: s.Status.CurrentReplicas,
				})
		}
	}
	ds, err := c.cs.AppsV1().DaemonSets("").List(ctx, metav1.ListOptions{})
	if err == nil {
		for i := range ds.Items {
			d := &ds.Items[i]
			rep.Workloads = append(rep.Workloads, K8sWorkload{
				Kind: "DaemonSet", Name: d.Name, Namespace: d.Namespace,
				Replicas: d.Status.DesiredNumberScheduled, Ready: d.Status.NumberReady, Available: d.Status.NumberAvailable,
			})
		}
	}

	// 4) 事件（Warning 为主）
	events, err := c.cs.CoreV1().Events("").List(ctx, metav1.ListOptions{})
	if err == nil {
		var warnings int
		for i := range events.Items {
			e := &events.Items[i]
			if e.Type != corev1.EventTypeWarning {
				continue
			}
			warnings++
			if len(rep.Events) < 50 { // 仅保留前 50 条 Warning 详请
				rep.Events = append(rep.Events, K8sEvent{
					Type: e.Type, Reason: e.Reason,
					Object: eventObject(e), Message: e.Message,
					Count: e.Count, LastSeen: e.LastTimestamp.Unix(),
				})
			}
		}
		rep.Metrics = append(rep.Metrics, K8sMetric{
			Name: "warning_events", Value: float64(warnings), Unit: "count", Timestamp: time.Now(),
		})
	}

	return rep, nil
}

// nodeRoles 由节点标签推导角色（control-plane / master / worker）。
func nodeRoles(n *corev1.Node) string {
	roles := make([]string, 0)
	for label := range n.Labels {
		if strings.HasPrefix(label, "node-role.kubernetes.io/") {
			r := strings.TrimPrefix(label, "node-role.kubernetes.io/")
			if r != "" && r != "<none>" {
				roles = append(roles, r)
			}
		}
	}
	if len(roles) == 0 {
		return "worker"
	}
	return strings.Join(roles, ",")
}

// eventObject 将事件涉及对象格式化为 kind/name。
func eventObject(e *corev1.Event) string {
	if e.InvolvedObject.Kind == "" {
		return e.InvolvedObject.Name
	}
	return e.InvolvedObject.Kind + "/" + e.InvolvedObject.Name
}

// q 安全取 Quantity 字符串（nil 时返回空）。
func q(s string) string { return s }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// 避免未使用导入报错（appsv1 已用，保留以备扩展）。
var _ = appsv1.DeploymentList{}
