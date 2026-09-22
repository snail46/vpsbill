package service

import "testing"

func TestTransitions(t *testing.T) {
	tests := []struct {
		from, to Status
		allowed  bool
	}{
		{PendingPayment, Provisioning, true},
		{Provisioning, Active, true},
		{Active, Suspended, true},
		{Suspended, Active, true},
		{Terminated, Active, false},
		{PendingPayment, Active, false},
	}
	for _, test := range tests {
		if got := CanTransition(test.from, test.to); got != test.allowed {
			t.Fatalf("transition %s -> %s: got %v want %v", test.from, test.to, got, test.allowed)
		}
	}
}
