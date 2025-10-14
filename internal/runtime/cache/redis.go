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
		tlsConfig := &tls.Config{}
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

func (c *redisCache) DeletePrefix(context.Context, string) error {
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
