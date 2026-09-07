package database

// State tracks the indexer's progress and history drop status as a singleton
// database row. First/LastIndexedBlockNumber advertise the contiguous indexed
// range; the range is empty when FirstIndexedBlockNumber is zero or greater
// than LastIndexedBlockNumber. An inverted range persists from resuming past
// unindexed blocks until the first batch is saved, and survives restarts.
type State struct {
	ID                         uint64 `gorm:"primaryKey;unique"`
	LastChainBlockNumber       uint64
	LastChainBlockTimestamp    uint64
	LastIndexedBlockNumber     uint64
	LastIndexedBlockTimestamp  uint64
	FirstIndexedBlockNumber    uint64
	FirstIndexedBlockTimestamp uint64
	LastIndexedBlockUpdated    uint64
	LastChainBlockUpdated      uint64
	LastHistoryDrop            uint64
}

// Version stores build and runtime metadata as a singleton database row.
type Version struct {
	ID               uint64 `gorm:"primaryKey;unique"`
	NodeVersion      string
	GitTag           string
	GitHash          string `gorm:"type:varchar(40)"`
	BuildDate        uint64
	NumConfirmations uint64
	HistorySeconds   uint64
}

// Block defines the interface that user-defined block entities must implement
// for indexing and history pruning.
type Block interface {
	// GetTimestamp returns the block's timestamp as a Unix epoch in seconds.
	GetTimestamp() uint64
	// GetBlockNumber returns the block's sequential number on the chain.
	GetBlockNumber() uint64
	// HistoryDropOrder returns the entities to delete during history pruning,
	// block entity first: a block row must not outlive its transactions. A
	// foreign key onto the block table must cascade.
	HistoryDropOrder() []Deletable
}

// Transaction is intentionally defined as any to allow maximum flexibility
// for implementors. Future versions may introduce a minimal interface.
type Transaction any

// Event is intentionally defined as any to allow maximum flexibility
// for implementors. A struct{} value indicates no events are used.
type Event any
