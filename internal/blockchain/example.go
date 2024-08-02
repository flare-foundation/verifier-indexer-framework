package blockchain

import (
	"context"
	"errors"

	"gitlab.com/ryancollingham/flare-indexer-framework/internal/indexer"
)

type Example struct{}

func NewExample() Example {
	return Example{}
}

func (e Example) GetLatestBlockNumber(context.Context) (uint64, error) {
	return 0, errors.New("not implemented")
}

func (e Example) GetBlockResult(context.Context, uint64) (*indexer.BlockResult, error) {
	return nil, errors.New("not implemented")
}