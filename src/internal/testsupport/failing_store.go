package testsupport

import (
	"context"
	"errors"

	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/andreswebs/feedwatch/internal/store"
)

// FailingUpsertStore wraps a store.Store and forces UpsertItems to fail for a
// chosen feed URL, simulating a hard write failure partway through a poll's
// persistence stage while every other feed persists normally.
type FailingUpsertStore struct {
	store.Store
	FailURL string
}

// UpsertItems fails with a sentinel error for FailURL, delegating to the
// wrapped store for every other feed.
func (s *FailingUpsertStore) UpsertItems(ctx context.Context, feedURL string, items []core.Item) ([]core.Item, error) {
	if feedURL == s.FailURL {
		return nil, errors.New("simulated store write failure")
	}
	return s.Store.UpsertItems(ctx, feedURL, items)
}
