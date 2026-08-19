package agentmgr

import (
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/opsorigin/core/internal/ent"
	entFile "github.com/opsorigin/core/internal/ent/file"
	"github.com/opsorigin/core/internal/token"

	"github.com/opsorigin/core/pkg/httputil"
)

func (s *Service) handleDownloadAgent(w http.ResponseWriter, r *http.Request) {
	osType := r.URL.Query().Get("os")
	arch := r.URL.Query().Get("arch")
	if arch == "" {
		arch = "amd64"
	}
	downloadType := r.URL.Query().Get("type")
	if downloadType == "" {
		downloadType = "agent"
	}

	// 获取服务器地址(支持 HTTPS)
	scheme := s.determineScheme(r)
	serverAddr := fmt.Sprintf("%s://%s", scheme, r.Host)

	// 二进制文件下载
	if downloadType == "proxy-bin" || downloadType == "agent-bin" {
		cwd, _ := os.Getwd()

		// 支持的二进制文件名列表
		var binaryNames []string
		if downloadType == "agent-bin" {
			binaryNames = []string{"opsagent", "agent"}
		} else {
			binaryNames = []string{"opsproxy", "proxy"}
		}

		if osType == "darwin" {
			for i, n := range binaryNames {
				binaryNames[i] = n + "-darwin"
			}
		} else if osType == "windows" {
			for i, n := range binaryNames {
				binaryNames[i] = n + ".exe"
			}
		}
		if arch == "arm64" {
			for i, n := range binaryNames {
				binaryNames[i] = n + "-arm64"
			}
		}

		var binPath string
		for _, name := range binaryNames {
			candidate := filepath.Join(cwd, "bin", name)
			if _, err := os.Stat(candidate); err == nil {
				binPath = candidate
				break
			}
		}
		if binPath == "" {
			httputil.WriteError(w, 404, "binary not found, please build first: make build")
			return
		}

		fileName := filepath.Base(binPath)
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", fileName))
		http.ServeFile(w, r, binPath)
		return
	}

	if downloadType == "proxy" {
		switch osType {
		case "linux", "darwin":
			w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
			w.Header().Set("Content-Disposition", "attachment; filename=install_proxy.sh")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(generateProxyInstallScript(serverAddr, osType, arch)))
		case "windows":
			w.Header().Set("Content-Type", "application/x-powershell; charset=utf-8")
			w.Header().Set("Content-Disposition", "attachment; filename=install_proxy.ps1")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(generateWindowsProxyInstallScript(serverAddr, arch)))
		default:
			httputil.WriteError(w, http.StatusBadRequest, "os parameter required: linux, darwin, or windows")
		}
		return
	}

	switch osType {
	case "linux", "darwin":
		w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
		w.Header().Set("Content-Disposition", "attachment; filename=install_agent.sh")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(generateInstallScript(serverAddr, osType, arch)))

	case "windows":
		w.Header().Set("Content-Type", "application/x-powershell; charset=utf-8")
		w.Header().Set("Content-Disposition", "attachment; filename=install_agent.ps1")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(generateWindowsInstallScript(serverAddr, arch)))

	default:
		httputil.WriteError(w, http.StatusBadRequest, "os parameter required: linux, darwin, or windows")
	}
}

// handleServeFile 中心存储文件拉取端点(§5):
//   - 校验短期令牌(file_id 绑定 + 未过期);
//   - 限流/熔断(按 agent_id);
//   - 以 http.ServeContent 支持 Range 断点续传;
//   - 失败计入熔断,成功清零。
func (s *Service) handleServeFile(w http.ResponseWriter, r *http.Request) {
	fileID := r.PathValue("file_id")
	tok := r.URL.Query().Get("token")
	if tok == "" {
		httputil.WriteError(w, http.StatusUnauthorized, "missing download token")
		return
	}
	claims, err := token.Verify(s.fileTokenSecret, tok)
	if err != nil {
		status := http.StatusForbidden
		if err == token.ErrExpired {
			status = http.StatusUnauthorized
		}
		httputil.WriteError(w, status, "invalid download token: "+err.Error())
		return
	}
	if claims.FileID != fileID {
		httputil.WriteError(w, http.StatusForbidden, "token/file mismatch")
		return
	}

	key := claims.AgentID
	if !s.fileLimiter.Allow(key) {
		httputil.WriteError(w, http.StatusTooManyRequests, "download rate limited")
		return
	}

	if s.fileStoreDir == "" {
		s.fileLimiter.RecordFailure(key)
		httputil.WriteError(w, http.StatusInternalServerError, "file store not configured")
		return
	}
	// 防目录穿越:file_id 必须是裸文件名,不含路径分隔符。
	if filepath.Base(fileID) != fileID {
		s.fileLimiter.RecordFailure(key)
		httputil.WriteError(w, http.StatusBadRequest, "invalid file_id")
		return
	}
	fp := filepath.Join(s.fileStoreDir, fileID)
	info, err := os.Stat(fp)
	if err != nil {
		s.fileLimiter.RecordFailure(key)
		httputil.WriteError(w, http.StatusNotFound, "file not found")
		return
	}
	f, err := os.Open(fp)
	if err != nil {
		s.fileLimiter.RecordFailure(key)
		httputil.WriteError(w, http.StatusInternalServerError, "open file failed")
		return
	}
	defer f.Close()

	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", fileID))
	http.ServeContent(w, r, fileID, info.ModTime(), f)
	s.fileLimiter.RecordSuccess(key)
}

func (s *Service) handleUploadFile(w http.ResponseWriter, r *http.Request) {
	if s.fileStoreDir == "" {
		httputil.WriteError(w, http.StatusInternalServerError, "file store not configured (SetFileStoreDir)")
		return
	}
	r.ParseMultipartForm(256 << 20) // 256MB max
	file, header, err := r.FormFile("file")
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "missing file field: "+err.Error())
		return
	}
	defer file.Close()
	fid := uuid.New().String()
	storedPath := filepath.Join(s.fileStoreDir, fid)
	out, err := os.Create(storedPath)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to store file: "+err.Error())
		return
	}
	defer out.Close()
	h := sha256.New()
	tr := io.TeeReader(file, h)
	size, err := io.Copy(out, tr)
	if err != nil {
		os.Remove(storedPath)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to write file: "+err.Error())
		return
	}
	checksum := fmt.Sprintf("%x", h.Sum(nil))
	tenantID := r.Header.Get("X-Tenant-ID")
	if tenantID == "" {
		tenantID = "default"
	}
	meta := fileMeta{
		ID: fid, OriginalName: header.Filename, StoredPath: storedPath,
		SizeBytes: size, Checksum: checksum, ChecksumAlgo: "sha256",
		TenantID: tenantID, CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	// Save metadata via ent if available
	if s.entClient != nil {
		_, err = s.entClient.File.Create().
			SetID(fid).SetTenantID(tenantID).
			SetOriginalName(header.Filename).SetStoredPath(storedPath).
			SetSizeBytes(size).SetChecksum(checksum).SetChecksumAlgo("sha256").
			SetCreatedAt(time.Now()).SetUpdatedAt(time.Now()).
			Save(r.Context())
		if err != nil {
			slog.Warn("file meta save failed", "error", err)
		}
	}
	httputil.WriteJSON(w, http.StatusCreated, meta)
}

func (s *Service) handleListFiles(w http.ResponseWriter, r *http.Request) {
	if s.entClient == nil {
		httputil.WriteJSON(w, http.StatusOK, []fileMeta{})
		return
	}
	tenantID := r.Header.Get("X-Tenant-ID")
	if tenantID == "" {
		tenantID = "default"
	}
	files, err := s.entClient.File.Query().Where(entFile.TenantIDEQ(tenantID)).Order(ent.Desc(entFile.FieldCreatedAt)).All(r.Context())
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	result := make([]fileMeta, 0, len(files))
	for _, f := range files {
		result = append(result, fileMeta{
			ID: f.ID, OriginalName: f.OriginalName, SizeBytes: f.SizeBytes,
			Checksum: f.Checksum, ChecksumAlgo: f.ChecksumAlgo,
			TenantID: f.TenantID, CreatedAt: f.CreatedAt.Format(time.RFC3339),
		})
	}
	if result == nil {
		result = []fileMeta{}
	}
	httputil.WriteJSON(w, http.StatusOK, result)
}

func (s *Service) handleDeleteFile(w http.ResponseWriter, r *http.Request) {
	fid := r.PathValue("file_id")
	if s.entClient != nil {
		s.entClient.File.DeleteOneID(fid).Exec(r.Context())
	}
	if s.fileStoreDir != "" {
		os.Remove(filepath.Join(s.fileStoreDir, fid))
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Service) handleFileMeta(w http.ResponseWriter, r *http.Request) {
	fid := r.PathValue("file_id")
	if s.entClient == nil {
		httputil.WriteError(w, http.StatusNotFound, "file not found")
		return
	}
	f, err := s.entClient.File.Get(r.Context(), fid)
	if err != nil {
		httputil.WriteError(w, http.StatusNotFound, "file not found")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, fileMeta{
		ID: f.ID, OriginalName: f.OriginalName, SizeBytes: f.SizeBytes,
		Checksum: f.Checksum, ChecksumAlgo: f.ChecksumAlgo,
		TenantID: f.TenantID, CreatedAt: f.CreatedAt.Format(time.RFC3339),
	})
}
