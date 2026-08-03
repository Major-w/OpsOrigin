package service

import (
	"encoding/json"
	"net/http"

	"github.com/opsorigin/core/internal/lowcode/component"
)

// MarketHandler 组件市场API处理器
type MarketHandler struct {
	market *component.MarketService
}

// NewMarketHandler 创建组件市场处理器
func NewMarketHandler(market *component.MarketService) *MarketHandler {
	return &MarketHandler{market: market}
}

// RegisterRoutes 注册路由
func (h *MarketHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/lowcode/market/categories", h.handleCategories)
	mux.HandleFunc("/api/v1/lowcode/market/components", h.handleComponents)
	mux.HandleFunc("/api/v1/lowcode/market/components/", h.handleComponent)
	mux.HandleFunc("/api/v1/lowcode/market/install/", h.handleInstall)
	mux.HandleFunc("/api/v1/lowcode/market/rate/", h.handleRate)
}

// handleCategories 处理分类请求
func (h *MarketHandler) handleCategories(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	categories := h.market.GetCategories()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"data":    categories,
	})
}

// handleComponents 处理组件列表请求
func (h *MarketHandler) handleComponents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	category := r.URL.Query().Get("category")
	keyword := r.URL.Query().Get("keyword")

	components := h.market.GetComponents(category, keyword)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"data":    components,
	})
}

// handleComponent 处理单个组件请求
func (h *MarketHandler) handleComponent(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Path[len("/api/v1/lowcode/market/components/"):]
	if id == "" {
		http.Error(w, `{"error":"missing component id"}`, http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		comp, err := h.market.GetComponent(id)
		if err != nil {
			http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"data":    comp,
		})
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

// handleInstall 处理安装组件请求
func (h *MarketHandler) handleInstall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	id := r.URL.Path[len("/api/v1/lowcode/market/install/"):]
	if id == "" {
		http.Error(w, `{"error":"missing component id"}`, http.StatusBadRequest)
		return
	}

	err := h.market.InstallComponent(id)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusNotFound)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "component installed successfully",
	})
}

// handleRate 处理评分组件请求
func (h *MarketHandler) handleRate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	id := r.URL.Path[len("/api/v1/lowcode/market/rate/"):]
	if id == "" {
		http.Error(w, `{"error":"missing component id"}`, http.StatusBadRequest)
		return
	}

	var req struct {
		Rating float64 `json:"rating"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	err := h.market.RateComponent(id, req.Rating)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "component rated successfully",
	})
}
