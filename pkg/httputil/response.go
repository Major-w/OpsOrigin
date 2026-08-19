package httputil

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
)

// EmptySliceIfNil 把 nil slice 规范化为空数组,避免序列化成 JSON null。
//
// 背景:Go 的 `var xs []T` 零值是 nil,encoding/json 会输出 `null` 而不是 `[]`。
// 前端拿到 null 后执行 `list.map(...)` / `list.filter(...)` 会直接抛
// "Cannot read properties of null",整个页面白屏。列表接口在无数据时
// 必须返回 `[]`,这是 REST 契约的一部分,不能把兜底责任全推给前端。
func EmptySliceIfNil(v any) any {
	if v == nil {
		return v
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Slice && rv.IsNil() {
		return []any{}
	}
	return v
}

func WriteJSON(w http.ResponseWriter, status int, v any) {
	v = EmptySliceIfNil(v)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func WriteError(w http.ResponseWriter, status int, msg string) {
	WriteJSON(w, status, map[string]string{"error": msg})
}

// BindJSON 解析请求体 JSON 到 v（handler 样板去重入口）。
// 统一错误语义为 "invalid request body"，handler 只需：
//
//	var req model.X
//	if err := httputil.BindJSON(r, &req); err != nil {
//		httputil.WriteError(w, http.StatusBadRequest, err.Error())
//		return
//	}
func BindJSON(r *http.Request, v any) error {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		return fmt.Errorf("invalid request body: %w", err)
	}
	return nil
}

// Param 返回 Go 1.22 路径参数（mux "GET /x/{id}" 模式），等价 r.PathValue。
func Param(r *http.Request, name string) string {
	return r.PathValue(name)
}
