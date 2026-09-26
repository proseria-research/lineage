package memstore

import (
	"context"
	"reflect"
	"testing"

	"github.com/proseria-research/lineage/internal/domain"
)

// The point of deepCopy is that it does not go stale. These tests populate each entity
// reflectively — every pointer, slice and map beneath it, however deep — copy it, and then
// walk both looking for a shared address. A field added to any entity next year is covered
// the day it is declared, without anyone remembering to come back here.

func TestDeepCopyLeavesNothingShared(t *testing.T) {
	for _, tc := range []struct {
		name string
		make func() (any, any) // original, copy
	}{
		{"Model", func() (any, any) { v := filled[domain.Model](t); return v, deepCopy(v) }},
		{"ModelVersion", func() (any, any) { v := filled[domain.ModelVersion](t); return v, deepCopy(v) }},
		{"Artifact", func() (any, any) { v := filled[domain.Artifact](t); return v, deepCopy(v) }},
		{"LineageEdge", func() (any, any) { v := filled[domain.LineageEdge](t); return v, deepCopy(v) }},
		{"Deployment", func() (any, any) { v := filled[domain.Deployment](t); return v, deepCopy(v) }},
		{"AuditEvent", func() (any, any) { v := filled[domain.AuditEvent](t); return v, deepCopy(v) }},
		{"Hold", func() (any, any) { v := filled[domain.Hold](t); return v, deepCopy(v) }},
		{"RiskClassification", func() (any, any) { v := filled[domain.RiskClassification](t); return v, deepCopy(v) }},
		{"VersionInsight", func() (any, any) { v := filled[domain.VersionInsight](t); return v, deepCopy(v) }},
		{"LayerBlock", func() (any, any) { v := filled[domain.LayerBlock](t); return v, deepCopy(v) }},
		{"Footprint", func() (any, any) { v := filled[domain.Footprint](t); return v, deepCopy(v) }},
		{"Evaluation", func() (any, any) { v := filled[domain.Evaluation](t); return v, deepCopy(v) }},
		{"Validation", func() (any, any) { v := filled[domain.Validation](t); return v, deepCopy(v) }},
		{"ChangePlan", func() (any, any) { v := filled[domain.ChangePlan](t); return v, deepCopy(v) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			orig, cp := tc.make()
			if !reflect.DeepEqual(orig, cp) {
				t.Fatalf("the copy must be equal by value:\n orig=%+v\n copy=%+v", orig, cp)
			}
			assertUnshared(t, reflect.ValueOf(orig), reflect.ValueOf(cp), tc.name, 0)
		})
	}
}

// TestStoreHandsBackCopies is the behavioural half: the property that matters is not that
// deepCopy works but that the adapter actually uses it. Mutating what a read returned must
// not change what the next read returns — which is what the SQL adapters give for free, and
// what this adapter silently did not until §19 forced the issue.
func TestStoreHandsBackCopies(t *testing.T) {
	ctx := context.Background()
	s := New()
	now := domain.NowMillis()

	m := &domain.Model{ID: domain.NewID(), Name: "aliasing", State: domain.StateActive,
		Labels: map[string]string{"team": "risk"}, CreatedAt: now, UpdatedAt: now}
	if err := s.CreateModel(ctx, m); err != nil {
		t.Fatalf("CreateModel: %v", err)
	}

	// The entity handed to Create is the caller's, not the store's.
	m.Name = "renamed-behind-the-stores-back"
	m.Labels["team"] = "clobbered"
	if got, err := s.GetModel(ctx, "aliasing"); err != nil || got.Labels["team"] != "risk" {
		t.Fatalf("mutating the created entity reached into the store: %v %+v", err, got)
	}

	// And what a read returns is the caller's, not the store's. This is the §19 case: a hold
	// must not be releasable by assignment.
	if err := s.SetHold(ctx, domain.SubjectModel, m.ID, &domain.Hold{HeldSince: now, HeldBy: "counsel@acme.example"}); err != nil {
		t.Fatalf("SetHold: %v", err)
	}
	got, err := s.GetModel(ctx, "aliasing")
	if err != nil {
		t.Fatalf("GetModel: %v", err)
	}
	got.LegalHold = nil
	got.Labels["team"] = "clobbered"
	again, err := s.GetModel(ctx, "aliasing")
	if err != nil {
		t.Fatalf("GetModel: %v", err)
	}
	if again.LegalHold == nil {
		t.Fatal("clearing LegalHold on a returned entity released the stored hold")
	}
	if again.Labels["team"] != "risk" {
		t.Fatalf("mutating a returned entity's labels reached into the store: %v", again.Labels)
	}
}

// filled builds a T with every reference field populated, so assertUnshared has something to
// find if deepCopy ever misses one.
func filled[T any](t *testing.T) *T {
	t.Helper()
	v := new(T)
	populate(reflect.ValueOf(v).Elem(), 0)
	return v
}

func populate(v reflect.Value, depth int) {
	if depth > 6 || !v.CanSet() {
		return
	}
	switch v.Kind() {
	case reflect.Pointer:
		n := reflect.New(v.Type().Elem())
		populate(n.Elem(), depth+1)
		v.Set(n)
	case reflect.Slice:
		n := reflect.MakeSlice(v.Type(), 1, 1)
		populate(n.Index(0), depth+1)
		v.Set(n)
	case reflect.Map:
		n := reflect.MakeMap(v.Type())
		key := reflect.New(v.Type().Key()).Elem()
		populate(key, depth+1)
		val := reflect.New(v.Type().Elem()).Elem()
		populate(val, depth+1)
		n.SetMapIndex(key, val)
		v.Set(n)
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			populate(v.Field(i), depth+1)
		}
	case reflect.String:
		v.SetString("x")
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(1)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(1)
	case reflect.Float32, reflect.Float64:
		v.SetFloat(1)
	case reflect.Bool:
		v.SetBool(true)
	}
}

// assertUnshared fails on any pointer, slice or map reachable from both values at the same
// address — and on any unexported reference field, which deepCopy provably cannot fix because
// reflect cannot set it.
func assertUnshared(t *testing.T, a, b reflect.Value, path string, depth int) {
	t.Helper()
	if depth > 8 || !a.IsValid() || !b.IsValid() {
		return
	}
	switch a.Kind() {
	case reflect.Pointer:
		if a.IsNil() || b.IsNil() {
			return
		}
		if a.Pointer() == b.Pointer() {
			t.Fatalf("%s: pointer is shared between the original and its copy", path)
		}
		assertUnshared(t, a.Elem(), b.Elem(), path+".*", depth+1)
	case reflect.Slice:
		if a.IsNil() || b.IsNil() || a.Len() == 0 {
			return
		}
		if a.Pointer() == b.Pointer() {
			t.Fatalf("%s: slice backing array is shared", path)
		}
		for i := 0; i < a.Len() && i < b.Len(); i++ {
			assertUnshared(t, a.Index(i), b.Index(i), path+"[]", depth+1)
		}
	case reflect.Map:
		if a.IsNil() || b.IsNil() {
			return
		}
		if a.Pointer() == b.Pointer() {
			t.Fatalf("%s: map is shared", path)
		}
		for _, k := range a.MapKeys() {
			assertUnshared(t, a.MapIndex(k), b.MapIndex(k), path+"[k]", depth+1)
		}
	case reflect.Struct:
		for i := 0; i < a.NumField(); i++ {
			f := a.Type().Field(i)
			name := path + "." + f.Name
			if !f.IsExported() {
				switch f.Type.Kind() {
				case reflect.Pointer, reflect.Slice, reflect.Map:
					t.Fatalf("%s: unexported reference field — deepCopy cannot copy it, so it "+
						"would stay shared. Export it, or give this entity a hand-written clone.", name)
				}
				continue
			}
			assertUnshared(t, a.Field(i), b.Field(i), name, depth+1)
		}
	}
}
