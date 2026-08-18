// Package objectstore 是 S3 协议兼容对象存储的薄封装(自建 MinIO / 腾讯云COS / 阿里OSS / 亚马逊S3 通用)。
// 职责: 桶自动创建 + Put/Get/Remove 三个原子操作, 供 ITSM 附件等二进制从 DB 迁出使用。
package objectstore

import (
	"context"
	"io"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Client 包装 minio.Client, 绑定单个 bucket。
type Client struct {
	mc     *minio.Client
	bucket string
}

// New 创建对象存储客户端并确保桶存在(幂等)。
func New(endpoint, accessKey, secretKey, bucket, region string, useSSL bool) (*Client, error) {
	mc, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
		Region: region,
	})
	if err != nil {
		return nil, err
	}
	exists, err := mc.BucketExists(context.Background(), bucket)
	if err != nil {
		return nil, err
	}
	if !exists {
		if err := mc.MakeBucket(context.Background(), bucket, minio.MakeBucketOptions{Region: region}); err != nil {
			return nil, err
		}
	}
	return &Client{mc: mc, bucket: bucket}, nil
}

// Bucket 返回当前桶名(调试用)。
func (c *Client) Bucket() string { return c.bucket }

// Health 检查桶可访问性(系统自监控探活用)。
func (c *Client) Health(ctx context.Context) error {
	_, err := c.mc.BucketExists(ctx, c.bucket)
	return err
}

// Put 上传对象; key 建议带业务前缀(如 itsm/<ticket>/<uuid>-<name>), size 传 -1 由 SDK 分片自动处理。
func (c *Client) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	_, err := c.mc.PutObject(ctx, c.bucket, key, r, size, minio.PutObjectOptions{ContentType: contentType})
	return err
}

// Get 将对象内容写入 w。
func (c *Client) Get(ctx context.Context, w io.Writer, key string) error {
	obj, err := c.mc.GetObject(ctx, c.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return err
	}
	defer obj.Close()
	_, err = io.Copy(w, obj)
	return err
}

// Remove 删除对象。
func (c *Client) Remove(ctx context.Context, key string) error {
	return c.mc.RemoveObject(ctx, c.bucket, key, minio.RemoveObjectOptions{})
}
