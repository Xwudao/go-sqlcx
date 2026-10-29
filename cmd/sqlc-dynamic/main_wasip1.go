//go:build wasip1

// The WASM build contains only the codegen plugin; run the link step with
// the native binary.
package main

import (
	"github.com/sqlc-dev/plugin-sdk-go/codegen"

	"github.com/Xwudao/go-sqlcx/internal/plugin"
)

func main() { codegen.Run(plugin.Generate) }
