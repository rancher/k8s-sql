package kv

import (
	"errors"

	"golang.org/x/net/context"
)

var (
	ErrExists    = errors.New("Key exists")
	ErrNotExists = errors.New("Key and or Revision does not exists")
)

type Client interface {
	Get(ctx context.Context, key string) (*KeyValue, error)

	// Similar to get but looks for "like 'key%'"
	List(ctx context.Context, key string) ([]*KeyValue, error)

	// Should return ErrExists on conflict
	Create(ctx context.Context, key string, value []byte, ttl uint64) (*KeyValue, error)

	// Should return ErrNotExists on conflict
	Delete(ctx context.Context, key string) (*KeyValue, error)

	// Should return ErrNotExist
	DeleteVersion(ctx context.Context, key string, revision int64) error

	// Should return ErrNotExists, if key doesn't exist it should be created
	UpdateOrCreate(ctx context.Context, key string, value []byte, revision int64, ttl uint64) (*KeyValue, error)

	Watch(ctx context.Context, key string) ([]*KeyValue, WatchChan, error)
}

type WatchChan <-chan WatchResponse

type WatchResponse struct {
	Events []Event
	err    error
}

func WatchResponseError(err error) WatchResponse {
	return WatchResponse{
		err: err,
	}
}

func (wr *WatchResponse) Err() error {
	return wr.err
}

type Event struct {
	Create bool
	Delete bool
	Kv     *KeyValue
	PrevKv *KeyValue
}

type KeyValue struct {
	Key      string
	Value    []byte
	Revision int64
}
