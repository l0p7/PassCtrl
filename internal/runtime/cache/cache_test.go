package cache

import (
	"context"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
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

	if err := cache.Store(ctx, "token", entry); err != nil {
		t.Fatalf("store: %v", err)
	}

	got, ok, err := cache.Lookup(ctx, "token")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if !ok {
		t.Fatalf("expected cache hit")
	}
	if got.Decision != "pass" || got.Response.Status != 200 {
		t.Fatalf("unexpected entry: %#v", got)
	}

	size, err := cache.Size(ctx)
	if err != nil {
		t.Fatalf("size: %v", err)
	}
	if size != 1 {
		t.Fatalf("expected size 1, got %d", size)
	}

	if err := cache.DeletePrefix(ctx, "tok"); err != nil {
		t.Fatalf("delete prefix: %v", err)
	}
	_, ok, err = cache.Lookup(ctx, "token")
	if err != nil {
		t.Fatalf("lookup after delete: %v", err)
	}
	if ok {
		t.Fatalf("expected delete to remove key")
	}

	if err := cache.Close(ctx); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func TestMemoryCacheExpiry(t *testing.T) {
	cache := NewMemory(10 * time.Millisecond)
	ctx := context.Background()

	entry := Entry{Decision: "fail", Response: Response{Status: 403}, StoredAt: time.Now().UTC()}
	entry.ExpiresAt = entry.StoredAt.Add(10 * time.Millisecond)
	if err := cache.Store(ctx, "key", entry); err != nil {
		t.Fatalf("store: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	_, ok, err := cache.Lookup(ctx, "key")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if ok {
		t.Fatalf("expected entry to expire")
	}
}

func TestMemoryCacheInvalidateOnReload(t *testing.T) {
	cache := NewMemory(1 * time.Minute)
	ctx := context.Background()

	entry := Entry{Decision: "pass", Response: Response{Status: 200}}
	entry.StoredAt = time.Now().UTC()
	entry.ExpiresAt = entry.StoredAt.Add(1 * time.Minute)
	if err := cache.Store(ctx, "namespace:key", entry); err != nil {
		t.Fatalf("store: %v", err)
	}

	invalidator, ok := cache.(ReloadInvalidator)
	if !ok {
		t.Fatalf("expected memory cache to implement ReloadInvalidator")
	}
	if err := invalidator.InvalidateOnReload(ctx, ReloadScope{Prefix: "namespace:"}); err != nil {
		t.Fatalf("invalidate on reload: %v", err)
	}
	_, ok, err := cache.Lookup(ctx, "namespace:key")
	if err != nil {
		t.Fatalf("lookup after invalidate: %v", err)
	}
	if ok {
		t.Fatalf("expected entry to be removed after invalidate")
	}
}

func TestRedisCacheStoreLookup(t *testing.T) {
	server, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	defer server.Close()

	cache, err := NewRedis(RedisConfig{Address: server.Addr()})
	if err != nil {
		t.Fatalf("new redis: %v", err)
	}
	ctx := context.Background()
	entry := Entry{
		Decision: "pass",
		Response: Response{Status: 200, Message: "allowed", Headers: map[string]string{"x-cache": "redis"}},
		StoredAt: time.Now().UTC(),
	}
	entry.ExpiresAt = entry.StoredAt.Add(500 * time.Millisecond)

	if err := cache.Store(ctx, "redis:key", entry); err != nil {
		t.Fatalf("store: %v", err)
	}
	got, ok, err := cache.Lookup(ctx, "redis:key")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if !ok {
		t.Fatalf("expected redis cache hit")
	}
	if got.Decision != entry.Decision || got.Response.Headers["x-cache"] != "redis" {
		t.Fatalf("unexpected entry: %#v", got)
	}

	server.FastForward(time.Second)
	_, ok, err = cache.Lookup(ctx, "redis:key")
	if err != nil {
		t.Fatalf("lookup after ttl: %v", err)
	}
	if ok {
		t.Fatalf("expected redis entry to expire")
	}

	if size, err := cache.Size(ctx); err != nil {
		t.Fatalf("size: %v", err)
	} else if size != 0 {
		t.Fatalf("expected size to reflect expired entries being gone, got %d", size)
	}

	if rcache, ok := cache.(*redisCache); ok {
		if err := rcache.DeletePrefix(ctx, "redis:"); err != nil {
			t.Fatalf("delete prefix: %v", err)
		}
		if err := rcache.InvalidateOnReload(ctx, ReloadScope{Prefix: "redis:"}); err != nil {
			t.Fatalf("invalidate on reload: %v", err)
		}
	}

	if err := cache.Close(ctx); err != nil {
		t.Fatalf("close: %v", err)
	}
}
