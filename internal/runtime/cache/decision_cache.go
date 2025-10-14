package cache

import (
	"context"
	"time"
)

type Response struct {
	Status  int               `json:"status"`
	Message string            `json:"message"`
	Headers map[string]string `json:"headers,omitempty"`
}

type Entry struct {
	Decision  string    `json:"decision"`
	Response  Response  `json:"response"`
	StoredAt  time.Time `json:"storedAt"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type DecisionCache interface {
	Lookup(ctx context.Context, key string) (Entry, bool, error)
	Store(ctx context.Context, key string, entry Entry) error
	DeletePrefix(ctx context.Context, prefix string) error
	Size(ctx context.Context) (int64, error)
	Close(ctx context.Context) error
}

// ReloadScope conveys the namespace and prefix of cache entries that should be
// invalidated when a pipeline reload occurs.
type ReloadScope struct {
	Namespace string
	Epoch     int
	Prefix    string
}

// ReloadInvalidator is implemented by cache backends that require additional
// coordination when the pipeline swaps configuration snapshots.
type ReloadInvalidator interface {
	InvalidateOnReload(ctx context.Context, scope ReloadScope) error
}
