/*
Copyright 2016 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package kv

import (
	"bytes"
	"errors"
	"fmt"
	"path"
	"reflect"
	"strings"
	"time"

	"github.com/golang/glog"
	"golang.org/x/net/context"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/conversion"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/storage"
	"k8s.io/apiserver/pkg/storage/etcd"
	"k8s.io/apiserver/pkg/storage/value"
	utiltrace "k8s.io/apiserver/pkg/util/trace"
)

// authenticatedDataString satisfies the value.Context interface. It uses the key to
// authenticate the stored data. This does not defend against reuse of previously
// encrypted values under the same key, but will prevent an attacker from using an
// encrypted value from a different key. A stronger authenticated data segment would
// include the etcd3 Version field (which is incremented on each write to a key and
// reset when the key is deleted), but an attacker with write access to etcd can
// force deletion and recreation of keys to weaken that angle.
type authenticatedDataString string

// AuthenticatedData implements the value.Context interface.
func (d authenticatedDataString) AuthenticatedData() []byte {
	return []byte(string(d))
}

var _ value.Context = authenticatedDataString("")

type store struct {
	client Client
	// getOpts contains additional options that should be passed
	// to all Get() calls.
	codec       runtime.Codec
	versioner   storage.Versioner
	transformer value.Transformer
	pathPrefix  string
	watcher     *watcher
}

type elemForDecode struct {
	data []byte
	rev  uint64
}

type objState struct {
	obj   runtime.Object
	meta  *storage.ResponseMeta
	rev   int64
	data  []byte
	stale bool
}

// New returns an etcd3 implementation of storage.Interface.
func New(c Client, codec runtime.Codec, prefix string, transformer value.Transformer) storage.Interface {
	return newStore(c, codec, prefix, transformer)
}

func newStore(c Client, codec runtime.Codec, prefix string, transformer value.Transformer) *store {
	versioner := etcd.APIObjectVersioner{}
	result := &store{
		client:      c,
		codec:       codec,
		versioner:   versioner,
		transformer: transformer,
		// for compatibility with etcd2 impl.
		// no-op for default prefix of '/registry'.
		// keeps compatibility with etcd2 impl for custom prefixes that don't start with '/'
		pathPrefix: path.Join("/", prefix),
		watcher:    newWatcher(c, codec, versioner, transformer),
	}
	return result
}

// Versioner implements storage.Interface.Versioner.
func (s *store) Versioner() storage.Versioner {
	return s.versioner
}

// Get implements storage.Interface.Get.
func (s *store) Get(ctx context.Context, key string, resourceVersion string, out runtime.Object, ignoreNotFound bool) error {
	key = path.Join(s.pathPrefix, key)
	resp, err := s.client.Get(ctx, key)
	if err != nil {
		return err
	}

	if resp == nil {
		if ignoreNotFound {
			return runtime.SetZeroValue(out)
		}
		return storage.NewKeyNotFoundError(key, 0)
	}

	data, _, err := s.transformer.TransformFromStorage(resp.Value, authenticatedDataString(key))
	if err != nil {
		return storage.NewInternalError(err.Error())
	}

	return decode(s.codec, s.versioner, data, out, resp.Revision)
}

// Create implements storage.Interface.Create.
func (s *store) Create(ctx context.Context, key string, obj, out runtime.Object, ttl uint64) error {
	if version, err := s.versioner.ObjectResourceVersion(obj); err == nil && version != 0 {
		return errors.New("resourceVersion should not be set on objects to be created")
	}
	data, err := runtime.Encode(s.codec, obj)
	if err != nil {
		return err
	}
	key = path.Join(s.pathPrefix, key)

	newData, err := s.transformer.TransformToStorage(data, authenticatedDataString(key))
	if err != nil {
		return storage.NewInternalError(err.Error())
	}

	resp, err := s.client.Create(ctx, key, newData, ttl)
	if err == ErrExists {
		return storage.NewKeyExistsError(key, 0)
	} else if err != nil {
		return err
	}

	if out != nil {
		return decode(s.codec, s.versioner, data, out, resp.Revision)
	}
	return nil
}

// Delete implements storage.Interface.Delete.
func (s *store) Delete(ctx context.Context, key string, out runtime.Object, preconditions *storage.Preconditions) error {
	v, err := conversion.EnforcePtr(out)
	if err != nil {
		panic("unable to convert output object to pointer")
	}
	key = path.Join(s.pathPrefix, key)
	if preconditions == nil {
		return s.unconditionalDelete(ctx, key, out)
	}
	return s.conditionalDelete(ctx, key, out, v, preconditions)
}

func (s *store) unconditionalDelete(ctx context.Context, key string, out runtime.Object) error {
	// We need to do get and delete in single transaction in order to
	// know the value and revision before deleting it.
	resp, err := s.client.Delete(ctx, key)
	if err == ErrNotExists {
		return storage.NewKeyNotFoundError(key, 0)
	} else if err != nil {
		return err
	}

	data, _, err := s.transformer.TransformFromStorage(resp.Value, authenticatedDataString(key))
	if err != nil {
		return storage.NewInternalError(err.Error())
	}
	return decode(s.codec, s.versioner, data, out, resp.Revision)
}

func (s *store) conditionalDelete(ctx context.Context, key string, out runtime.Object, v reflect.Value, preconditions *storage.Preconditions) error {
	for {
		getResp, err := s.client.Get(ctx, key)
		if err != nil {
			return err
		}

		origState, err := s.getState(getResp, key, v, false)
		if err != nil {
			return err
		}
		if err := checkPreconditions(key, preconditions, origState.obj); err != nil {
			return err
		}
		if err := s.client.DeleteVersion(ctx, key, origState.rev); err == ErrNotExists {
			continue
		} else if err != nil {
			return err
		}
		return decode(s.codec, s.versioner, origState.data, out, origState.rev)
	}
}

// GuaranteedUpdate implements storage.Interface.GuaranteedUpdate.
func (s *store) GuaranteedUpdate(
	ctx context.Context, key string, out runtime.Object, ignoreNotFound bool,
	preconditions *storage.Preconditions, tryUpdate storage.UpdateFunc, suggestion ...runtime.Object) error {
	trace := utiltrace.New(fmt.Sprintf("GuaranteedUpdate etcd3: %s", reflect.TypeOf(out).String()))
	defer trace.LogIfLong(500 * time.Millisecond)

	v, err := conversion.EnforcePtr(out)
	if err != nil {
		panic("unable to convert output object to pointer")
	}
	key = path.Join(s.pathPrefix, key)

	var origState *objState
	if len(suggestion) == 1 && suggestion[0] != nil {
		origState, err = s.getStateFromObject(suggestion[0])
		if err != nil {
			return err
		}
	} else {
		getResp, err := s.client.Get(ctx, key)
		if err != nil {
			return err
		}
		origState, err = s.getState(getResp, key, v, ignoreNotFound)
		if err != nil {
			return err
		}
	}
	trace.Step("initial value restored")

	transformContext := authenticatedDataString(key)
	for {
		if err := checkPreconditions(key, preconditions, origState.obj); err != nil {
			return err
		}

		ret, ttl, err := s.updateState(origState, tryUpdate)
		if err != nil {
			return err
		}

		data, err := runtime.Encode(s.codec, ret)
		if err != nil {
			return err
		}
		if !origState.stale && bytes.Equal(data, origState.data) {
			return decode(s.codec, s.versioner, origState.data, out, origState.rev)
		}

		newData, err := s.transformer.TransformToStorage(data, transformContext)
		if err != nil {
			return storage.NewInternalError(err.Error())
		}

		trace.Step("Transaction prepared")

		resp, err := s.client.UpdateOrCreate(ctx, key, newData, origState.rev, ttl)
		if err == ErrNotExists {
			glog.V(4).Infof("GuaranteedUpdate of %s failed because of a conflict, going to retry", key)
			origState, err = s.getState(resp, key, v, ignoreNotFound)
			if err != nil {
				return err
			}
			trace.Step("Retry value restored")
			continue
		} else if err != nil {
			return err
		}

		trace.Step("Transaction committed")

		return decode(s.codec, s.versioner, data, out, resp.Revision)
	}
}

// GetToList implements storage.Interface.GetToList.
func (s *store) GetToList(ctx context.Context, key string, resourceVersion string, pred storage.SelectionPredicate, listObj runtime.Object) error {
	listPtr, err := meta.GetItemsPtr(listObj)
	if err != nil {
		return err
	}
	key = path.Join(s.pathPrefix, key)

	resp, err := s.client.Get(ctx, key)
	if err != nil {
		return err
	}
	if resp == nil {
		return nil
	}
	data, _, err := s.transformer.TransformFromStorage(resp.Value, authenticatedDataString(key))
	if err != nil {
		return storage.NewInternalError(err.Error())
	}
	elems := []*elemForDecode{{
		data: data,
		rev:  uint64(resp.Revision),
	}}
	if err := decodeList(elems, storage.SimpleFilter(pred), listPtr, s.codec, s.versioner); err != nil {
		return err
	}
	// TODO: List revision seems silly and complicated. Setting to 0
	return s.versioner.UpdateList(listObj, 0)
}

// List implements storage.Interface.List.
func (s *store) List(ctx context.Context, key, resourceVersion string, pred storage.SelectionPredicate, listObj runtime.Object) error {
	listPtr, err := meta.GetItemsPtr(listObj)
	if err != nil {
		return err
	}
	key = path.Join(s.pathPrefix, key)
	// We need to make sure the key ended with "/" so that we only get children "directories".
	// e.g. if we have key "/a", "/a/b", "/ab", getting keys with prefix "/a" will return all three,
	// while with prefix "/a/" will return only "/a/b" which is the correct answer.
	if !strings.HasSuffix(key, "/") {
		key += "/"
	}
	getResp, err := s.client.List(ctx, key)
	if err != nil {
		return err
	}

	elems := make([]*elemForDecode, 0, len(getResp))
	for _, item := range getResp {
		data, _, err := s.transformer.TransformFromStorage(item.Value, authenticatedDataString(item.Key))
		if err != nil {
			utilruntime.HandleError(fmt.Errorf("unable to transform key %q: %v", key, err))
			continue
		}

		elems = append(elems, &elemForDecode{
			data: data,
			rev:  uint64(item.Revision),
		})
	}
	if err := decodeList(elems, storage.SimpleFilter(pred), listPtr, s.codec, s.versioner); err != nil {
		return err
	}
	// TODO: List revision seems silly and complicated. Setting to 0
	return s.versioner.UpdateList(listObj, 0)
}

// Watch implements storage.Interface.Watch.
func (s *store) Watch(ctx context.Context, key string, resourceVersion string, pred storage.SelectionPredicate) (watch.Interface, error) {
	return s.watch(ctx, key, resourceVersion, pred, false)
}

// WatchList implements storage.Interface.WatchList.
func (s *store) WatchList(ctx context.Context, key string, resourceVersion string, pred storage.SelectionPredicate) (watch.Interface, error) {
	return s.watch(ctx, key, resourceVersion, pred, true)
}

func (s *store) watch(ctx context.Context, key string, rv string, pred storage.SelectionPredicate, recursive bool) (watch.Interface, error) {
	rev, err := storage.ParseWatchResourceVersion(rv)
	if err != nil {
		return nil, err
	}
	key = path.Join(s.pathPrefix, key)
	return s.watcher.Watch(ctx, key, int64(rev), recursive, pred)
}

func (s *store) getState(item *KeyValue, key string, v reflect.Value, ignoreNotFound bool) (*objState, error) {
	state := &objState{
		obj:  reflect.New(v.Type()).Interface().(runtime.Object),
		meta: &storage.ResponseMeta{},
	}
	if item == nil {
		if !ignoreNotFound {
			return nil, storage.NewKeyNotFoundError(key, 0)
		}
		if err := runtime.SetZeroValue(state.obj); err != nil {
			return nil, err
		}
	} else {
		data, stale, err := s.transformer.TransformFromStorage(item.Value, authenticatedDataString(key))
		if err != nil {
			return nil, storage.NewInternalError(err.Error())
		}
		state.rev = item.Revision
		state.meta.ResourceVersion = uint64(state.rev)
		state.data = data
		state.stale = stale
		if err := decode(s.codec, s.versioner, state.data, state.obj, state.rev); err != nil {
			return nil, err
		}
	}
	return state, nil
}

func (s *store) getStateFromObject(obj runtime.Object) (*objState, error) {
	state := &objState{
		obj:  obj,
		meta: &storage.ResponseMeta{},
	}

	rv, err := s.versioner.ObjectResourceVersion(obj)
	if err != nil {
		return nil, fmt.Errorf("couldn't get resource version: %v", err)
	}
	state.rev = int64(rv)
	state.meta.ResourceVersion = uint64(state.rev)

	// Compute the serialized form - for that we need to temporarily clean
	// its resource version field (those are not stored in etcd).
	if err := s.versioner.UpdateObject(obj, 0); err != nil {
		return nil, errors.New("resourceVersion cannot be set on objects store in etcd")
	}
	state.data, err = runtime.Encode(s.codec, obj)
	if err != nil {
		return nil, err
	}
	s.versioner.UpdateObject(state.obj, uint64(rv))
	return state, nil
}

func (s *store) updateState(st *objState, userUpdate storage.UpdateFunc) (runtime.Object, uint64, error) {
	ret, ttlPtr, err := userUpdate(st.obj, *st.meta)
	if err != nil {
		return nil, 0, err
	}

	version, err := s.versioner.ObjectResourceVersion(ret)
	if err != nil {
		return nil, 0, err
	}
	if version != 0 {
		// We cannot store object with resourceVersion in etcd. We need to reset it.
		if err := s.versioner.UpdateObject(ret, 0); err != nil {
			return nil, 0, fmt.Errorf("UpdateObject failed: %v", err)
		}
	}
	var ttl uint64
	if ttlPtr != nil {
		ttl = *ttlPtr
	}
	return ret, ttl, nil
}

// decode decodes value of bytes into object. It will also set the object resource version to rev.
// On success, objPtr would be set to the object.
func decode(codec runtime.Codec, versioner storage.Versioner, value []byte, objPtr runtime.Object, rev int64) error {
	if _, err := conversion.EnforcePtr(objPtr); err != nil {
		panic("unable to convert output object to pointer")
	}
	_, _, err := codec.Decode(value, nil, objPtr)
	if err != nil {
		return err
	}
	// being unable to set the version does not prevent the object from being extracted
	versioner.UpdateObject(objPtr, uint64(rev))
	return nil
}

// decodeList decodes a list of values into a list of objects, with resource version set to corresponding rev.
// On success, ListPtr would be set to the list of objects.
func decodeList(elems []*elemForDecode, filter storage.FilterFunc, ListPtr interface{}, codec runtime.Codec, versioner storage.Versioner) error {
	v, err := conversion.EnforcePtr(ListPtr)
	if err != nil || v.Kind() != reflect.Slice {
		panic("need ptr to slice")
	}
	for _, elem := range elems {
		obj, _, err := codec.Decode(elem.data, nil, reflect.New(v.Type().Elem()).Interface().(runtime.Object))
		if err != nil {
			return err
		}
		// being unable to set the version does not prevent the object from being extracted
		versioner.UpdateObject(obj, elem.rev)
		if filter(obj) {
			v.Set(reflect.Append(v, reflect.ValueOf(obj).Elem()))
		}
	}
	return nil
}

func checkPreconditions(key string, preconditions *storage.Preconditions, out runtime.Object) error {
	if preconditions == nil {
		return nil
	}
	objMeta, err := meta.Accessor(out)
	if err != nil {
		return storage.NewInternalErrorf("can't enforce preconditions %v on un-introspectable object %v, got error: %v", *preconditions, out, err)
	}
	if preconditions.UID != nil && *preconditions.UID != objMeta.GetUID() {
		errMsg := fmt.Sprintf("Precondition failed: UID in precondition: %v, UID in object meta: %v", *preconditions.UID, objMeta.GetUID())
		return storage.NewInvalidObjError(key, errMsg)
	}
	return nil
}
