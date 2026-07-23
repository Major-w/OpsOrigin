package cloud

import (
	"fmt"
	"sort"
	"sync"
)

// registry 保存已注册的 Provider 实现。
var (
	regMu     sync.RWMutex
	providers = make(map[string]Provider)
)

// Register 注册一个云 Provider，应在各 provider 包的 init() 中调用。
// 同名重复注册会 panic（开发期尽早暴露配置错误）。
func Register(p Provider) {
	if p == nil {
		panic("cloud: Register nil provider")
	}
	name := p.Name()
	if name == "" {
		panic("cloud: provider Name() must not be empty")
	}
	regMu.Lock()
	defer regMu.Unlock()
	if _, ok := providers[name]; ok {
		panic(fmt.Sprintf("cloud: provider %q already registered", name))
	}
	providers[name] = p
}

// Get 按厂商标识取 Provider。
func Get(name string) (Provider, error) {
	regMu.RLock()
	defer regMu.RUnlock()
	p, ok := providers[name]
	if !ok {
		return nil, &ProviderNotFound{Name: name}
	}
	return p, nil
}

// Names 返回全部已注册厂商名（排序，便于 UI 展示）。
func Names() []string {
	regMu.RLock()
	defer regMu.RUnlock()
	names := make([]string, 0, len(providers))
	for n := range providers {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Exists 判断厂商是否已注册。
func Exists(name string) bool {
	regMu.RLock()
	defer regMu.RUnlock()
	_, ok := providers[name]
	return ok
}
