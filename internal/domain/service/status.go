package service

import "fmt"

type Status string

const (
	PendingPayment Status = "pending_payment"
	Provisioning   Status = "provisioning"
	Active         Status = "active"
	Overdue        Status = "overdue"
	Suspended      Status = "suspended"
	Terminating    Status = "terminating"
	Terminated     Status = "terminated"
	Error          Status = "error"
	Review         Status = "review"
)

var transitions = map[Status]map[Status]bool{
	PendingPayment: {Provisioning: true, Review: true, Terminated: true},
	Provisioning:   {Active: true, Error: true, Review: true, Terminating: true},
	Active:         {Overdue: true, Suspended: true, Terminating: true, Error: true},
	Overdue:        {Active: true, Suspended: true, Terminating: true},
	Suspended:      {Active: true, Terminating: true, Error: true},
	Error:          {Provisioning: true, Active: true, Review: true, Terminating: true},
	Review:         {Provisioning: true, Active: true, Terminating: true, Terminated: true},
	Terminating:    {Terminated: true, Error: true},
	Terminated:     {},
}

func CanTransition(from, to Status) bool {
	return transitions[from][to]
}

func ValidateTransition(from, to Status) error {
	if !CanTransition(from, to) {
		return fmt.Errorf("invalid service transition: %s -> %s", from, to)
	}
	return nil
}
