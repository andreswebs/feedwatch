// Package core holds feedwatch's pure domain types (Feed, Item, Enclosure,
// Category) together with the error taxonomy (FeedError and the sentinel
// errors). It has no internal dependencies and is imported by every other
// package, so output, parse, and store can depend on it without depending on
// one another.
package core
