package main

import (
	"log/slog"
)

type TunnelManager struct {
	proxy *Proxy
}

func NewTunnelManager(proxy *Proxy) *TunnelManager {
	return &TunnelManager{proxy: proxy}
}

func (m *TunnelManager) Run() {
	slog.Info("tunnel manager running")
}
