package agent

import "time"

// SetPasswordRetry shortens the wait between root password attempts in tests.
func SetPasswordRetry(s *Service, wait time.Duration) { s.passwordRetry = wait }

// SetCommandRunner replaces host command execution in tests.
func SetCommandRunner(s *Service, run CommandRunner) { s.run = run }

// WaitBackground waits for work the service left running after replying.
func WaitBackground(s *Service) { s.background.Wait() }

// SetClock replaces the service's clock in tests.
func SetClock(s *Service, now func() time.Time) { s.now = now }

// SetFirstSampleWait sets how long usage waits between its two looks at an
// instance it has no recent observation of.
func SetFirstSampleWait(s *Service, wait time.Duration) { s.firstSampleWait = wait }
