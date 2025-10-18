package main

import "net/http"

// httpDoer represents the minimal client contract used by integration helpers.
type httpDoer interface {
	Do(*http.Request) (*http.Response, error)
}
