package database

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

type Version struct {
	ID               uint64 `gorm:"primaryKey;unique"`
	NodeVersion      string
	GitTag           string
	GitHash          string `gorm:"type:varchar(40)"`
	BuildDate        uint64
	NumConfirmations uint64
	HistorySeconds   uint64
}

type Block interface {
	GetTimestamp() uint64
	GetBlockNumber() uint64
	HistoryDropOrder() []Deletable
}

type Transaction any

type Event any
