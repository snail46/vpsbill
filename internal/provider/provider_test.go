package provider

import (
	"context"
	"errors"
	"testing"
)

type minimalDriver struct{}

func (minimalDriver) HostInfo(context.Context) (HostInfo, error) { return HostInfo{}, nil }
func (minimalDriver) Images(context.Context) ([]Image, error)    { return nil, nil }
func (minimalDriver) GetInstance(context.Context, string) (Instance, error) {
	return Instance{}, ErrNotFound
}
func (minimalDriver) EnsureInstance(context.Context, CreateSpec) (EnsureResult, error) {
	return EnsureResult{}, nil
}
func (minimalDriver) PowerAction(context.Context, string, string) (string, error) { return "", nil }
func (minimalDriver) DeleteInstance(context.Context, string) error                { return nil }

type suspendingDriver struct{ minimalDriver }

func (suspendingDriver) Suspend(context.Context, string) error { return nil }
func (suspendingDriver) Resume(context.Context, string) error  { return nil }

func TestCapabilitiesFollowImplementedInterfaces(t *testing.T) {
	minimal := CapabilitiesOf(minimalDriver{})
	if minimal.Reinstall || minimal.PortMapping || minimal.Suspend || minimal.Console == nil || len(minimal.Console) != 0 {
		t.Fatalf("minimal driver must advertise nothing optional: %+v", minimal)
	}
	if !CapabilitiesOf(suspendingDriver{}).Suspend {
		t.Fatal("suspend capability not detected")
	}
}

func TestNormalizeStatus(t *testing.T) {
	cases := map[string]string{"Running": "running", "started": "running", "STOPPED": "stopped", "Frozen": "suspended", "pending": "creating", "surprise": "unknown"}
	for input, expected := range cases {
		if got := NormalizeStatus(input); got != expected {
			t.Errorf("NormalizeStatus(%q) = %q, want %q", input, got, expected)
		}
	}
}

func TestImageSellable(t *testing.T) {
	image := Image{ID: "debian-12", Virtualization: "lxc", Enabled: true, Downloaded: true}
	if !image.Sellable("lxc") || image.Sellable("kvm") {
		t.Fatal("virtualization filter mismatch")
	}
	image.Downloaded = false
	if image.Sellable("lxc") {
		t.Fatal("undownloaded image must not be sellable")
	}
	if !(Image{Enabled: true, Downloaded: true}).Sellable("kvm") {
		t.Fatal("untyped image should match any virtualization")
	}
}

type fakeBox struct{ plaintext string }

func (b fakeBox) Open([]byte) (string, error) { return b.plaintext, nil }

func TestRegistryOpensRegisteredTypesOnly(t *testing.T) {
	var received Config
	Register(Descriptor{Type: "test-registry", Name: "Test"}, func(config Config) (Driver, error) {
		received = config
		return minimalDriver{}, nil
	})
	if _, err := Open("missing", Config{}); !errors.Is(err, ErrUnknownType) {
		t.Fatalf("expected ErrUnknownType, got %v", err)
	}
	if _, err := OpenSealed(fakeBox{plaintext: "secret"}, Sealed{Type: "test-registry", BaseURL: "https://node", CredentialCiphertext: []byte("sealed")}, 0); err != nil {
		t.Fatal(err)
	}
	if received.Credential != "secret" || received.BaseURL != "https://node" {
		t.Fatalf("factory received %+v", received)
	}
	if _, ok := Lookup("test-registry"); !ok {
		t.Fatal("registered type not found")
	}
}
