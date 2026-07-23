// Package token 实现 §6 文件下载短期令牌(≤10min)。
//
// 采用 stdlib 自实现 HMAC-SHA256 的 JWT 风格令牌,避免引入 golang-jwt 等
// 外部依赖(本仓库 go.mod 在 ai/agent 缺失时无法 tidy,加依赖风险高)。
//
// 令牌结构:header(固定 HS256).payload(file_id,agent_id,iat,exp).sig(HMAC)。
// 服务端生成下载地址时签发;Agent/Proxy 下载文件时附带;Proxy 文件代理校验。
package token

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrInvalid / ErrExpired 校验失败原因。
var (
	ErrInvalid  = errors.New("invalid download token")
	ErrExpired  = errors.New("download token expired")
	ErrNotYet   = errors.New("download token not yet valid")
)

// Claims 令牌载荷。
type Claims struct {
	FileID  string `json:"fid"` // 文件标识(中心存储 object key 或 task id)
	AgentID string `json:"aid"` // 允许下载的 Agent/主机标识(防令牌盗用转售)
	Iat     int64  `json:"iat"` // 签发时间
	Exp     int64  `json:"exp"` // 过期时间(建议 ≤ 现在+600s)
}

const maxTTL = 10 * time.Minute

// MaxTTL 返回令牌允许的最大有效期(§6:建议≤10 分钟)。
func MaxTTL() time.Duration { return maxTTL }

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
func b64s(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }

func hmacSign(secret, signingInput string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signingInput))
	return b64(mac.Sum(nil))
}

// Generate 签发短期下载令牌。ttl 超过 10 分钟会被截断到上限(§6:建议≤10分钟)。
func Generate(secret, fileID, agentID string, ttl time.Duration) (string, error) {
	if secret == "" {
		return "", errors.New("token: empty secret")
	}
	if fileID == "" || agentID == "" {
		return "", errors.New("token: file_id and agent_id required")
	}
	if ttl <= 0 {
		ttl = time.Minute
	}
	if ttl > maxTTL {
		ttl = maxTTL
	}
	now := time.Now()
	claims := Claims{
		FileID:  fileID,
		AgentID: agentID,
		Iat:     now.Unix(),
		Exp:     now.Add(ttl).Unix(),
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("marshal claims: %w", err)
	}
	header := b64s(`{"alg":"HS256","typ":"JWT"}`)
	signingInput := header + "." + b64(payload)
	sig := hmacSign(secret, signingInput)
	return signingInput + "." + sig, nil
}

// Verify 校验令牌并返回 Claims。secret 必须与签发一致。
func Verify(secret, tokenStr string) (*Claims, error) {
	if secret == "" {
		return nil, errors.New("token: empty secret")
	}
	parts := strings.Split(tokenStr, ".")
	if len(parts) != 3 {
		return nil, ErrInvalid
	}
	signingInput := parts[0] + "." + parts[1]
	expected := hmacSign(secret, signingInput)
	if !hmac.Equal([]byte(expected), []byte(parts[2])) {
		return nil, ErrInvalid
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, ErrInvalid
	}
	var claims Claims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, ErrInvalid
	}
	now := time.Now().Unix()
	if claims.Exp <= now {
		return &claims, ErrExpired
	}
	if claims.Iat > now+60 { // 允许 60s 时钟漂移
		return &claims, ErrNotYet
	}
	return &claims, nil
}
