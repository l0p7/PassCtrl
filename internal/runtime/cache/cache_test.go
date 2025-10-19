package cache

import (
	"context"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/require"
)

func TestMemoryCacheStoreLookup(t *testing.T) {
	cache := NewMemory(500 * time.Millisecond)
	ctx := context.Background()

	entry := Entry{
		Decision: "pass",
		Response: Response{Status: 200, Message: "ok"},
		StoredAt: time.Now().UTC(),
	}
	entry.ExpiresAt = entry.StoredAt.Add(500 * time.Millisecond)

	require.NoError(t, cache.Store(ctx, "token", entry))

	got, ok, err := cache.Lookup(ctx, "token")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "pass", got.Decision)
	require.Equal(t, 200, got.Response.Status)

	size, err := cache.Size(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), size)

	require.NoError(t, cache.DeletePrefix(ctx, "tok"))
	_, ok, err = cache.Lookup(ctx, "token")
	require.NoError(t, err)
	require.False(t, ok)

	require.NoError(t, cache.Close(ctx))
}

func TestMemoryCacheExpiry(t *testing.T) {
	cache := NewMemory(10 * time.Millisecond)
	ctx := context.Background()

	entry := Entry{Decision: "fail", Response: Response{Status: 403}, StoredAt: time.Now().UTC()}
	entry.ExpiresAt = entry.StoredAt.Add(10 * time.Millisecond)
	require.NoError(t, cache.Store(ctx, "key", entry))

	time.Sleep(20 * time.Millisecond)
	_, ok, err := cache.Lookup(ctx, "key")
	require.NoError(t, err)
	require.False(t, ok)
}

func TestMemoryCacheInvalidateOnReload(t *testing.T) {
	cache := NewMemory(1 * time.Minute)
	ctx := context.Background()

	entry := Entry{Decision: "pass", Response: Response{Status: 200}}
	entry.StoredAt = time.Now().UTC()
	entry.ExpiresAt = entry.StoredAt.Add(1 * time.Minute)
	require.NoError(t, cache.Store(ctx, "namespace:key", entry))

	invalidator, ok := cache.(ReloadInvalidator)
	require.True(t, ok, "expected memory cache to implement ReloadInvalidator")
	require.NoError(t, invalidator.InvalidateOnReload(ctx, ReloadScope{Prefix: "namespace:"}))

	_, ok, err := cache.Lookup(ctx, "namespace:key")
	require.NoError(t, err)
	require.False(t, ok)
}

func TestRedisCacheStoreLookup(t *testing.T) {
	server, err := miniredis.Run()
	require.NoError(t, err)
	defer server.Close()

	cache, err := NewRedis(RedisConfig{Address: server.Addr()})
	require.NoError(t, err)
	ctx := context.Background()
	entry := Entry{
		Decision: "pass",
		Response: Response{Status: 200, Message: "allowed", Headers: map[string]string{"x-cache": "redis"}},
		StoredAt: time.Now().UTC(),
	}
	entry.ExpiresAt = entry.StoredAt.Add(500 * time.Millisecond)

	require.NoError(t, cache.Store(ctx, "redis:key", entry))
	got, ok, err := cache.Lookup(ctx, "redis:key")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, entry.Decision, got.Decision)
	require.Equal(t, "redis", got.Response.Headers["x-cache"])

	server.FastForward(time.Second)
	_, ok, err = cache.Lookup(ctx, "redis:key")
	require.NoError(t, err)
	require.False(t, ok)

	size, err := cache.Size(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(0), size)

	if rcache, ok := cache.(*redisCache); ok {
		require.NoError(t, rcache.DeletePrefix(ctx, "redis:"))
		require.NoError(t, rcache.InvalidateOnReload(ctx, ReloadScope{Prefix: "redis:"}))
	}

	require.NoError(t, cache.Close(ctx))
}
