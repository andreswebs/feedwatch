// Package store defines the Store interface over core domain types: feed and
// item persistence, the dedup upsert, history queries, and retention. Concrete
// implementations live in subpackages so each can be replaced by a test double.
package store
