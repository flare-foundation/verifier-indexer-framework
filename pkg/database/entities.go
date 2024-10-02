package database

type Block interface {
	GetTimestamp() uint64
	GetBlockNumber() uint64
}

type Transaction interface {
}
