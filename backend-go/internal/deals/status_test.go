package deals

import "strconv"

// statusError is a site's answer with a status other than 200, for fake
// sources.
type statusError struct{ code int }

func (e statusError) Error() string { return "status " + strconv.Itoa(e.code) }

func (e statusError) StatusCode() int { return e.code }
