package testsupport_test

import (
	"github.com/andreswebs/feedwatch/internal/testsupport"
	"github.com/andreswebs/feedwatch/store"
)

// Compile-time conformance: the wrapper double satisfies the consumer
// interface, so a change to Store surfaces here rather than at a call site.
var _ store.Store = (*testsupport.FailingUpsertStore)(nil)
