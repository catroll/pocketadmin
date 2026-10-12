package organization

import "errors"

// Line-manager guard errors.
var (
	errSelfManager  = errors.New("an employee cannot be their own line manager")
	errManagerCycle = errors.New("that manager already reports to this employee, directly or through someone else")
)
