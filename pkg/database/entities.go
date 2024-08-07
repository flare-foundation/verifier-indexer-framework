package database

import (
	"time"

	"gorm.io/datatypes"
)

var entities = []interface{}{
	State{},
	Block{},
	Transaction{},
}

type State struct {
	ID                     int `gorm:"primaryKey;unique"`
	LastIndexedBlockNumber uint64
	UpdatedAt              time.Time
}

type Block struct {
	Hash      string `gorm:"type:varchar(66);index;unique"`
	Number    uint64 `gorm:"index"`
	Timestamp uint64 `gorm:"index"`

	// Any extra chain-specific data goes here.
	ChainAttributes datatypes.JSON
}

type Transaction struct {
	Hash        string `gorm:"primaryKey;type:varchar(66);index;unique"`
	BlockHash   string `gorm:"type:varchar(66)"`
	Block       *Block
	FromAddress string `gorm:"type:varchar(42);index"`
	ToAddress   string `gorm:"type:varchar(42);index"`
	Timestamp   uint64 `gorm:"index"`

	// Any extra chain-specific data goes here.
	ChainAttributes datatypes.JSON
}
