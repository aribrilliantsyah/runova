package api

import "time"

// SetSessionRecheck shortens the WebSocket session recheck interval for a test
// and returns a function that restores it.
func SetSessionRecheck(d time.Duration) (restore func()) {
	old := sessionRecheck
	sessionRecheck = d
	return func() { sessionRecheck = old }
}
