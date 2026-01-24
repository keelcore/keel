//go:build no_sidecar

package sidecar

import (
    "errors"
    "net/http"
)

func ReverseProxy(_ string) (http.Handler, error) {
    return nil, errors.New("sidecar disabled by build tag")
}
