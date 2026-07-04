// Package httpshared holds the leaf HTTP helpers shared between package
// handlers and its subpackage handlers/local. It exists to break the
// bidirectional import cycle: local handlers need a few HLS/transcode/promote
// helpers that used to live in package handlers, while handlers needs the
// local mount/scoping helpers. Everything here is self-contained (depends only
// on gin, the stdlib and internal/transcode) so both sides can import it
// without either importing the other.
package httpshared
