package core

import (
	"reflect"
	"sync"
	"testing"
	"unsafe"
)

type doubleRecord struct {
	t       testing.TB
	ptr     reflect.Value
	typ     reflect.Type
	initial reflect.Value
}

var doubleRecords sync.Map

func recordDouble(t *testing.T, ptr reflect.Value) {
	elem := ptr.Elem()
	if elem.Type().Size() == 0 {
		return
	}
	base := ptr.Pointer()
	doubleRecords.Store(base, &doubleRecord{
		t:       t,
		ptr:     ptr,
		typ:     elem.Type(),
		initial: reflect.ValueOf(elem.Interface()),
	})
	t.Cleanup(func() { doubleRecords.Delete(base) })
}

var goroutineTests = sync.Map{}

func bindGoroutine(t testing.TB) func() {
	id := getTestID()
	if id == "" {
		return func() {}
	}
	prev, hadPrev := goroutineTests.Load(id)
	goroutineTests.Store(id, t)
	return func() {
		if hadPrev {
			goroutineTests.Store(id, prev)
		} else {
			goroutineTests.Delete(id)
		}
	}
}

func currentT() testing.TB {
	if id := getTestID(); id != "" {
		if v, ok := goroutineTests.Load(id); ok {
			return v.(testing.TB)
		}
	}
	return nil
}

func doubleAt(base uintptr) *doubleRecord {
	if v, ok := doubleRecords.Load(base); ok {
		return v.(*doubleRecord)
	}
	return nil
}

func changed(r *doubleRecord) bool {
	current := r.ptr.Elem()
	initial := reflect.New(r.typ).Elem()
	initial.Set(r.initial)

	if r.typ.Kind() != reflect.Struct {
		return !reflect.DeepEqual(fieldValue(initial), fieldValue(current))
	}

	spyPtr := reflect.TypeOf((*Spy)(nil))
	spyVal := reflect.TypeOf(Spy{})
	for i := 0; i < r.typ.NumField(); i++ {
		if ft := r.typ.Field(i).Type; ft == spyPtr || ft == spyVal {
			continue
		}
		if !reflect.DeepEqual(fieldValue(initial.Field(i)), fieldValue(current.Field(i))) {
			return true
		}
	}
	return false
}

func fieldStateByPtr(r *doubleRecord, fieldPtr any) (name string, initial any, current any, ok bool) {
	if r.typ.Kind() != reflect.Struct {
		return "", nil, nil, false
	}
	pv := reflect.ValueOf(fieldPtr)
	if pv.Kind() != reflect.Ptr || pv.IsNil() {
		return "", nil, nil, false
	}
	base := r.ptr.Pointer()
	addr := pv.Pointer()
	if addr < base {
		return "", nil, nil, false
	}
	offset := addr - base

	for i := 0; i < r.typ.NumField(); i++ {
		f := r.typ.Field(i)
		if uintptr(f.Offset) != offset {
			continue
		}
		initialStruct := reflect.New(r.typ).Elem()
		initialStruct.Set(r.initial)
		return f.Name, fieldValue(initialStruct.Field(i)), fieldValue(r.ptr.Elem().Field(i)), true
	}
	return "", nil, nil, false
}

func fieldValue(v reflect.Value) any {
	return reflect.NewAt(v.Type(), unsafe.Pointer(v.UnsafeAddr())).Elem().Interface()
}

func doubleName(r *doubleRecord) string {
	if n := r.typ.Name(); n != "" {
		return n
	}
	return r.typ.String()
}
