package rdbms

import (
	"context"
	"database/sql"
	"errors"

	"github.com/rancher/k8s-sql/kv"
)

var (
	ErrRevisionMatch = errors.New("Revision does not match")
	dialects         = map[string]dialect{}
)

func Register(name string, d dialect) {
	dialects[name] = d
}

type dialect interface {
	Get(ctx context.Context, db *sql.DB, key string) (*kv.KeyValue, error)

	List(ctx context.Context, db *sql.DB, key string) ([]*kv.KeyValue, error)

	Create(ctx context.Context, db *sql.DB, key string, value []byte, ttl uint64) error

	Delete(ctx context.Context, db *sql.DB, key string, revision *int64) (*kv.KeyValue, error)

	// Update should return ErrNotExist when the key does not exist and ErrRevisionMatch when revision doesn't match
	Update(ctx context.Context, db *sql.DB, key string, value []byte, revision int64) (oldKv *kv.KeyValue, newKv *kv.KeyValue, err error)
}
