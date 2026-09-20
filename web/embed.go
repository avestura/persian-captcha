// Package web carries the browser-side assets, embedded into the binary.
//
// The TypeScript in src/ is compiled to dist/ by esbuild (see package.json),
// and the build output is committed. That means `go build` alone produces a
// working binary: contributors who only touch Go, and CI jobs that only run
// Go, never need a Node toolchain.
package web

import "embed"

// Dist holds the compiled widget and frame bundles.
//
//go:embed dist
var Dist embed.FS

// FrameHTML is the template for the challenge iframe document.
//
//go:embed frame.html
var FrameHTML string

// Assets holds static files served as-is, such as the optional Persian
// webfont. The all: prefix keeps files the toolchain would otherwise skip.
//
//go:embed all:assets
var Assets embed.FS
