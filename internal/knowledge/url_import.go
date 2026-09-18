package knowledge

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/opsorigin/core/pkg/auth"
	"github.com/opsorigin/core/pkg/docextract"
	"github.com/opsorigin/core/pkg/httputil"
)

// —— 网页 URL 抓取入库 ——
// 把外部网页正文抓下来转成知识文章(切片 + embedding)。
// 该端点会由用户触发向外发起请求, 因此必须做 SSRF 防护: 禁止内网/回环/链路本地/元数据地址,
// 且防护要在「DNS 解析之后、真正建连之前」生效, 否则 DNS rebinding 可绕过纯字符串校验。

const (
	urlImportMaxBytes = 5 << 20 // 抓取的响应体上限
	urlImportTimeout  = 20 * time.Second
	urlImportMaxText  = 1 << 20 // 抽取正文上限
	urlImportMaxRedir = 3
)

// isBlockedIP 判定目标 IP 是否属于禁止访问的内网/本机/特殊地址段。
func isBlockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	// IPv4-mapped IPv6(::ffff:10.0.0.1) 归一成 IPv4 再判, 否则可绕过。
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}
	// 注意: 上面已把 IPv4-mapped IPv6 归一成 4 字节, 因此此处按 ip[0]/ip[1] 取首两段。
	if ip.To4() != nil {
		switch {
		case ip[0] == 10: // 10.0.0.0/8
			return true
		case ip[0] == 127: // 127.0.0.0/8
			return true
		case ip[0] == 0: // 0.0.0.0/8
			return true
		case ip[0] == 169 && ip[1] == 254: // 169.254.0.0/16 链路本地(含云元数据 169.254.169.254)
			return true
		case ip[0] == 172 && ip[1] >= 16 && ip[1] <= 31: // 172.16.0.0/12
			return true
		case ip[0] == 192 && ip[1] == 168: // 192.168.0.0/16
			return true
		case ip[0] == 100 && ip[1] >= 64 && ip[1] <= 127: // 100.64.0.0/10 CGNAT
			return true
		case ip[0] >= 224: // 224.0.0.0/4 组播 + 保留
			return true
		}
		return false
	}
	// IPv6: 仅允许全局单播(2000::/3 起), fc00::/7(ULA) 与 fe80::/10 已在上面拦截。
	if ip[0]&0xe0 == 0x20 { // 2000::/3 全局单播
		return false
	}
	return true
}

// validatePublicURL 校验 URL 合法性: 仅 http/https、端口非封锁、主机名可解析且解析结果均为公网地址。
func validatePublicURL(raw string) (*url.URL, error) {
	u, err := url.ParseRequestURI(raw)
	if err != nil {
		return nil, fmt.Errorf("URL 格式非法")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("仅支持 http/https, 当前: %s", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return nil, fmt.Errorf("URL 缺少主机名")
	}
	// 禁止指向本机常见别名(绕过 IP 判断的兜底)
	lower := strings.ToLower(host)
	if lower == "localhost" || lower == "metadata" || lower == "metadata.google.internal" ||
		strings.HasSuffix(lower, ".localhost") || strings.HasSuffix(lower, ".internal") || strings.HasSuffix(lower, ".local") {
		return nil, fmt.Errorf("禁止抓取内网/本机地址")
	}
	// IP 字面量直接判
	if ip := net.ParseIP(host); ip != nil {
		if isBlockedIP(ip) {
			return nil, fmt.Errorf("禁止抓取内网/保留地址")
		}
		return u, nil
	}
	ips, err := net.DefaultResolver.LookupIPAddr(context.Background(), host)
	if err != nil {
		return nil, fmt.Errorf("域名解析失败: %v", err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("域名未解析到 IP")
	}
	for _, ia := range ips {
		if isBlockedIP(ia.IP) {
			return nil, fmt.Errorf("域名解析到内网/保留地址, 已拒绝")
		}
	}
	return u, nil
}

// ssrfSafeTransport 构造带 SSRF 防护的 Transport。
// 关键点: DialContext 的 Control 钩子在建连前拿到内核解析出的真实 IP, 能挡住 DNS rebinding
// (即 validatePublicURL 解析到公网 IP、实际连接却打到内网的攻击)。
func ssrfSafeTransport() *http.Transport {
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 0,
		Control: func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return fmt.Errorf("地址解析失败")
			}
			ip := net.ParseIP(host)
			if ip == nil {
				return fmt.Errorf("非 IP 地址, 拒绝连接")
			}
			if isBlockedIP(ip) {
				return fmt.Errorf("禁止连接内网/保留地址")
			}
			return nil
		},
	}
	return &http.Transport{
		DialContext:         dialer.DialContext,
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout: 10 * time.Second,
		DisableKeepAlives:   true,
	}
}

// handleImportURL 抓取网页正文 → 建知识文章(自动切片/向量化)。
func (s *Service) handleImportURL(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	var req struct {
		URL      string   `json:"url"`
		Title    string   `json:"title"`
		Category string   `json:"category"`
		Tags     []string `json:"tags"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.URL = strings.TrimSpace(req.URL)
	if req.URL == "" {
		httputil.WriteError(w, http.StatusBadRequest, "url is required")
		return
	}
	u, err := validatePublicURL(req.URL)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	client := &http.Client{
		Timeout:       urlImportTimeout,
		Transport:     ssrfSafeTransport(),
		CheckRedirect: ssrfRedirectGuard,
	}
	resp, err := client.Get(u.String())
	if err != nil {
		slog.Warn("knowledge: url import fetch failed", "url", req.URL, "err", err)
		httputil.WriteError(w, http.StatusBadGateway, "抓取失败: "+err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		httputil.WriteError(w, http.StatusBadGateway, fmt.Sprintf("目标返回 HTTP %d", resp.StatusCode))
		return
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, urlImportMaxBytes+1))
	if err != nil {
		httputil.WriteError(w, http.StatusBadGateway, "读取响应失败")
		return
	}
	if len(raw) > urlImportMaxBytes {
		httputil.WriteError(w, http.StatusRequestEntityTooLarge, "网页超过 5MB 上限")
		return
	}
	body := string(raw)
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = extractHTMLTitle(body)
	}
	if title == "" {
		title = u.Hostname()
	}
	content := docextract.ExtractHTMLText(body)
	if len(content) > urlImportMaxText {
		content = content[:urlImportMaxText]
	}
	if strings.TrimSpace(content) == "" {
		httputil.WriteError(w, http.StatusUnprocessableEntity, "未从网页中抽取到正文")
		return
	}
	// 正文前补来源链接, 便于引用溯源。
	content = "# " + title + "\n\n> 来源: " + u.String() + "\n\n" + content
	now := time.Now()
	a := &Article{
		ID:             uuid.New().String(),
		TenantID:       tenantID,
		Title:          title,
		Content:        content,
		Category:       req.Category,
		Tags:           req.Tags,
		Status:         "draft",
		Version:        1,
		ViewPermission: "all",
		EditPermission: "owner",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := s.store.Create(r.Context(), a); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to create article: "+err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, a)
}

// ssrfRedirectGuard 重定向时同样做 SSRF 校验, 并限制跳转次数(防重定向绕过内网限制)。
func ssrfRedirectGuard(req *http.Request, via []*http.Request) error {
	if len(via) >= urlImportMaxRedir {
		return fmt.Errorf("重定向次数超限")
	}
	if _, err := validatePublicURL(req.URL.String()); err != nil {
		return err
	}
	return nil
}

var reHTMLTitle = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)

// extractHTMLTitle 抽取 <title> 作为默认标题。
func extractHTMLTitle(body string) string {
	m := reHTMLTitle.FindStringSubmatch(body)
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(docextract.XMLUnescape(m[1]))
}
