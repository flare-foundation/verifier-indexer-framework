package database

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// txFirstBlock declares the fail-unsafe order: its transactions go first.
type txFirstBlock struct {
	Hash      string `gorm:"primaryKey;type:varchar(64)"`
	Timestamp uint64 `gorm:"index"`
}

func (b txFirstBlock) GetBlockNumber() uint64        { return 0 }
func (b txFirstBlock) GetTimestamp() uint64          { return b.Timestamp }
func (b txFirstBlock) TimestampField() string        { return "timestamp" }
func (b txFirstBlock) HistoryDropOrder() []Deletable { return []Deletable{&ptrTx{}, txFirstBlock{}} }

// blocklessOrderBlock prunes its transactions but never its own table.
type blocklessOrderBlock struct {
	Hash      string `gorm:"primaryKey;type:varchar(64)"`
	Timestamp uint64 `gorm:"index"`
}

func (b blocklessOrderBlock) GetBlockNumber() uint64        { return 0 }
func (b blocklessOrderBlock) GetTimestamp() uint64          { return b.Timestamp }
func (b blocklessOrderBlock) TimestampField() string        { return "timestamp" }
func (b blocklessOrderBlock) HistoryDropOrder() []Deletable { return []Deletable{&ptrTx{}} }

// soloBlock prunes only its own table.
type soloBlock struct {
	Hash      string `gorm:"primaryKey;type:varchar(64)"`
	Timestamp uint64 `gorm:"index"`
}

func (b soloBlock) GetBlockNumber() uint64        { return 0 }
func (b soloBlock) GetTimestamp() uint64          { return b.Timestamp }
func (b soloBlock) TimestampField() string        { return "timestamp" }
func (b soloBlock) HistoryDropOrder() []Deletable { return []Deletable{soloBlock{}} }

func TestValidateHistoryDropOrder(t *testing.T) {
	tests := []struct {
		name     string
		validate func() error
		wantErr  string
	}{
		{
			// New probes *new(B), a nil pointer for a pointer B.
			name:     "pointer block first",
			validate: func() error { return validateHistoryDropOrder[*ptrBlock](testNamer(), nil) },
		},
		{
			name:     "value block listed through its pointer",
			validate: func() error { return validateHistoryDropOrder(testNamer(), orderPtrBlock{}) },
		},
		{
			name:     "block only",
			validate: func() error { return validateHistoryDropOrder(testNamer(), soloBlock{}) },
		},
		{
			name:     "empty order",
			validate: func() error { return validateHistoryDropOrder(testNamer(), halfPtrBlock{}) },
			wantErr:  "HistoryDropOrder is empty",
		},
		{
			name:     "block table missing",
			validate: func() error { return validateHistoryDropOrder(testNamer(), blocklessOrderBlock{}) },
			wantErr:  "does not include the block table",
		},
		{
			name:     "transactions first",
			validate: func() error { return validateHistoryDropOrder(testNamer(), txFirstBlock{}) },
			wantErr:  "must list the block table",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.validate()
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}

			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}
