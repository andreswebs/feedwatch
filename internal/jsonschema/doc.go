// Package jsonschema reflects a Go value into a draft-07 JSON Schema.
//
// It exists so feedwatch's command output schemas are derived from the Go
// result structs that commands actually return, rather than hand-authored
// alongside them where the two can silently drift. The surface is deliberately
// small: Reflect walks a struct, OneOf wraps alternative shapes, and Scalar
// describes a non-object output. It depends only on the standard library.
package jsonschema
