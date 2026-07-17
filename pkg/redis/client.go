package redis

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

type Client struct {
	client *redis.Client
}

func NewClient(addr string) (*Client, error) {
	if addr == "" {
		return nil, fmt.Errorf("redis addr is empty")
	}

	opts := &redis.Options{
		Addr:            addr,
		DialTimeout:     2 * time.Second,
		ReadTimeout:     2 * time.Second,
		WriteTimeout:    2 * time.Second,
		PoolSize:        5,
		MinIdleConns:    1,
		MaxRetries:      1,
		MinRetryBackoff: 100 * time.Millisecond,
		MaxRetryBackoff: 300 * time.Millisecond,
	}

	rdb := redis.NewClient(opts)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis ping: %w", err)
	}

	slog.Info("redis connected", "addr", addr)
	return &Client{client: rdb}, nil
}

func (c *Client) Close() error {
	return c.client.Close()
}

func (c *Client) Conn() *redis.Client {
	return c.client
}

func (c *Client) StreamExists(ctx context.Context, stream string) (bool, error) {
	exists, err := c.client.Exists(ctx, stream).Result()
	if err != nil {
		return false, err
	}
	return exists > 0, nil
}

func (c *Client) CreateConsumerGroup(ctx context.Context, stream, group string) error {
	err := c.client.XGroupCreate(ctx, stream, group, "0").Err()
	if err != nil {
		if isBusyGroupError(err) {
			slog.Debug("consumer group already exists", "group", group, "stream", stream)
			return nil
		}
		return fmt.Errorf("xgroup create: %w", err)
	}
	slog.Info("consumer group created", "group", group, "stream", stream)
	return nil
}

func (c *Client) ReadStream(ctx context.Context, stream, group, consumer string, count int, block time.Duration) ([]redis.XStream, error) {
	result, err := c.client.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    group,
		Consumer: consumer,
		Streams:  []string{stream, ">"},
		Count:    int64(count),
		Block:    block,
		NoAck:    false,
	}).Result()
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (c *Client) Ack(ctx context.Context, stream, group string, ids ...string) error {
	return c.client.XAck(ctx, stream, group, ids...).Err()
}

func isBusyGroupError(err error) bool {
	return err != nil && (err.Error() == "BUSYGROUP Consumer Group name already exists" ||
		err.Error() == "BUSYGROUP" ||
		strings.Contains(err.Error(), "BUSYGROUP"))
}
