package database

import "time"

type State struct {
	ID                         uint64 `gorm:"primaryKey;unique"`
	LastChainBlockNumber       uint64
	LastChainBlockTimestamp    uint64
	LastIndexedBlockNumber     uint64
	LastIndexedBlockTimestamp  uint64
	FirstIndexedBlockNumber    uint64
	FirstIndexedBlockTimestamp uint64
	Updated                    time.Time
}

type Block interface {
	GetTimestamp() uint64
	GetBlockNumber() uint64
}

// TODO empty interface ??
type Transaction interface {
}
