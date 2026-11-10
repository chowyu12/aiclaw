package codemode

import (
	"encoding/json"
	"errors"

	"github.com/dop251/goja"
)

const maxScriptBytes = 1024 * 1024
const maxStoreValueBytes = 1024 * 1024
const maxStoreBytes = 8 * 1024 * 1024
const maxStoreKeys = 256

// CopyStateFrom carries only serialized data across an idle in-process session remount.
// Tool bindings, approvals, workers and cell IDs are never inherited.
func (r *Runtime) CopyStateFrom(previous *Runtime) {
	if previous == r {
		return
	}
	previous.storeMu.Lock()
	values := make(map[string]json.RawMessage, len(previous.stored))
	for key, value := range previous.stored {
		values[key] = append(json.RawMessage(nil), value...)
	}
	previous.storeMu.Unlock()
	r.storeMu.Lock()
	r.stored = values
	r.storeMu.Unlock()
}

func (r *Runtime) put(key string, value json.RawMessage) error {
	if len(key) > 256 || key == "" {
		return errors.New("store key must contain 1 to 256 bytes")
	}
	if len(value) > maxStoreValueBytes {
		return errors.New("store value exceeds 1 MiB")
	}
	if !json.Valid(value) {
		return errors.New("store requires a JSON-serializable value")
	}
	r.storeMu.Lock()
	defer r.storeMu.Unlock()
	size := len(key) + len(value)
	for k, v := range r.stored {
		if k != key {
			size += len(k) + len(v)
		}
	}
	if size > maxStoreBytes {
		return errors.New("session store exceeds 8 MiB")
	}
	if _, ok := r.stored[key]; !ok && len(r.stored) >= maxStoreKeys {
		return errors.New("session store exceeds 256 keys")
	}
	r.stored[key] = append(json.RawMessage(nil), value...)
	return nil
}

func (r *Runtime) get(key string) (json.RawMessage, error) {
	r.storeMu.Lock()
	defer r.storeMu.Unlock()
	return append(json.RawMessage(nil), r.stored[key]...), nil
}

func serializable(vm *goja.Runtime, value goja.Value) json.RawMessage {
	// Serialize inside the VM: no functions, live Go proxies or references to another cell escape.
	fn, _ := goja.AssertFunction(vm.Get("JSON").ToObject(vm).Get("stringify"))
	v, err := fn(goja.Undefined(), value)
	if err != nil {
		panic(err)
	}
	if v == nil || goja.IsUndefined(v) {
		panic(vm.NewTypeError("value is not JSON serializable"))
	}
	return json.RawMessage(v.String())
}
