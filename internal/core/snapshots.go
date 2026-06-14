package core

import (
	"reflect"
	"unsafe"
)

// Clone returns a deep copy of v, sharing no memory with the original.
func Clone[T any](v T) T {
	snap := snapshotParam(v)
	if snap == nil {
		var zero T
		return zero
	}
	return snap.(T)
}

func snapshotParams(params []any) []any {
	snapshots := make([]any, len(params))
	for i, p := range params {
		snapshots[i] = snapshotParam(p)
	}
	return snapshots
}

func snapshotParam(p any) (snapshot any) {
	if p == nil || !needsSnapshot(reflect.TypeOf(p)) {
		return p
	}
	defer func() {
		if recover() != nil {
			snapshot = p
		}
	}()
	return cloneValue(reflect.ValueOf(p), map[visitKey]reflect.Value{}).Interface()
}

func needsSnapshot(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Ptr, reflect.Slice, reflect.Map, reflect.Interface:
		return true
	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			if needsSnapshot(t.Field(i).Type) {
				return true
			}
		}
		return false
	case reflect.Array:
		return needsSnapshot(t.Elem())
	default:
		return false
	}
}

type visitKey struct {
	ptr uintptr
	typ reflect.Type
}

// pinFunc reports whether a pointer should be kept by identity instead of deep
// copied. A nil pinFunc copies everything (the default for call-param
// snapshots); cloneBaseline passes baselinePin so a double's restore keeps
// shared references stable.
type pinFunc func(reflect.Value) bool

func cloneValue(src reflect.Value, visited map[visitKey]reflect.Value) reflect.Value {
	return cloneValuePinned(src, visited, nil)
}

func cloneValuePinned(src reflect.Value, visited map[visitKey]reflect.Value, pin pinFunc) reflect.Value {
	dst := reflect.New(src.Type()).Elem()
	shallowSet(dst, src)
	fixIndirections(dst, visited, pin)
	return dst
}

func shallowSet(dst, src reflect.Value) {
	if src.CanInterface() {
		dst.Set(src)
		return
	}
	if src.CanAddr() {
		dst.Set(reflect.NewAt(src.Type(), unsafe.Pointer(src.UnsafeAddr())).Elem())
	}
}

func fixIndirections(v reflect.Value, visited map[visitKey]reflect.Value, pin pinFunc) {
	switch v.Kind() {
	case reflect.Ptr:
		if v.IsNil() {
			return
		}
		if pin != nil && pin(v) {
			return // boundary: keep the pointer, do not clone what it points to
		}
		key := visitKey{v.Pointer(), v.Type()}
		if cached, ok := visited[key]; ok {
			v.Set(cached)
			return
		}
		out := reflect.New(v.Type().Elem())
		visited[key] = out
		shallowSet(out.Elem(), v.Elem())
		fixIndirections(out.Elem(), visited, pin)
		v.Set(out)
	case reflect.Slice:
		if v.IsNil() {
			return
		}
		out := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		reflect.Copy(out, v)
		for i := 0; i < out.Len(); i++ {
			fixIndirections(out.Index(i), visited, pin)
		}
		v.Set(out)
	case reflect.Map:
		if v.IsNil() {
			return
		}
		out := reflect.MakeMapWithSize(v.Type(), v.Len())
		iter := v.MapRange()
		for iter.Next() {
			out.SetMapIndex(cloneValuePinned(iter.Key(), visited, pin), cloneValuePinned(iter.Value(), visited, pin))
		}
		v.Set(out)
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			field := v.Field(i)
			if !needsSnapshot(field.Type()) {
				continue
			}
			fixIndirections(reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem(), visited, pin)
		}
	case reflect.Array:
		for i := 0; i < v.Len(); i++ {
			fixIndirections(v.Index(i), visited, pin)
		}
	case reflect.Interface:
		if v.IsNil() {
			return
		}
		v.Set(cloneValuePinned(v.Elem(), visited, pin))
	}
}

var spyPtrType = reflect.TypeOf((*Spy)(nil))

// baselinePin keeps two kinds of pointer by identity when cloning a double's
// baseline: pointers to another registered double (so the wiring between
// doubles survives a restore) and *Spy pointers (so a spy tracked for
// cross-goroutine call visibility keeps its address and is cleared in place).
func baselinePin(v reflect.Value) bool {
	if v.Type() == spyPtrType {
		return true
	}
	return doubleAt(v.Pointer()) != nil
}

// cloneBaseline deep copies src for use as a double's immutable baseline,
// pinning the references baselinePin protects.
func cloneBaseline(src reflect.Value) reflect.Value {
	return cloneValuePinned(src, map[visitKey]reflect.Value{}, baselinePin)
}
