package auth

import (
	"errors"

	"github.com/corinthian/traktctl/internal/xhttp"
)

func isOversize(err error) bool { return errors.Is(err, xhttp.ErrOversize) }
