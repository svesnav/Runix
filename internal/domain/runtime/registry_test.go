package runtime

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type fakeProvider struct{ t Type }

func (p fakeProvider) Type() Type                  { return p.t }
func (p fakeProvider) Capabilities() CapabilitySet { return 0 }
func (p fakeProvider) Availability(context.Context) Availability {
	return Availability{Available: true}
}
func (p fakeProvider) List(context.Context) ([]Descriptor, error) { return nil, nil }
func (p fakeProvider) Get(context.Context, ID) (Runtime, error)   { return nil, ErrNotFound }
func (p fakeProvider) Create(context.Context, Spec) (Runtime, error) {
	return nil, ErrNotSupported
}
func (p fakeProvider) Remove(context.Context, ID, RemoveOptions) error { return nil }

func TestRegistryRegisterAndGet(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(fakeProvider{t: TypeDocker}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	p, err := r.Get(TypeDocker)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if p.Type() != TypeDocker {
		t.Errorf("Get returned provider of type %q", p.Type())
	}
}

func TestRegistryRejectsDuplicates(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(fakeProvider{t: TypeSystemd}); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	if err := r.Register(fakeProvider{t: TypeSystemd}); !errors.Is(err, ErrAlreadyExists) {
		t.Errorf("duplicate Register = %v, want ErrAlreadyExists", err)
	}
}

func TestRegistryRejectsInvalid(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(nil); !errors.Is(err, ErrInvalidSpec) {
		t.Errorf("Register(nil) = %v, want ErrInvalidSpec", err)
	}
	if err := r.Register(fakeProvider{t: "Bad Type"}); !errors.Is(err, ErrInvalidSpec) {
		t.Errorf("Register(invalid type) = %v, want ErrInvalidSpec", err)
	}
}

func TestRegistryGetUnknown(t *testing.T) {
	r := NewRegistry()
	if _, err := r.Get(TypeCompose); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(unregistered) = %v, want ErrNotFound", err)
	}
}

func TestRegistryTypesSorted(t *testing.T) {
	r := NewRegistry()
	for _, tt := range []Type{TypeSystemd, TypeDocker, TypeDaemon} {
		if err := r.Register(fakeProvider{t: tt}); err != nil {
			t.Fatalf("Register(%s): %v", tt, err)
		}
	}
	want := []Type{TypeDaemon, TypeDocker, TypeSystemd}
	if got := r.Types(); !reflect.DeepEqual(got, want) {
		t.Errorf("Types() = %v, want %v", got, want)
	}
	if got := len(r.Providers()); got != 3 {
		t.Errorf("Providers() returned %d providers, want 3", got)
	}
}
