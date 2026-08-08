package memstore

import "reflect"

// Copy-on-read and copy-on-write for the in-memory adapter.
//
// A store is not a shared object graph. The SQL adapters marshal every entity across a driver
// boundary, so a caller physically cannot reach a persisted row — mutating what a Get returned
// changes nothing, and a write happens only when a write method is called. This adapter used
// to hand back its live pointers, which made both of those false, and made it possible for
// memory-backed code to pass a test the SQL adapters would fail.
//
// §19 is where that stopped being theoretical: `m := GetModel(...); m.LegalHold = nil` released
// a legal hold, with no audit event, no write call, and no way for the two adapters to agree.
//
// Rather than a clone function per entity — ten of them, each quietly going stale the next
// time a field is added — this is one reflective deep copy. New fields are covered the day
// they are declared, and clone_test.go fails if any reference-typed field is ever shared.

// deepCopy returns a copy of v that shares no mutable memory with it.
//
// The struct is first copied by value, which carries scalars and any unexported fields, then
// every settable pointer, slice and map beneath it is rebuilt. Unexported reference fields
// would stay shared — reflect cannot set them — but none of the entities has one, and
// clone_test.go asserts the entities stay that way.
func deepCopy[T any](v *T) *T {
	if v == nil {
		return nil
	}
	c := new(T)
	*c = *v
	deepFix(reflect.ValueOf(c).Elem())
	return c
}

// deepCopyAll copies a slice and every element in it.
func deepCopyAll[T any](in []*T) []*T {
	if in == nil {
		return nil
	}
	out := make([]*T, len(in))
	for i, e := range in {
		out[i] = deepCopy(e)
	}
	return out
}

func deepFix(v reflect.Value) {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() || !v.CanSet() {
			return
		}
		n := reflect.New(v.Type().Elem())
		n.Elem().Set(v.Elem())
		deepFix(n.Elem())
		v.Set(n)

	case reflect.Slice:
		// A nil slice stays nil: json.RawMessage(nil) and []byte{} marshal differently, and
		// round-tripping one into the other would change what a response body looks like.
		if v.IsNil() || !v.CanSet() {
			return
		}
		n := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		reflect.Copy(n, v)
		for i := 0; i < n.Len(); i++ {
			deepFix(n.Index(i))
		}
		v.Set(n)

	case reflect.Map:
		if v.IsNil() || !v.CanSet() {
			return
		}
		n := reflect.MakeMapWithSize(v.Type(), v.Len())
		iter := v.MapRange()
		for iter.Next() {
			// Map values are not addressable, so each one is copied into a settable temporary
			// before being fixed and put back.
			ev := reflect.New(v.Type().Elem()).Elem()
			ev.Set(iter.Value())
			deepFix(ev)
			n.SetMapIndex(iter.Key(), ev)
		}
		v.Set(n)

	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			deepFix(v.Field(i))
		}
	}
}
