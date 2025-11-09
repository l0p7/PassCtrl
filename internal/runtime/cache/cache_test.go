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

	require.NoError(t, cache.Close(ctx))
}

func TestRedisCacheDeletePrefix(t *testing.T) {
	server, err := miniredis.Run()
	require.NoError(t, err)
	defer server.Close()

	cache, err := NewRedis(RedisConfig{Address: server.Addr()})
	require.NoError(t, err)
	defer cache.Close(context.Background())

	ctx := context.Background()
	now := time.Now().UTC()

	// Create multiple cache entries with different prefixes
	entries := map[string]Entry{
		"epoch:1:user:alice": {Decision: "pass", Response: Response{Status: 200}, StoredAt: now, ExpiresAt: now.Add(time.Hour)},
		"epoch:1:user:bob":   {Decision: "pass", Response: Response{Status: 200}, StoredAt: now, ExpiresAt: now.Add(time.Hour)},
		"epoch:1:admin:jane": {Decision: "pass", Response: Response{Status: 200}, StoredAt: now, ExpiresAt: now.Add(time.Hour)},
		"epoch:2:user:alice": {Decision: "fail", Response: Response{Status: 403}, StoredAt: now, ExpiresAt: now.Add(time.Hour)},
		"epoch:2:user:bob":   {Decision: "fail", Response: Response{Status: 403}, StoredAt: now, ExpiresAt: now.Add(time.Hour)},
		"other:key":          {Decision: "pass", Response: Response{Status: 200}, StoredAt: now, ExpiresAt: now.Add(time.Hour)},
	}

	// Store all entries
	for key, entry := range entries {
		require.NoError(t, cache.Store(ctx, key, entry), "failed to store key: %s", key)
	}

	// Verify all entries exist
	for key := range entries {
		_, ok, err := cache.Lookup(ctx, key)
		require.NoError(t, err)
		require.True(t, ok, "key should exist: %s", key)
	}

	// Delete all epoch:1: entries
	require.NoError(t, cache.DeletePrefix(ctx, "epoch:1:"))

	// Verify epoch:1: entries are deleted
	for key := range entries {
		_, ok, err := cache.Lookup(ctx, key)
		require.NoError(t, err)
		if key[:8] == "epoch:1:" {
			require.False(t, ok, "epoch:1: key should be deleted: %s", key)
		} else {
			require.True(t, ok, "non-epoch:1: key should still exist: %s", key)
		}
	}

	// Verify epoch:2: and other: entries still exist
	_, ok, err := cache.Lookup(ctx, "epoch:2:user:alice")
	require.NoError(t, err)
	require.True(t, ok)

	_, ok, err = cache.Lookup(ctx, "other:key")
	require.NoError(t, err)
	require.True(t, ok)
}

func TestRedisCacheInvalidateOnReload(t *testing.T) {
	server, err := miniredis.Run()
	require.NoError(t, err)
	defer server.Close()

	cache, err := NewRedis(RedisConfig{Address: server.Addr()})
	require.NoError(t, err)
	defer cache.Close(context.Background())

	ctx := context.Background()
	now := time.Now().UTC()

	// Create cache entries with namespace prefix
	entry := Entry{Decision: "pass", Response: Response{Status: 200}, StoredAt: now, ExpiresAt: now.Add(time.Hour)}
	require.NoError(t, cache.Store(ctx, "namespace:epoch:1:key1", entry))
	require.NoError(t, cache.Store(ctx, "namespace:epoch:1:key2", entry))
	require.NoError(t, cache.Store(ctx, "other:key", entry))

	// Verify entries exist
	_, ok, err := cache.Lookup(ctx, "namespace:epoch:1:key1")
	require.NoError(t, err)
	require.True(t, ok)

	// Invalidate using ReloadInvalidator interface
	invalidator, ok := cache.(ReloadInvalidator)
	require.True(t, ok, "expected Redis cache to implement ReloadInvalidator")
	require.NoError(t, invalidator.InvalidateOnReload(ctx, ReloadScope{
		Namespace: "namespace",
		Epoch:     1,
		Prefix:    "namespace:epoch:1:",
	}))

	// Verify namespace:epoch:1: entries are deleted
	_, ok, err = cache.Lookup(ctx, "namespace:epoch:1:key1")
	require.NoError(t, err)
	require.False(t, ok)

	_, ok, err = cache.Lookup(ctx, "namespace:epoch:1:key2")
	require.NoError(t, err)
	require.False(t, ok)

	// Verify other: entry still exists
	_, ok, err = cache.Lookup(ctx, "other:key")
	require.NoError(t, err)
	require.True(t, ok)
}
