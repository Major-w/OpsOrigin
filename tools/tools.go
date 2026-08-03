//go:build tools
// +build tools

// Package tools tracks build-time tool dependencies so that `go generate`
// (and CI) can resolve them via go.sum. It is never compiled into the
// application binary (guarded by the `tools` build tag).
package tools

import (
	_ "entgo.io/ent/cmd/ent"
)
