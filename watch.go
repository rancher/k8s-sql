package rdbms

import (
	"io"
	"strings"

	"github.com/rancher/k8s-sql/kv"
	"golang.org/x/net/context"
)

func (c *client) Watch(ctx context.Context, key string) ([]*kv.KeyValue, kv.WatchChan, error) {
	watcher := c.createWatcher(ctx, key)
	listResp, err := c.List(ctx, key)
	return listResp, kv.WatchChan(watcher), err
}

func (c *client) watchEvents(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			c.closeWatchers()
			return
		case event := <-c.events:
			c.handleEvent(event)
		}
	}
}

func (c *client) closeWatchers() {
	c.Lock()
	defer c.Unlock()

	for _, watchers := range c.watchers {
		for _, watcher := range watchers {
			watcher <- kv.WatchResponseError(io.EOF)
		}
	}
}

func (c *client) handleEvent(event kv.Event) {
	var watchers []watchChan
	c.Lock()
	for k, v := range c.watchers {
		if strings.HasPrefix(event.Kv.Key, k) {
			watchers = append(watchers, v...)
		}
	}
	c.Unlock()

	for _, watcher := range watchers {
		watcher <- kv.WatchResponse{
			Events: []kv.Event{event},
		}
	}
}

func (c *client) createWatcher(ctx context.Context, key string) chan kv.WatchResponse {
	c.Lock()
	defer c.Unlock()

	watcher := make(watchChan, chanSize)
	c.watchers[key] = append(c.watchers[key], watcher)

	go func() {
		<-ctx.Done()
		c.removeWatcher(key, watcher)
	}()

	return watcher
}

func (c *client) removeWatcher(key string, watcher watchChan) {
	c.Lock()
	defer c.Unlock()

	var newList []watchChan
	for _, i := range c.watchers[key] {
		if i != watcher {
			newList = append(newList, i)
		}
	}
	if len(newList) == 0 {
		delete(c.watchers, key)
	} else {
		c.watchers[key] = newList
	}
}

func (c *client) created(val *kv.KeyValue) {
	c.events <- kv.Event{
		Create: true,
		Kv:     val,
	}
}

func (c *client) updated(oldVal, val *kv.KeyValue) {
	c.events <- kv.Event{
		Kv:     val,
		PrevKv: oldVal,
	}
}

func (c *client) deleted(val *kv.KeyValue) {
	c.events <- kv.Event{
		Delete: true,
		Kv:     val,
		PrevKv: val,
	}
}
