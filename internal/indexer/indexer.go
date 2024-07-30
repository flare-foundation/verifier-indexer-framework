package indexer

import (
	"context"
	"errors"

	"gitlab.com/ryancollingham/flare-indexer-framework/internal/config"
)

func New(cfg *config.Config) *Indexer {
	return &Indexer{}
}

type Indexer struct{}

func (ix *Indexer) Run(ctx context.Context) error {
	return errors.New("not implemented")
}
