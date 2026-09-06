package client

import (
	"io"
	"log"
)

// quietLog keeps httptest's expected connection errors out of the test output.
func quietLog() *log.Logger { return log.New(io.Discard, "", 0) }
