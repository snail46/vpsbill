package agent

import "time"

// SetPasswordRetry shortens the wait between root password attempts in tests.
func SetPasswordRetry(s *Service, wait time.Duration) { s.passwordRetry = wait }
