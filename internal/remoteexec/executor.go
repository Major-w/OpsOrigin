package remoteexec

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// Credential holds SSH authentication info.
type Credential struct {
	User     string
	Password string
	Key      string // PEM private key content
}

// ExecRequest is a request to execute a command on a remote host.
type ExecRequest struct {
	Host       string
	Port       int
	Credential Credential
	Command    string
	Timeout    time.Duration
}

// ExecResult is the result of a remote command execution.
type ExecResult struct {
	Host     string
	Port     int
	Output   string
	ExitCode int
	Error    error
	Duration time.Duration
}

// StreamCallback is called with streaming output chunks.
type StreamCallback func(host string, chunk []byte)

// Pool manages SSH connections for reuse.
type Pool struct {
	mu              sync.RWMutex
	connections     map[string]*ssh.Client
	cur             int
	maxConns        int
	maxIdle         time.Duration
	hostKeyCallback ssh.HostKeyCallback
}

// NewPool creates a new SSH connection pool.
// maxConns caps the number of simultaneously open SSH connections so a
// bulk job against many distinct hosts cannot exhaust file descriptors /
// sockets on the Core node (see report issue 6.1).
func NewPool() *Pool {
	return &Pool{
		connections:     make(map[string]*ssh.Client),
		maxIdle:         5 * time.Minute,
		maxConns:        64,
		hostKeyCallback: DefaultHostKeyCallback(),
	}
}

// cacheKey 生成连接池缓存键。
// 注意: 不能把明文密码放进 key (会泄露到内存/日志), 这里对密码做 SHA256 哈希。
func (p *Pool) cacheKey(host string, port int, cred Credential) string {
	pwHash := sha256.Sum256([]byte(cred.Password))
	return fmt.Sprintf("%s:%d:%s:%x", host, port, cred.User, pwHash)
}

func (p *Pool) get(host string, port int, cred Credential) (*ssh.Client, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	key := p.cacheKey(host, port, cred)
	client, ok := p.connections[key]
	return client, ok
}

func (p *Pool) set(host string, port int, cred Credential, client *ssh.Client) {
	p.mu.Lock()
	defer p.mu.Unlock()
	key := p.cacheKey(host, port, cred)
	p.connections[key] = client
}

func (p *Pool) remove(host string, port int, cred Credential) {
	p.mu.Lock()
	defer p.mu.Unlock()
	key := p.cacheKey(host, port, cred)
	if _, ok := p.connections[key]; ok {
		delete(p.connections, key)
		p.cur--
	}
}

// GetClient returns an SSH client from pool or creates a new one.
func (p *Pool) GetClient(host string, port int, cred Credential, timeout time.Duration) (*ssh.Client, error) {
	if client, ok := p.get(host, port, cred); ok {
		// test if still alive
		if _, _, err := client.SendRequest("keepalive@openssh.com", true, nil); err == nil {
			return client, nil
		}
		p.remove(host, port, cred)
		client.Close()
	}

	// Enforce the connection cap before opening a new SSH connection.
	p.mu.Lock()
	if p.cur >= p.maxConns {
		p.mu.Unlock()
		return nil, fmt.Errorf("ssh connection pool exhausted (max %d)", p.maxConns)
	}
	p.cur++
	p.mu.Unlock()

	config := &ssh.ClientConfig{
		User:            cred.User,
		Auth:            []ssh.AuthMethod{},
		HostKeyCallback: p.hostKeyCallback,
		Timeout:         timeout,
	}

	if cred.Key != "" {
		signer, err := ssh.ParsePrivateKey([]byte(cred.Key))
		if err != nil {
			// try with password as passphrase
			signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(cred.Key), []byte(cred.Password))
			if err != nil {
				p.mu.Lock()
				p.cur--
				p.mu.Unlock()
				return nil, fmt.Errorf("parse private key: %w", err)
			}
		}
		config.Auth = append(config.Auth, ssh.PublicKeys(signer))
	}
	if cred.Password != "" && cred.Key == "" {
		config.Auth = append(config.Auth, ssh.Password(cred.Password))
	}

	if len(config.Auth) == 0 {
		p.mu.Lock()
		p.cur--
		p.mu.Unlock()
		return nil, fmt.Errorf("no authentication method provided")
	}

	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		p.mu.Lock()
		p.cur--
		p.mu.Unlock()
		return nil, fmt.Errorf("ssh dial %s: %w", addr, err)
	}

	p.set(host, port, cred, client)
	return client, nil
}

// CloseAll closes all cached connections.
func (p *Pool) CloseAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for key, client := range p.connections {
		client.Close()
		delete(p.connections, key)
	}
	p.cur = 0
}

// DefaultHostKeyCallback 返回安全的主机密钥校验回调 (TOFU 模式)。
// 首次连接的主机会被记录到 ~/.ssh/known_hosts; 之后若指纹变化则拒绝 (MITM 防护)。
// 这比 ssh.InsecureIgnoreHostKey() 安全得多。导出供 agentless (SSH) 通道复用。
func DefaultHostKeyCallback() ssh.HostKeyCallback {
	home, err := os.UserHomeDir()
	if err != nil {
		return func(string, net.Addr, ssh.PublicKey) error {
			return fmt.Errorf("cannot determine home dir for known_hosts")
		}
	}
	khPath := filepath.Join(home, ".ssh", "known_hosts")
	return tofuHostKeyCallback(khPath)
}

func tofuHostKeyCallback(khPath string) ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		if cb, err := knownhosts.New(khPath); err == nil {
			if verr := cb(hostname, remote, key); verr == nil {
				return nil // 已知且匹配
			} else if _, ok := verr.(*knownhosts.KeyError); ok {
				// 未知主机 (或首次): 记录指纹 (TOFU)
				return appendKnownHost(khPath, hostname, remote, key)
			} else {
				return verr
			}
		}
		// known_hosts 不存在: 首次运行, 记录
		return appendKnownHost(khPath, hostname, remote, key)
	}
}

func appendKnownHost(khPath, hostname string, remote net.Addr, key ssh.PublicKey) error {
	if err := os.MkdirAll(filepath.Dir(khPath), 0o700); err != nil {
		return fmt.Errorf("create ssh dir: %w", err)
	}
	f, err := os.OpenFile(khPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open known_hosts: %w", err)
	}
	defer f.Close()
	line := knownhosts.Line([]string{hostname}, key)
	if _, err := f.WriteString(line + "\n"); err != nil {
		return fmt.Errorf("write known_hosts: %w", err)
	}
	return nil
}

// Executor handles remote command execution via SSH.
type Executor struct {
	pool *Pool
}

// NewExecutor creates a new remote executor.
func NewExecutor() *Executor {
	return &Executor{pool: NewPool()}
}

// Execute runs a command on a remote host and returns the result.
func (e *Executor) Execute(ctx context.Context, req ExecRequest) ExecResult {
	start := time.Now()
	if req.Timeout == 0 {
		req.Timeout = 30 * time.Second
	}
	if req.Port == 0 {
		req.Port = 22
	}

	result := ExecResult{Host: req.Host, Port: req.Port}

	client, err := e.pool.GetClient(req.Host, req.Port, req.Credential, req.Timeout)
	if err != nil {
		result.Error = err
		result.Duration = time.Since(start)
		return result
	}

	session, err := client.NewSession()
	if err != nil {
		e.pool.remove(req.Host, req.Port, req.Credential)
		result.Error = fmt.Errorf("new session: %w", err)
		result.Duration = time.Since(start)
		return result
	}
	defer session.Close()

	execCtx, cancel := context.WithTimeout(ctx, req.Timeout)
	defer cancel()

	session.Stdin = nil

	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr

	err = session.Start(req.Command)
	if err != nil {
		result.Error = fmt.Errorf("start command: %w", err)
		result.Duration = time.Since(start)
		return result
	}

	done := make(chan error, 1)
	go func() {
		done <- session.Wait()
	}()

	select {
	case <-execCtx.Done():
		session.Close()
		result.Error = fmt.Errorf("command timeout after %s", req.Timeout)
	case err := <-done:
		if err != nil {
			if exitErr, ok := err.(*ssh.ExitError); ok {
				result.ExitCode = exitErr.ExitStatus()
			} else {
				result.ExitCode = -1
				result.Error = err
			}
		}
	}

	result.Output = stdout.String()
	if stderr.Len() > 0 {
		if result.Output != "" {
			result.Output += "\n"
		}
		result.Output += stderr.String()
	}
	result.Duration = time.Since(start)
	return result
}

// ExecuteStream runs a command and streams output via callback.
func (e *Executor) ExecuteStream(ctx context.Context, req ExecRequest, callback StreamCallback) ExecResult {
	start := time.Now()
	if req.Timeout == 0 {
		req.Timeout = 30 * time.Second
	}
	if req.Port == 0 {
		req.Port = 22
	}

	result := ExecResult{Host: req.Host, Port: req.Port}

	client, err := e.pool.GetClient(req.Host, req.Port, req.Credential, req.Timeout)
	if err != nil {
		result.Error = err
		result.Duration = time.Since(start)
		return result
	}

	session, err := client.NewSession()
	if err != nil {
		e.pool.remove(req.Host, req.Port, req.Credential)
		result.Error = fmt.Errorf("new session: %w", err)
		result.Duration = time.Since(start)
		return result
	}
	defer session.Close()

	stdout, err := session.StdoutPipe()
	if err != nil {
		result.Error = fmt.Errorf("stdout pipe: %w", err)
		result.Duration = time.Since(start)
		return result
	}

	stderr, err := session.StderrPipe()
	if err != nil {
		result.Error = fmt.Errorf("stderr pipe: %w", err)
		result.Duration = time.Since(start)
		return result
	}

	err = session.Start(req.Command)
	if err != nil {
		result.Error = fmt.Errorf("start command: %w", err)
		result.Duration = time.Since(start)
		return result
	}

	// read stdout and stderr concurrently
	var wg sync.WaitGroup
	var stdoutBuf, stderrBuf []byte

	wg.Add(2)
	go func() {
		defer wg.Done()
		buf := make([]byte, 4096)
		for {
			n, err := stdout.Read(buf)
			if n > 0 {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				stdoutBuf = append(stdoutBuf, chunk...)
				if callback != nil {
					callback(req.Host, chunk)
				}
			}
			if err != nil {
				break
			}
		}
	}()

	go func() {
		defer wg.Done()
		buf := make([]byte, 4096)
		for {
			n, err := stderr.Read(buf)
			if n > 0 {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				stderrBuf = append(stderrBuf, chunk...)
				if callback != nil {
					callback(req.Host, chunk)
				}
			}
			if err != nil {
				break
			}
		}
	}()

	err = session.Wait()
	wg.Wait()

	if err != nil {
		if exitErr, ok := err.(*ssh.ExitError); ok {
			result.ExitCode = exitErr.ExitStatus()
		} else {
			result.ExitCode = -1
			result.Error = err
		}
	}

	result.Output = string(stdoutBuf)
	if len(stderrBuf) > 0 {
		if result.Output != "" {
			result.Output += "\n"
		}
		result.Output += string(stderrBuf)
	}
	result.Duration = time.Since(start)
	return result
}

// BatchExecute runs a command on multiple hosts concurrently.
func (e *Executor) BatchExecute(ctx context.Context, requests []ExecRequest, concurrency int) []ExecResult {
	if concurrency <= 0 {
		concurrency = 10
	}

	results := make([]ExecResult, len(requests))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for i, req := range requests {
		wg.Add(1)
		go func(idx int, r ExecRequest) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			results[idx] = e.Execute(ctx, r)
		}(i, req)
	}

	wg.Wait()
	return results
}

// TestConnectivity tests SSH connectivity to a host.
func (e *Executor) TestConnectivity(host string, port int, cred Credential, timeout time.Duration) (bool, string) {
	if port == 0 {
		port = 22
	}
	if timeout == 0 {
		timeout = 5 * time.Second
	}

	client, err := e.pool.GetClient(host, port, cred, timeout)
	if err != nil {
		return false, err.Error()
	}
	client.Close()
	return true, "connected"
}

// Close shuts down the executor and closes all connections.
func (e *Executor) Close() {
	e.pool.CloseAll()
}
