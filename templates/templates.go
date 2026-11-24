// Package templates holds the files pixelgo-ops generates its configuration
// from, embedded into the binary.
//
// Embedded rather than read from disk because `make install` copies the binary
// and nothing else: an installed pixelgo-ops has no templates/ directory next
// to it, and reading one relative to the working directory would work only
// when run from a checkout. The skeleton's plan - "generate from templates/" -
// could not have worked for an installed binary.
//
// The directory stays where the README says it is, and each consumer reads its
// own file out of FS.
package templates

import "embed"

//go:embed *.conf *.xml *.yaml
var FS embed.FS
