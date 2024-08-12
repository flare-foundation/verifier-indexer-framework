package database

import (
	"time"
)

type ExternalEntities struct {
	Block       interface{}
	Transaction interface{}
}

type State struct {
	ID                     int `gorm:"primaryKey"`
	LastIndexedBlockNumber uint64
	UpdatedAt              time.Time
}

type BaseBlock struct {
	Hash      string `gorm:"primaryKey;type:varchar(66)"`
	Number    uint64 `gorm:"index"`
	Timestamp uint64 `gorm:"index"`
}

type BaseTransaction struct {
	Hash      string `gorm:"primaryKey;type:varchar(66)"`
	Timestamp uint64 `gorm:"index"`
}
