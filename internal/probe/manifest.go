package probe

import "fmt"

// GenerateManifest 生成 K8s 探针的一键安装清单（ServiceAccount + ClusterRole
// + ClusterRoleBinding + DaemonSet），与主机 Agent 的 install-script 同范式。
// 探针以 in-cluster 形态运行（kubeconfig 为空，使用 ServiceAccount）。
// 返回的 YAML 可直接 `kubectl apply -f -`。
func GenerateManifest(server, token, cluster, namespace, image string) string {
	if namespace == "" {
		namespace = "kube-system"
	}
	if image == "" {
		image = "opsorigin/k8s-probe:latest"
	}
	return fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: %s
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: opsorigin-probe
  namespace: %s
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: opsorigin-probe
rules:
  - apiGroups: [""]
    resources: ["nodes", "pods", "events"]
    verbs: ["get", "list", "watch"]
  - apiGroups: ["apps"]
    resources: ["deployments", "statefulsets", "daemonsets"]
    verbs: ["get", "list", "watch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: opsorigin-probe
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: opsorigin-probe
subjects:
  - kind: ServiceAccount
    name: opsorigin-probe
    namespace: %s
---
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: opsorigin-probe
  namespace: %s
  labels:
    app: opsorigin-probe
spec:
  selector:
    matchLabels:
      app: opsorigin-probe
  template:
    metadata:
      labels:
        app: opsorigin-probe
    spec:
      serviceAccountName: opsorigin-probe
      tolerations:
        - operator: Exists
      containers:
        - name: probe
          image: %s
          args:
            - "--server"
            - "%s"
            - "--token"
            - "%s"
            - "--cluster"
            - "%s"
            - "--in-cluster"
          resources:
            requests:
              cpu: 50m
              memory: 64Mi
            limits:
              cpu: 200m
              memory: 256Mi
          securityContext:
            readOnlyRootFilesystem: true
            runAsNonRoot: true
`, namespace, namespace, namespace, namespace, image, server, token, cluster)
}
