package dialect

import (
	"context"
	"database/sql"
	"time"

	"github.com/pkg/errors"
	"github.com/rancher/k8s-sql"
	"github.com/rancher/k8s-sql/kv"
	"sync/atomic"
)

type Generic struct {
	CleanupSQL      string
	GetSQL          string
	ListSQL         string
	ListRevisionSQL string
	CreateSQL       string
	DeleteSQL       string
	UpdateSQL       string
	GetRevisionSQL  string
	revision        int64
}

func (g *Generic) Start(ctx context.Context, db *sql.DB) error {
	row := db.QueryRowContext(ctx, g.GetRevisionSQL)
	if err := row.Scan(&g.revision); err != nil {
		return errors.Wrap(err, "Failed to initialize revision")
	}

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Minute):
				db.ExecContext(ctx, g.CleanupSQL, time.Now().Second())
			}
		}
	}()

	return nil
}

func (g *Generic) Get(ctx context.Context, db *sql.DB, key string) (*kv.KeyValue, error) {
	value := kv.KeyValue{}
	row := db.QueryRowContext(ctx, g.GetSQL, key)

	err := scan(row.Scan, &value)
	if err == sql.ErrNoRows {
		return nil, nil
	}

	return &value, err
}

func (g *Generic) List(ctx context.Context, db *sql.DB, revision int64, key string) ([]*kv.KeyValue, error) {
	var (
		rows *sql.Rows
		err  error
	)

	if revision <= 0 {
		rows, err = db.QueryContext(ctx, g.ListSQL, key+"%")
	} else {
		rows, err = db.QueryContext(ctx, g.ListRevisionSQL, revision, key+"%")
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	resp := []*kv.KeyValue{}
	for rows.Next() {
		value := kv.KeyValue{}
		if err := scan(rows.Scan, &value); err != nil {
			return nil, err
		}
		resp = append(resp, &value)
	}

	return resp, nil
}

func (g *Generic) Create(ctx context.Context, db *sql.DB, key string, value []byte, ttl uint64) error {
	if ttl != 0 {
		ttl = uint64(time.Now().Second()) + ttl
	}
	newRev := atomic.AddInt64(&g.revision, 1)
	_, err := db.ExecContext(ctx, g.CreateSQL, key, []byte(value), newRev, ttl)
	return err
}

func (g *Generic) Delete(ctx context.Context, db *sql.DB, key string, revision *int64) (*kv.KeyValue, error) {
	value, err := g.Get(ctx, db, key)
	if err != nil {
		return nil, err
	}
	if value == nil || (revision != nil && value.Revision != *revision) {
		return nil, kv.ErrNotExists
	}

	result, err := db.ExecContext(ctx, g.DeleteSQL, key, value.Revision)
	if err != nil {
		return nil, err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}

	if rows == 0 {
		return nil, kv.ErrNotExists
	}

	return value, nil
}

func (g *Generic) Update(ctx context.Context, db *sql.DB, key string, value []byte, revision int64) (*kv.KeyValue, *kv.KeyValue, error) {
	oldKv, err := g.Get(ctx, db, key)
	if err != nil {
		return nil, nil, err
	}
	if oldKv == nil {
		return nil, nil, kv.ErrNotExists
	}

	if oldKv.Revision != revision {
		return nil, nil, rdbms.ErrRevisionMatch
	}

	newRevision := atomic.AddInt64(&g.revision, 1)
	result, err := db.ExecContext(ctx, g.UpdateSQL, value, newRevision, key, oldKv.Revision)
	if err != nil {
		return nil, nil, err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return nil, nil, err
	}
	if rows == 0 {
		return nil, nil, rdbms.ErrRevisionMatch
	}

	return oldKv, &kv.KeyValue{
		Key:      oldKv.Key,
		Value:    []byte(value),
		Revision: oldKv.Revision + 1,
	}, nil
}

type scanner func(dest ...interface{}) error

func scan(s scanner, out *kv.KeyValue) error {
	return s(&out.Key, &out.Value, &out.Revision)
}
