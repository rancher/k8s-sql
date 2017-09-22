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

type event struct {
	key       string
	value     []byte
	prevValue []byte
	rev       int64
	isDeleted bool
	isCreated bool
}

// parseKV converts a KeyValue retrieved from an initial sync() listing to a synthetic isCreated event.
func parseKV(kv *KeyValue) *event {
	return &event{
		key:       string(kv.Key),
		value:     kv.Value,
		prevValue: nil,
		rev:       kv.Revision,
		isDeleted: false,
		isCreated: true,
	}
}

func parseEvent(e Event) *event {
	ret := &event{
		key:       string(e.Kv.Key),
		value:     e.Kv.Value,
		rev:       e.Kv.Revision,
		isDeleted: e.Delete,
		isCreated: e.Create,
	}
	if e.PrevKv != nil {
		ret.prevValue = e.PrevKv.Value
	}
	return ret
}
