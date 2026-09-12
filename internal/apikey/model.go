package apikey

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"time"
)

// KeyPrefix 是所有 API Key 明文的统一前缀，便于在日志/代码中识别（也便于网关侧快速判别）。
const KeyPrefix = "osk_"

// rawKeyBytes 明文随机部分长度（32 字节 ≈ 43 字符 base64url），保守超出 256 bit 熵。
const rawKeyBytes = 32

// Key 是 API Key 的持久化/展示模型。结构体中不含明文 —— 明文只在创建响应里返回一次。
//
// 权限语义：Key 不冗余任何权限点。鉴权命中后用 UserID 回查用户当前角色，
// 再走 IAM 聚合富化 —— 因此用户角色/用户组变更后，其名下所有 Key 的权限自动跟随。
type Key struct {
	ID         string     `json:"id"`
	TenantID   string     `json:"tenant_id"`
	UserID     string     `json:"user_id"`
	Username   string     `json:"username,omitempty"` // 展示用（JOIN users，可能为空）
	Name       string     `json:"name"`
	KeyPrefix  string     `json:"key_prefix"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	UseCount   int64      `json:"use_count"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

// Status 派生展示状态：active / expired / revoked。
func (k *Key) Status(now time.Time) string {
	switch {
	case k.RevokedAt != nil:
		return "revoked"
	case k.ExpiresAt != nil && now.After(*k.ExpiresAt):
		return "expired"
	default:
		return "active"
	}
}

// createRequest 是新建 Key 的入参。ExpiresInDays <= 0 表示永不过期。
type createRequest struct {
	Name          string `json:"name"`
	ExpiresInDays int    `json:"expires_in_days"`
	UserID        string `json:"user_id"` // 仅管理员可代为指定；普通用户忽略（强制本人）
}

// userRefResolver 由 cmd/core 注入（user.Service），返回用户当前角色与状态。
// 未注入时校验直接失败（fail-closed），绝不放行未知主体。
type userRefResolver func(ctx context.Context, tenantID, userID string) (role, status string, ok bool)

// newRawKey 生成明文 Key：osk_ + 32 字节随机数的 base64url（无填充）。
func newRawKey() (string, error) {
	buf := make([]byte, rawKeyBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate api key: %w", err)
	}
	return KeyPrefix + base64.RawURLEncoding.EncodeToString(buf), nil
}

// hashKey 计算落库摘要。用 SHA-256 而非 bcrypt：Key 是高熵随机串，
// 不存在字典攻击面，需要的是「可索引的确定性摘要」以便带索引查表。
func hashKey(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// displayPrefix 截取可读前缀（osk_ + 8 字符），列表页用于辨认。
func displayPrefix(raw string) string {
	const n = len(KeyPrefix) + 8
	if len(raw) <= n {
		return raw
	}
	return raw[:n]
}
