package aws

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// sha256hex 返回输入内容的十六进制 SHA256 摘要。
func sha256hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// hexhmac 使用 key 对 msg 做 HMAC-SHA256，返回十六进制结果。
func hexhmac(key []byte, msg string) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(msg))
	return hex.EncodeToString(mac.Sum(nil))
}

// getSignatureKey 派生 SigV4 签名密钥：
// AWS4 + secretKey → date → region → service → "aws4_request"。
func getSignatureKey(secretKey, dateStamp, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secretKey), dateStamp)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, service)
	kSigning := hmacSHA256(kService, "aws4_request")
	return kSigning
}

// hmacSHA256 使用 key 对 msg 做 HMAC-SHA256，返回原始字节。
func hmacSHA256(key []byte, msg string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(msg))
	return mac.Sum(nil)
}
