// Package core holds feedwatch's pure domain types (Feed, Item, Enclosure,
// Category) together with the error taxonomy (FeedError and the sentinel
// errors). It has no internal dependencies and is imported by every other
// package, so output, parse, and store can depend on it without depending on
// one another.
//
// core is a public package of the library surface described in ADR 0007: it
// holds the domain types every public signature is written in terms of,
// including ParsedFeed and Candidate, so an external embedder can name every
// type it hands to feedwatch or receives back.
//
// FeedError exposes Code, ExitCode, and Hint, so an embedder classifies and
// re-encodes a failure without reaching into feedwatch's internal packages.
//
// For the stability commitment this package carries, see the feedwatch package
// documentation.
package core
