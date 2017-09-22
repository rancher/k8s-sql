package rdbms

import (
	"database/sql"
	"fmt"
	"sync"

	"github.com/rancher/k8s-sql/kv"
	"golang.org/x/net/context"
)

const chanSize = 1000

type watchChan chan kv.WatchResponse
type scanner func(dest ...interface{}) error

func newClient(ctx context.Context, dialectName string, db *sql.DB) (kv.Client, error) {
	dialect, ok := dialects[dialectName]
	if !ok {
		return nil, fmt.Errorf("Failed to find dialect %v", dialectName)
	}

	client := &client{
		db:       db,
		dialect:  dialect,
		events:   make(chan kv.Event, chanSize),
		watchers: map[string][]watchChan{},
	}
	go client.watchEvents(ctx)

	return client, nil
}

type client struct {
	sync.Mutex
	db       *sql.DB
	dialect  dialect
	events   chan kv.Event
	watchers map[string][]watchChan
}

func (c *client) Get(ctx context.Context, key string) (*kv.KeyValue, error) {
	return c.dialect.Get(ctx, c.db, key)
}

func (c *client) List(ctx context.Context, key string) ([]*kv.KeyValue, error) {
	return c.dialect.List(ctx, c.db, key)
}

func (c *client) Create(ctx context.Context, key string, value []byte, ttl uint64) (*kv.KeyValue, error) {
	err := c.dialect.Create(ctx, c.db, key, value, ttl)
	// TODO: Check for specific error? Don't just assume the key is taken
	if err != nil {
		return nil, kv.ErrExists
	}

	result := &kv.KeyValue{
		Key:      key,
		Value:    value,
		Revision: 1,
	}
	c.created(result)
	return result, nil
}

func (c *client) Delete(ctx context.Context, key string) (*kv.KeyValue, error) {
	return c.deleteVersion(ctx, key, nil)
}

func (c *client) DeleteVersion(ctx context.Context, key string, revision int64) error {
	_, err := c.deleteVersion(ctx, key, &revision)
	return err
}

func (c *client) deleteVersion(ctx context.Context, key string, revision *int64) (*kv.KeyValue, error) {
	value, err := c.dialect.Delete(ctx, c.db, key, revision)
	if err != nil {
		return nil, err
	}
	c.deleted(value)
	return value, nil
}

func (c *client) UpdateOrCreate(ctx context.Context, key string, value []byte, revision int64, ttl uint64) (*kv.KeyValue, error) {
	oldKv, newKv, err := c.dialect.Update(ctx, c.db, key, value, revision)
	if err == ErrRevisionMatch {
		return nil, kv.ErrNotExists
	} else if err == kv.ErrNotExists {
		return c.Create(ctx, key, value, 0)
	} else if err != nil {
		return nil, err
	}

	c.updated(oldKv, newKv)
	return newKv, nil
}
