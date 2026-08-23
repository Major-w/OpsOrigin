package cloud

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
)

// PGStore 基于 PostgreSQL 的云凭证持久化存储（生产默认）。
// 与 MemStore 行为对齐（idOf 稳定 ID、更新保留旧 Region），区别在于：
//   - SecretKey 使用 AES-GCM 加密落库（密钥来自 OPSORIGIN_CRED_ENCRYPT_KEY，
//     未设置时回退内置开发密钥，单租户本地形态）；空 SecretKey 更新时保留旧值。
//   - 服务重启后凭证不丢失，云采集任务可持续拉取资源（修复 MemStore 重启即失）。
type PGStore struct {
	db   *sql.DB
	aead cipher.AEAD
}

// NewPGStore 构造 PG 凭证存储。db 不可为 nil。
func NewPGStore(db *sql.DB) *PGStore {
	key := os.Getenv("OPSORIGIN_CRED_ENCRYPT_KEY")
	if key == "" {
		key = "opsorigin-dev-cred-key"
	}
	kh := sha256.Sum256([]byte(key))
	block, err := aes.NewCipher(kh[:])
	if err != nil {
		return &PGStore{db: db}
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return &PGStore{db: db}
	}
	return &PGStore{db: db, aead: aead}
}

func (s *PGStore) encrypt(plain string) string {
	if plain == "" || s.aead == nil {
		return plain
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return plain
	}
	ct := s.aead.Seal(nonce, nonce, []byte(plain), nil)
	return base64.StdEncoding.EncodeToString(ct)
}

func (s *PGStore) decrypt(enc string) string {
	if enc == "" || s.aead == nil {
		return enc
	}
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return enc
	}
	if len(raw) < s.aead.NonceSize() {
		return enc
	}
	nonce, ct := raw[:s.aead.NonceSize()], raw[s.aead.NonceSize():]
	pt, err := s.aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return enc
	}
	return string(pt)
}

func (s *PGStore) Save(c Credential) error {
	id := idOf(c)
	// 更新场景：SecretKey 留空表示不修改（保留旧值）
	secret := s.encrypt(c.SecretKey)
	region := c.Region
	extraJSON, err := json.Marshal(c.Extra)
	if err != nil {
		return fmt.Errorf("marshal credential extra: %w", err)
	}
	// 读取既有行以合并 Region / 保留旧 SecretKey
	var oldRegion, oldSecret string
	err = s.db.QueryRow(`SELECT region, secret_key FROM cloud_credentials WHERE provider=$1 AND id=$2`, c.Provider, id).Scan(&oldRegion, &oldSecret)
	if err == nil {
		if region == "" {
			region = oldRegion
		}
		if c.SecretKey == "" {
			secret = oldSecret
		}
	}
	_, err = s.db.Exec(`
		INSERT INTO cloud_credentials (provider, id, region, access_key, secret_key, extra, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6, NOW())
		ON CONFLICT (provider, id) DO UPDATE SET
			region=EXCLUDED.region, access_key=EXCLUDED.access_key,
			secret_key=EXCLUDED.secret_key, extra=EXCLUDED.extra, updated_at=NOW()`,
		c.Provider, id, region, c.AccessKey, secret, extraJSON)
	if err != nil {
		return fmt.Errorf("save credential: %w", err)
	}
	return nil
}

func (s *PGStore) Get(provider, id string) (*Credential, error) {
	row := s.db.QueryRow(`
		SELECT provider, id, region, access_key, secret_key, extra
		FROM cloud_credentials WHERE provider=$1 AND id=$2`, provider, id)
	var c Credential
	var rowID string
	var extraJSON []byte
	if err := row.Scan(&c.Provider, &rowID, &c.Region, &c.AccessKey, &c.SecretKey, &extraJSON); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get credential: %w", err)
	}
	c.SecretKey = s.decrypt(c.SecretKey)
	if len(extraJSON) > 0 {
		_ = json.Unmarshal(extraJSON, &c.Extra)
	}
	return &c, nil
}

func (s *PGStore) List(provider string) ([]Credential, error) {
	rows, err := s.db.Query(`
		SELECT provider, id, region, access_key, secret_key, extra
		FROM cloud_credentials WHERE provider=$1 ORDER BY created_at`, provider)
	if err != nil {
		return nil, fmt.Errorf("list credentials: %w", err)
	}
	defer rows.Close()
	var out []Credential
	for rows.Next() {
		var c Credential
		var rowID string
		var extraJSON []byte
		if err := rows.Scan(&c.Provider, &rowID, &c.Region, &c.AccessKey, &c.SecretKey, &extraJSON); err != nil {
			return nil, fmt.Errorf("scan credential: %w", err)
		}
		c.SecretKey = s.decrypt(c.SecretKey)
		if len(extraJSON) > 0 {
			_ = json.Unmarshal(extraJSON, &c.Extra)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list credentials rows: %w", err)
	}
	return out, nil
}

func (s *PGStore) Delete(provider, id string) error {
	_, err := s.db.Exec(`DELETE FROM cloud_credentials WHERE provider=$1 AND id=$2`, provider, id)
	if err != nil {
		return fmt.Errorf("delete credential: %w", err)
	}
	return nil
}
