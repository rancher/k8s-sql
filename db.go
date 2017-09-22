package rdbms

import (
	"context"
	"database/sql"
	"sync"

	"github.com/pkg/errors"
	"github.com/rancher/k8s-sql/kv"
	"k8s.io/apiserver/pkg/storage"
	"k8s.io/apiserver/pkg/storage/storagebackend"
	"k8s.io/apiserver/pkg/storage/storagebackend/factory"
	"k8s.io/apiserver/pkg/storage/value"
)

var (
	ErrNoDSN = errors.New("DB DSN must be set as ServerList")
	// Just assume there is only one for now
	globalClient     kv.Client
	globalClientLock sync.Mutex
)

func NewRDBMSStorage(c storagebackend.Config) (storage.Interface, factory.DestroyFunc, error) {
	if len(c.ServerList) != 2 {
		return nil, nil, ErrNoDSN
	}

	driverName, dsn := c.ServerList[0], c.ServerList[1]

	dbClient, err := getClient(driverName, dsn)
	if err != nil {
		return nil, nil, err
	}

	transformer := c.Transformer
	if transformer == nil {
		transformer = value.NewMutableTransformer(value.IdentityTransformer)
	}

	return kv.New(dbClient, c.Codec, c.Prefix, transformer), func() {}, nil
}

func getClient(driverName, dsn string) (kv.Client, error) {
	globalClientLock.Lock()
	defer globalClientLock.Unlock()
	if globalClient != nil {
		return globalClient, nil
	}

	// Notice that we never close the DB connection or watcher (because this code assumes only one DB)
	// "Room for improvement"
	db, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, errors.Wrapf(err, "Failed to create DB(%s) connection", driverName)
	}

	dbClient, err := newClient(context.Background(), driverName, db)
	if err != nil {
		return nil, err
	}

	globalClient = dbClient
	return globalClient, nil
}
