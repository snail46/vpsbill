package app

import "testing"

func TestHasPermission(t *testing.T) {
	tests := []struct {
		permissions []string
		required    string
		want        bool
	}{
		{[]string{"*"}, "nodes:write", true},
		{[]string{"nodes:read", "plans:read"}, "nodes:read", true},
		{[]string{"nodes:read"}, "nodes:write", false},
		{nil, "plans:read", false},
	}
	for _, test := range tests {
		if got := hasPermission(test.permissions, test.required); got != test.want {
			t.Fatalf("hasPermission(%v, %q)=%v want %v", test.permissions, test.required, got, test.want)
		}
	}
}
