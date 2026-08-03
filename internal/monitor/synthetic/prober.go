package synthetic

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/opsorigin/core/internal/monitor/model"
)

type ProbeCallback func(target *model.MonitorTarget, result *model.ProbeResult)

type Prober struct {
	mu           sync.Mutex
	targets      map[string]*model.MonitorTarget
	failureCount map[string]int
	onResult     ProbeCallback
	client       *http.Client
}

func NewProber() *Prober {
	return &Prober{
		targets:      make(map[string]*model.MonitorTarget),
		failureCount: make(map[string]int),
		client: &http.Client{
			Timeout: 10 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

func (p *Prober) OnResult(cb ProbeCallback) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.onResult = cb
}

func (p *Prober) AddTarget(target *model.MonitorTarget) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.targets[target.ID] = target
}

func (p *Prober) RemoveTarget(targetID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.targets, targetID)
	delete(p.failureCount, targetID)
}

func (p *Prober) Run(ctx context.Context) error {
	slog.Info("synthetic prober started")
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("synthetic prober stopped")
			return nil
		case <-ticker.C:
			p.probeAll(ctx)
		}
	}
}

func (p *Prober) probeAll(ctx context.Context) {
	p.mu.Lock()
	targets := make([]*model.MonitorTarget, 0, len(p.targets))
	for _, t := range p.targets {
		targets = append(targets, t)
	}
	cb := p.onResult
	p.mu.Unlock()

	for _, target := range targets {
		for _, proto := range target.Probes {
			port := target.Port
			if port == 0 && proto == model.ProbeHTTP {
				port = 80
			}
			if port == 0 && proto == model.ProbeSSL {
				port = 443
			}
			if port == 0 {
				port = 8080
			}

			task := &model.ProbeTask{
				TargetID: target.ID,
				TenantID: target.TenantID,
				Protocol: proto,
				Address:  target.Address,
				Port:     port,
				Timeout:  target.Timeout,
			}

			result := p.Probe(ctx, task)
			now := time.Now()
			target.LastChecked = &now
			if result.Success {
				target.Status = model.TargetUp
				p.mu.Lock()
				p.failureCount[target.ID] = 0
				p.mu.Unlock()
			} else {
				p.mu.Lock()
				p.failureCount[target.ID]++
				failCount := p.failureCount[target.ID]
				p.mu.Unlock()
				if failCount >= 3 {
					target.Status = model.TargetDown
				}
			}

			if cb != nil {
				cb(target, result)
			}

			select {
			case <-ctx.Done():
				return
			default:
			}
		}
	}
}

func (p *Prober) Probe(ctx context.Context, task *model.ProbeTask) *model.ProbeResult {
	start := time.Now()

	var result *model.ProbeResult
	switch task.Protocol {
	case model.ProbeICMP:
		result = p.probeICMP(ctx, task)
	case model.ProbeTCP:
		result = p.probeTCP(ctx, task)
	case model.ProbeHTTP:
		result = p.probeHTTP(ctx, task)
	case model.ProbeDNS:
		result = p.probeDNS(ctx, task)
	case model.ProbeSSL:
		result = p.probeSSL(ctx, task)
	case model.ProbeUDP:
		result = p.probeUDP(ctx, task)
	default:
		result = &model.ProbeResult{
			Success:      false,
			ErrorMessage: fmt.Sprintf("unsupported protocol: %s", task.Protocol),
			CheckedAt:    time.Now(),
		}
	}

	result.Latency = time.Since(start)
	return result
}

func (p *Prober) probeICMP(ctx context.Context, task *model.ProbeTask) *model.ProbeResult {
	conn, err := net.DialTimeout("ip4:icmp", task.Address, task.Timeout)
	if err != nil {
		return &model.ProbeResult{
			Success:      false,
			ErrorMessage: err.Error(),
			CheckedAt:    time.Now(),
		}
	}
	conn.Close()
	return &model.ProbeResult{
		Success:   true,
		CheckedAt: time.Now(),
	}
}

func (p *Prober) probeTCP(ctx context.Context, task *model.ProbeTask) *model.ProbeResult {
	addr := net.JoinHostPort(task.Address, fmt.Sprintf("%d", task.Port))
	conn, err := net.DialTimeout("tcp", addr, task.Timeout)
	if err != nil {
		return &model.ProbeResult{
			Success:      false,
			ErrorMessage: err.Error(),
			CheckedAt:    time.Now(),
		}
	}
	conn.Close()
	return &model.ProbeResult{
		Success:   true,
		CheckedAt: time.Now(),
	}
}

func (p *Prober) probeHTTP(ctx context.Context, task *model.ProbeTask) *model.ProbeResult {
	url := fmt.Sprintf("http://%s", task.Address)
	if task.Port > 0 {
		url = fmt.Sprintf("http://%s:%d", task.Address, task.Port)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return &model.ProbeResult{
			Success:      false,
			ErrorMessage: err.Error(),
			CheckedAt:    time.Now(),
		}
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return &model.ProbeResult{
			Success:      false,
			ErrorMessage: err.Error(),
			CheckedAt:    time.Now(),
		}
	}
	defer resp.Body.Close()

	return &model.ProbeResult{
		Success:    true,
		StatusCode: resp.StatusCode,
		CheckedAt:  time.Now(),
	}
}

func (p *Prober) probeDNS(ctx context.Context, task *model.ProbeTask) *model.ProbeResult {
	ips, err := net.DefaultResolver.LookupHost(ctx, task.Address)
	if err != nil {
		return &model.ProbeResult{
			Success:      false,
			ErrorMessage: err.Error(),
			CheckedAt:    time.Now(),
		}
	}
	if len(ips) == 0 {
		return &model.ProbeResult{
			Success:      false,
			ErrorMessage: "no DNS records found",
			CheckedAt:    time.Now(),
		}
	}
	return &model.ProbeResult{
		Success:   true,
		CheckedAt: time.Now(),
	}
}

func (p *Prober) probeSSL(ctx context.Context, task *model.ProbeTask) *model.ProbeResult {
	addr := net.JoinHostPort(task.Address, fmt.Sprintf("%d", task.Port))
	if task.Port == 0 {
		addr = net.JoinHostPort(task.Address, "443")
	}

	dialer := &net.Dialer{Timeout: task.Timeout}
	conn, err := tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{
		InsecureSkipVerify: true,
	})
	if err != nil {
		return &model.ProbeResult{
			Success:      false,
			ErrorMessage: err.Error(),
			CheckedAt:    time.Now(),
		}
	}
	defer conn.Close()

	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return &model.ProbeResult{
			Success:      false,
			ErrorMessage: "no peer certificates",
			CheckedAt:    time.Now(),
		}
	}

	leaf := certs[0]
	daysUntilExpiry := int(time.Until(leaf.NotAfter).Hours() / 24)

	var infoParts []string
	infoParts = append(infoParts, fmt.Sprintf("issuer=%s", leaf.Issuer.CommonName))
	infoParts = append(infoParts, fmt.Sprintf("expires_in=%dd", daysUntilExpiry))
	infoParts = append(infoParts, fmt.Sprintf("subject=%s", leaf.Subject.CommonName))

	for _, dnsName := range leaf.DNSNames {
		infoParts = append(infoParts, fmt.Sprintf("dns=%s", dnsName))
	}

	var chainErrors []string
	intermediates := x509.NewCertPool()
	for _, cert := range certs[1:] {
		intermediates.AddCert(cert)
	}
	_, err = leaf.Verify(x509.VerifyOptions{
		Intermediates: intermediates,
		CurrentTime:   time.Now(),
	})
	if err != nil {
		chainErrors = append(chainErrors, err.Error())
	}

	if daysUntilExpiry <= 0 {
		return &model.ProbeResult{
			Success:      false,
			ErrorMessage: fmt.Sprintf("certificate expired %d days ago", -daysUntilExpiry),
			CheckedAt:    time.Now(),
		}
	}

	if daysUntilExpiry <= 30 {
		return &model.ProbeResult{
			Success:      false,
			ErrorMessage: fmt.Sprintf("certificate expires in %d days (threshold: 30)", daysUntilExpiry),
			CheckedAt:    time.Now(),
		}
	}

	if len(chainErrors) > 0 {
		return &model.ProbeResult{
			Success:      false,
			ErrorMessage: fmt.Sprintf("certificate chain verification failed: %s", strings.Join(chainErrors, "; ")),
			CheckedAt:    time.Now(),
		}
	}

	return &model.ProbeResult{
		Success:   true,
		CheckedAt: time.Now(),
	}
}

func (p *Prober) probeUDP(ctx context.Context, task *model.ProbeTask) *model.ProbeResult {
	addr := net.JoinHostPort(task.Address, fmt.Sprintf("%d", task.Port))
	if task.Port == 0 {
		addr = net.JoinHostPort(task.Address, "53")
	}

	conn, err := net.DialTimeout("udp", addr, task.Timeout)
	if err != nil {
		return &model.ProbeResult{
			Success:      false,
			ErrorMessage: err.Error(),
			CheckedAt:    time.Now(),
		}
	}
	defer conn.Close()

	return &model.ProbeResult{
		Success:   true,
		CheckedAt: time.Now(),
	}
}
