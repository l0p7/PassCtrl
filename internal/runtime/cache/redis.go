package cache

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	valkey "github.com/valkey-io/valkey-go"
)

type RedisTLSConfig struct {
	Enabled bool
	CAFile  string
}

type RedisConfig struct {
	Address  string
	Username string
	Password string
	DB       int
	TLS      RedisTLSConfig
}

type redisCache struct {
	client valkey.Client
}

func NewRedis(cfg RedisConfig) (DecisionCache, error) {
	if cfg.Address == "" {
		return nil, errors.New("cache: redis address required")
	}

	option := valkey.ClientOption{
		InitAddress:       []string{cfg.Address},
		Username:          cfg.Username,
		Password:          cfg.Password,
		SelectDB:          cfg.DB,
		AlwaysRESP2:       true,
		ForceSingleClient: true,
		DisableCache:      true,
	}

	if cfg.TLS.Enabled {
		tlsConfig := &tls.Config{
			MinVersion: tls.VersionTLS12,
		}
		if cfg.TLS.CAFile != "" {
			caData, err := os.ReadFile(cfg.TLS.CAFile)
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return nil, fmt.Errorf("cache: read redis ca file: %w", err)
				}
				return nil, fmt.Errorf("cache: read redis ca file: %w", err)
			}
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(caData) {
				return nil, errors.New("cache: redis ca file contains no certificates")
			}
			tlsConfig.RootCAs = pool
		}
		option.TLSConfig = tlsConfig
	}

	client, err := valkey.NewClient(option)
	if err != nil {
		return nil, fmt.Errorf("cache: redis client: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Do(ctx, client.B().Ping().Build()).Error(); err != nil {
		client.Close()
		return nil, fmt.Errorf("cache: redis ping: %w", err)
	}

	return &redisCache{client: client}, nil
}

func (c *redisCache) Lookup(ctx context.Context, key string) (Entry, bool, error) {
	resp := c.client.Do(ctx, c.client.B().Get().Key(key).Build())
	if err := resp.Error(); err != nil {
		if errors.Is(err, valkey.Nil) {
			return Entry{}, false, nil
		}
		return Entry{}, false, fmt.Errorf("cache: redis get: %w", err)
	}
	payload, err := resp.AsBytes()
	if err != nil {
		return Entry{}, false, fmt.Errorf("cache: redis get bytes: %w", err)
	}
	var entry Entry
	if err := json.Unmarshal(payload, &entry); err != nil {
		return Entry{}, false, fmt.Errorf("cache: redis unmarshal: %w", err)
	}
	return entry, true, nil
}

func (c *redisCache) Store(ctx context.Context, key string, entry Entry) error {
	if entry.StoredAt.IsZero() {
		entry.StoredAt = time.Now().UTC()
	}
	if entry.ExpiresAt.IsZero() || entry.ExpiresAt.Before(entry.StoredAt) {
		return errors.New("cache: redis entry expiry required")
	}
	ttl := time.Until(entry.ExpiresAt)
	if ttl <= 0 {
		return nil
	}
	payload, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("cache: redis marshal: %w", err)
	}
	cmd := c.client.B().Set().Key(key).Value(string(payload)).Px(ttl).Build()
	if err := c.client.Do(ctx, cmd).Error(); err != nil {
		return fmt.Errorf("cache: redis set: %w", err)
	}
	return nil
}

func (c *redisCache) DeletePrefix(ctx context.Context, prefix string) error {
	if prefix == "" {
		return nil
	}

	// Use SCAN with MATCH to find keys with the given prefix
	// SCAN is cursor-based and non-blocking, safe for production use
	const (
		batchSize = 100  // Number of keys to scan per iteration
		delSize   = 50   // Number of keys to delete per DEL command
	)

	pattern := prefix + "*"
	cursor := uint64(0)
	totalDeleted := 0

	for {
		// Check context cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// SCAN returns cursor and matching keys
		cmd := c.client.B().Scan().Cursor(cursor).Match(pattern).Count(int64(batchSize)).Build()
		resp := c.client.Do(ctx, cmd)
		if err := resp.Error(); err != nil {
			return fmt.Errorf("cache: redis scan: %w", err)
		}

		// Parse scan response: [cursor, [key1, key2, ...]]
		scanResult, err := resp.AsScanEntry()
		if err != nil {
			return fmt.Errorf("cache: redis scan parse: %w", err)
		}

		// Delete keys in batches using UNLINK (non-blocking) instead of DEL
		// UNLINK is available in Redis 4.0+ and Valkey, performs async deletion
		keys := scanResult.Elements
		if len(keys) > 0 {
			for i := 0; i < len(keys); i += delSize {
				end := min(i+delSize, len(keys))
				batch := keys[i:end]

				// Use UNLINK for non-blocking deletion
				unlinkCmd := c.client.B().Unlink().Key(batch...).Build()
				if err := c.client.Do(ctx, unlinkCmd).Error(); err != nil {
					// Fall back to DEL if UNLINK not supported
					delCmd := c.client.B().Del().Key(batch...).Build()
					if err := c.client.Do(ctx, delCmd).Error(); err != nil {
						return fmt.Errorf("cache: redis delete keys: %w", err)
					}
				}
				totalDeleted += len(batch)
			}
		}

		// Update cursor for next iteration
		cursor = scanResult.Cursor

		// Cursor = 0 means scan is complete
		if cursor == 0 {
			break
		}
	}

	return nil
}

func (c *redisCache) Size(ctx context.Context) (int64, error) {
	resp := c.client.Do(ctx, c.client.B().Dbsize().Build())
	size, err := resp.ToInt64()
	if err != nil {
		return 0, fmt.Errorf("cache: redis dbsize: %w", err)
	}
	return size, nil
}

func (c *redisCache) Close(context.Context) error {
	c.client.Close()
	return nil
}

func (c *redisCache) InvalidateOnReload(ctx context.Context, scope ReloadScope) error {
	return c.DeletePrefix(ctx, scope.Prefix)
}
