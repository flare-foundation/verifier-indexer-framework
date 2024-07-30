package indexer

import (
	"context"
	"errors"

	"gitlab.com/ryancollingham/flare-indexer-framework/internal/config"
	"gitlab.com/ryancollingham/flare-indexer-framework/internal/database"
)

func New(cfg *config.Config) (*Indexer, error) {
	db, err := database.New(cfg.DB)
	if err != nil {
		return nil, err
	}

	return &Indexer{db: db}, nil
}

type Indexer struct {
	db *database.DB
}

func (ix *Indexer) Run(ctx context.Context) error {
	return errors.New("not implemented")
}
