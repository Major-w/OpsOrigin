package auth

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"strings"
	"time"
)

// JWTToken 标准JWT Token结构
type JWTToken struct {
	Header    JWTHeader
	Payload   JWTPayload
	Signature []byte
}

// JWTHeader JWT头部
type JWTHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
}

// JWTPayload JWT载荷
type JWTPayload struct {
	Issuer    string `json:"iss,omitempty"`    // 签发者
	Subject   string `json:"sub,omitempty"`    // 主题
	Audience  string `json:"aud,omitempty"`    // 受众
	ExpiresAt int64  `json:"exp"`              // 过期时间
	NotBefore int64  `json:"nbf,omitempty"`    // 生效时间
	IssuedAt  int64  `json:"iat,omitempty"`    // 签发时间
	ID        string `json:"jti,omitempty"`    // Token ID
	UserID    string `json:"uid"`              // 用户ID
	TenantID  string `json:"tid"`              // 租户ID
	Role      string `json:"role"`             // 用户角色
}

// JWTManager JWT管理器
type JWTManager struct {
	secret     []byte
	privateKey *rsa.PrivateKey
	publicKey  *rsa.PublicKey
	algorithm  string
}

// NewJWTManager 创建JWT管理器
func NewJWTManager(secret string) *JWTManager {
	return &JWTManager{
		secret:    []byte(secret),
		algorithm: "HS256",
	}
}

// NewRSAJWTManager 创建RSA JWT管理器
func NewRSAJWTManager(privateKeyPEM []byte) (*JWTManager, error) {
	block, _ := pem.Decode(privateKeyPEM)
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM block")
	}

	privateKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key: %w", err)
	}

	return &JWTManager{
		privateKey: privateKey,
		publicKey:  &privateKey.PublicKey,
		algorithm:  "RS256",
	}, nil
}

// GenerateToken 生成JWT Token
func (m *JWTManager) GenerateToken(userID, tenantID, role string, ttl time.Duration) (string, error) {
	now := time.Now()
	claims := JWTPayload{
		Issuer:    "opsorigin",
		Subject:   userID,
		Audience:  "opsorigin-api",
		ExpiresAt: now.Add(ttl).Unix(),
		NotBefore: now.Unix(),
		IssuedAt:  now.Unix(),
		ID:        generateTokenID(),
		UserID:    userID,
		TenantID:  tenantID,
		Role:      role,
	}

	header := JWTHeader{
		Alg: m.algorithm,
		Typ: "JWT",
	}

	return m.signToken(header, claims)
}

// ValidateToken 验证JWT Token
func (m *JWTManager) ValidateToken(tokenString string) (*JWTPayload, error) {
	parts := strings.Split(tokenString, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid token format")
	}

	// 解码头部
	headerBytes, err := base64URLDecode(parts[0])
	if err != nil {
		return nil, fmt.Errorf("failed to decode header: %w", err)
	}
	var header JWTHeader
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return nil, fmt.Errorf("failed to unmarshal header: %w", err)
	}

	// 验证算法
	if header.Alg != m.algorithm {
		return nil, fmt.Errorf("unsupported algorithm: %s", header.Alg)
	}

	// 解码载荷
	payloadBytes, err := base64URLDecode(parts[1])
	if err != nil {
		return nil, fmt.Errorf("failed to decode payload: %w", err)
	}
	var payload JWTPayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return nil, fmt.Errorf("failed to unmarshal payload: %w", err)
	}

	// 验证签名
	signatureBytes, err := base64URLDecode(parts[2])
	if err != nil {
		return nil, fmt.Errorf("failed to decode signature: %w", err)
	}

	if err := m.verifySignature(parts[0]+"."+parts[1], signatureBytes); err != nil {
		return nil, fmt.Errorf("invalid signature: %w", err)
	}

	// 验证过期时间
	if time.Now().Unix() > payload.ExpiresAt {
		return nil, fmt.Errorf("token expired")
	}

	// 验证生效时间
	if time.Now().Unix() < payload.NotBefore {
		return nil, fmt.Errorf("token not yet valid")
	}

	return &payload, nil
}

// signToken 签名Token
func (m *JWTManager) signToken(header JWTHeader, payload JWTPayload) (string, error) {
	headerBytes, err := json.Marshal(header)
	if err != nil {
		return "", fmt.Errorf("failed to marshal header: %w", err)
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal payload: %w", err)
	}

	headerB64 := base64URLEncode(headerBytes)
	payloadB64 := base64URLEncode(payloadBytes)

	signingInput := headerB64 + "." + payloadB64

	var signature []byte
	if m.algorithm == "RS256" {
		signature, err = m.signRS256(signingInput)
	} else {
		signature, err = m.signHS256(signingInput)
	}
	if err != nil {
		return "", fmt.Errorf("failed to sign token: %w", err)
	}

	signatureB64 := base64URLEncode(signature)

	return signingInput + "." + signatureB64, nil
}

// signHS256 使用HMAC-SHA256签名
func (m *JWTManager) signHS256(data string) ([]byte, error) {
	h := sha256.New()
	h.Write([]byte(data))
	return h.Sum(nil), nil
}

// signRS256 使用RSA-SHA256签名
func (m *JWTManager) signRS256(data string) ([]byte, error) {
	h := sha256.New()
	h.Write([]byte(data))
	digest := h.Sum(nil)
	return rsa.SignPKCS1v15(rand.Reader, m.privateKey, crypto.SHA256, digest)
}

// verifySignature 验证签名
func (m *JWTManager) verifySignature(data string, signature []byte) error {
	if m.algorithm == "RS256" {
		return m.verifyRS256(data, signature)
	}
	return m.verifyHS256(data, signature)
}

// verifyHS256 验证HMAC-SHA256签名
func (m *JWTManager) verifyHS256(data string, signature []byte) error {
	expected, err := m.signHS256(data)
	if err != nil {
		return err
	}
	if !compareSignature(signature, expected) {
		return fmt.Errorf("signature mismatch")
	}
	return nil
}

// verifyRS256 验证RSA-SHA256签名
func (m *JWTManager) verifyRS256(data string, signature []byte) error {
	h := sha256.New()
	h.Write([]byte(data))
	digest := h.Sum(nil)
	return rsa.VerifyPKCS1v15(m.publicKey, crypto.SHA256, digest, signature)
}

// compareSignature 比较签名（常量时间比较）
func compareSignature(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// base64URLEncode Base64 URL编码
func base64URLEncode(data []byte) string {
	return base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(data)
}

// base64URLDecode Base64 URL解码
func base64URLDecode(s string) ([]byte, error) {
	// 添加填充
	switch len(s) % 4 {
	case 2:
		s += "=="
	case 3:
		s += "="
	}
	return base64.URLEncoding.DecodeString(s)
}

// generateTokenID 生成Token ID
func generateTokenID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}

// GenerateKeyPair 生成RSA密钥对
func GenerateKeyPair(bits int) (*rsa.PrivateKey, *rsa.PublicKey, error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate key pair: %w", err)
	}
	return privateKey, &privateKey.PublicKey, nil
}

// ExportPrivateKey 导出私钥为PEM格式
func ExportPrivateKey(key *rsa.PrivateKey) []byte {
	privKeyBytes := x509.MarshalPKCS1PrivateKey(key)
	privKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: privKeyBytes,
	})
	return privKeyPEM
}

// ExportPublicKey 导出公钥为PEM格式
func ExportPublicKey(key *rsa.PublicKey) ([]byte, error) {
	pubKeyBytes, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal public key: %w", err)
	}
	pubKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PUBLIC KEY",
		Bytes: pubKeyBytes,
	})
	return pubKeyPEM, nil
}

// GetPublicKey 获取公钥
func (m *JWTManager) GetPublicKey() *rsa.PublicKey {
	return m.publicKey
}

// GetAlgorithm 获取算法
func (m *JWTManager) GetAlgorithm() string {
	return m.algorithm
}

// bigIntToBytes 大整数转字节数组
func bigIntToBytes(n *big.Int) []byte {
	return n.Bytes()
}