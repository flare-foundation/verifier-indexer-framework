package database

import "time"

type State struct {
	ID                     int `gorm:"primaryKey,unique"`
	LastIndexedBlockNumber uint64
	UpdatedAt              time.Time
}

type Block struct{}

type Transaction struct{}
