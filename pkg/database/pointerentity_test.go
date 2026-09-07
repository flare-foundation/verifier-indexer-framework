package database

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// ptrBlock mirrors a consumer that instantiates the framework with *ptrBlock:
// every method is on the pointer receiver.
type ptrBlock struct {
	Hash        string `gorm:"primaryKey;type:varchar(64)"`
	BlockNumber uint64 `gorm:"index"`
	Timestamp   uint64 `gorm:"index"`
}

func (b *ptrBlock) GetBlockNumber() uint64 { return b.BlockNumber }
func (b *ptrBlock) GetTimestamp() uint64   { return b.Timestamp }
func (b *ptrBlock) TimestampField() string { return "timestamp" }
func (b *ptrBlock) HistoryDropOrder() []Deletable {
	return []Deletable{&ptrBlock{}, &ptrTx{}}
}

type ptrTx struct {
	Hash        string `gorm:"primaryKey;type:varchar(64)"`
	BlockNumber uint64 `gorm:"index"`
	Timestamp   uint64 `gorm:"index"`
}

func (t *ptrTx) TimestampField() string { return "timestamp" }

// halfPtrBlock satisfies Block by value but declares TimestampField on the
// pointer receiver — the shape the README used to describe as required.
type halfPtrBlock struct {
	BlockNumber uint64 `gorm:"primaryKey"`
	Timestamp   uint64 `gorm:"index"`
}

func (b halfPtrBlock) GetBlockNumber() uint64        { return b.BlockNumber }
func (b halfPtrBlock) GetTimestamp() uint64          { return b.Timestamp }
func (b halfPtrBlock) HistoryDropOrder() []Deletable { return nil }
func (b *halfPtrBlock) TimestampField() string       { return "timestamp" }

// orderPtrBlock is the v1.1.1 shape: instantiated by value, pruned through the
// pointer it lists in its own HistoryDropOrder.
type orderPtrBlock struct {
	BlockNumber uint64 `gorm:"primaryKey"`
	Timestamp   uint64 `gorm:"index"`
}

func (b orderPtrBlock) GetBlockNumber() uint64        { return b.BlockNumber }
func (b orderPtrBlock) GetTimestamp() uint64          { return b.Timestamp }
func (b orderPtrBlock) HistoryDropOrder() []Deletable { return []Deletable{&orderPtrBlock{}, &ptrTx{}} }
func (b *orderPtrBlock) TimestampField() string       { return "closed_at" }

func TestBlockTimestampFieldUsesInstantiatedMethodSet(t *testing.T) {
	// New probes *new(B), a nil pointer for a pointer B, so the method must
	// resolve without dereferencing.
	col, err := blockTimestampField[*ptrBlock](testNamer(), nil)
	require.NoError(t, err)
	require.Equal(t, "timestamp", col)

	// The v1.1.1 fallback: the HistoryDropOrder entry for the block's own table.
	col, err = blockTimestampField(testNamer(), orderPtrBlock{})
	require.NoError(t, err)
	require.Equal(t, "closed_at", col)

	_, err = blockTimestampField(testNamer(), halfPtrBlock{})
	require.ErrorContains(t, err, "does not implement database.Deletable")
}

// TestOverwriteConflictAcceptsPointerEntity pins schema parsing of the **B that
// deriveConflicts hands over for a pointer block type.
func TestOverwriteConflictAcceptsPointerEntity(t *testing.T) {
	conflict, skipReason, err := conflictClause(testNamer(), new(*ptrBlock))
	require.NoError(t, err)
	require.Empty(t, skipReason)
	require.Len(t, conflict.Columns, 1)
	require.Equal(t, "hash", conflict.Columns[0].Name)
	require.False(t, conflict.DoNothing)
}
