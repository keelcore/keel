//go:build !no_sidecar

package sidecar

import (
    "net/http"
    "net/http/httputil"
    "net/url"
)

func ReverseProxy(upstream string) (http.Handler, error) {
    u, err := url.Parse(upstream)
    if err != nil {
        return nil, err
    }
    return httputil.NewSingleHostReverseProxy(u), nil
}
