package runtime

import (
	"errors"
	"strings"
	"testing"
)

func TestTypeValidate(t *testing.T) {
	valid := []Type{TypeDocker, TypeCompose, TypeSystemd, TypeDaemon, "my-plugin", "k8s"}
	for _, tt := range valid {
		if err := tt.Validate(); err != nil {
			t.Errorf("Type(%q).Validate() = %v, want nil", tt, err)
		}
	}
	invalid := []Type{"", "Docker", "1docker", "docker_x", Type(strings.Repeat("a", 40))}
	for _, tt := range invalid {
		if err := tt.Validate(); !errors.Is(err, ErrInvalidSpec) {
			t.Errorf("Type(%q).Validate() = %v, want ErrInvalidSpec", tt, err)
		}
	}
}

func TestIDValidate(t *testing.T) {
	if err := ID("web-1").Validate(); err != nil {
		t.Errorf("valid id rejected: %v", err)
	}
	for _, id := range []ID{"", "   ", ID(strings.Repeat("x", 300))} {
		if err := id.Validate(); !errors.Is(err, ErrInvalidSpec) {
			t.Errorf("ID(%q).Validate() = %v, want ErrInvalidSpec", id, err)
		}
	}
}

func TestSpecValidate(t *testing.T) {
	ok := Spec{Name: "api", Type: TypeDocker}
	if err := ok.Validate(); err != nil {
		t.Errorf("valid spec rejected: %v", err)
	}
	bad := []Spec{
		{Name: "", Type: TypeDocker},
		{Name: "  ", Type: TypeDocker},
		{Name: "api", Type: "Bad Type"},
		{Name: strings.Repeat("n", 200), Type: TypeDocker},
	}
	for i, s := range bad {
		if err := s.Validate(); !errors.Is(err, ErrInvalidSpec) {
			t.Errorf("case %d: Validate() = %v, want ErrInvalidSpec", i, err)
		}
	}
}

func TestRefString(t *testing.T) {
	r := Ref{ServerID: "srv-1", Type: TypeSystemd, ID: "nginx.service"}
	if got, want := r.String(), "srv-1/systemd/nginx.service"; got != want {
		t.Errorf("Ref.String() = %q, want %q", got, want)
	}
}
