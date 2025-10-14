package cache

import (
	"context"
	"strings"
	"sync"
	"time"
)

type memoryCache struct {
	ttl time.Duration

	mu      sync.RWMutex
	entries map[string]Entry
}

func NewMemory(ttl time.Duration) DecisionCache {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	return &memoryCache{ttl: ttl, entries: make(map[string]Entry)}
}

func (c *memoryCache) Lookup(_ context.Context, key string) (Entry, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return Entry{}, false, nil
	}
	if time.Now().After(entry.ExpiresAt) {
		delete(c.entries, key)
		return Entry{}, false, nil
	}
	return cloneEntry(entry), true, nil
}

func (c *memoryCache) Store(_ context.Context, key string, entry Entry) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry.StoredAt.IsZero() {
		entry.StoredAt = time.Now().UTC()
	}
	if entry.ExpiresAt.IsZero() || entry.ExpiresAt.Before(entry.StoredAt) {
		entry.ExpiresAt = entry.StoredAt.Add(c.ttl)
	}
	c.entries[key] = cloneEntry(entry)
	return nil
}

func (c *memoryCache) DeletePrefix(_ context.Context, prefix string) error {
	if prefix == "" {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for key := range c.entries {
		if strings.HasPrefix(key, prefix) {
			delete(c.entries, key)
		}
	}
	return nil
}

func (c *memoryCache) Size(_ context.Context) (int64, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return int64(len(c.entries)), nil
}

func (c *memoryCache) Close(_ context.Context) error {
	return nil
}

func (c *memoryCache) InvalidateOnReload(ctx context.Context, scope ReloadScope) error {
	if scope.Prefix == "" {
		return nil
	}
	return c.DeletePrefix(ctx, scope.Prefix)
}

func cloneEntry(in Entry) Entry {
	out := Entry{
		Decision:  in.Decision,
		Response:  Response{Status: in.Response.Status, Message: in.Response.Message},
		StoredAt:  in.StoredAt,
		ExpiresAt: in.ExpiresAt,
	}
	if len(in.Response.Headers) > 0 {
		out.Response.Headers = make(map[string]string, len(in.Response.Headers))
		for k, v := range in.Response.Headers {
			out.Response.Headers[k] = v
		}
	}
	return out
}
