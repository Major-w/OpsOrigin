package httputil

import (
	"bufio"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
)

// RecoverHandler 捕获下游 handler 链抛出的 panic, 记录堆栈并返回 500,
// 避免单个端点 panic 拖垮整个进程。作为最外层 Handler 包装使用时,
// 可覆盖包括 bypass 前缀在内的全部请求(见 Server.WithHandlerWrapper)。
//
// 若 panic 发生在响应已开始写出之后, 无法再改写状态码, 此时仅记录日志
// 并截断连接, 保证进程不崩溃。
func RecoverHandler(next http.Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tw := &trackedWriter{ResponseWriter: w}
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			logger.Error("http handler panic recovered",
				"panic", rec,
				"method", r.Method,
				"path", r.URL.Path,
				"remote", r.RemoteAddr,
				"stack", string(debug.Stack()),
			)
			// 响应已开始则不再写状态码, 仅记录日志。
			if !tw.wrote {
				WriteError(tw, http.StatusInternalServerError, "internal server error")
			}
		}()
		next.ServeHTTP(tw, r)
	})
}

// trackedWriter 包装 ResponseWriter 以跟踪响应是否已开始写出,
// 并透传 Flusher(SSE/WebSocket 等流式响应依赖)。
type trackedWriter struct {
	http.ResponseWriter
	wrote bool
}

func (w *trackedWriter) WriteHeader(code int) {
	w.wrote = true
	w.ResponseWriter.WriteHeader(code)
}

func (w *trackedWriter) Write(b []byte) (int, error) {
	w.wrote = true
	return w.ResponseWriter.Write(b)
}

func (w *trackedWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack 透传给底层 writer, 使 WebSocket Upgrade 握手在经过 RecoverHandler
// 包装后依然能拿到 http.Hijacker 能力; 否则 websocket.Accept 会因
// ResponseWriter 未实现 Hijacker 而返回 501 (does not implement http.Hijacker)。
func (w *trackedWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := w.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, errors.New("underlying ResponseWriter does not implement http.Hijacker")
}
