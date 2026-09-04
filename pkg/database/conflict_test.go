package database

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm/schema"
)

// noPKEntity is the shape README permits and PostgreSQL rejects.
type noPKEntity struct {
	BlockNumber uint64
	Timestamp   uint64
}

type hashPKEntity struct {
	Hash        string `gorm:"primaryKey"`
	BlockNumber uint64 `gorm:"index"`
	Timestamp   uint64 `gorm:"index"`
}

type surrogatePKEntity struct {
	ID          uint   `gorm:"primaryKey;autoIncrement"`
	Hash        string `gorm:"uniqueIndex"`
	BlockNumber uint64 `gorm:"index"`
}

type nullDefaultEntity struct {
	Hash             string  `gorm:"primaryKey"`
	Response         string  `gorm:"type:varchar"`
	PaymentReference string  `gorm:"index;default:null"`
	DestinationTag   *uint32 `gorm:"default:null"`
}

type pkOnlyEntity struct {
	Hash string `gorm:"primaryKey"`
}

type uniqueOnPKEntity struct {
	ID uint64 `gorm:"primaryKey;unique"`
	// Response is present so the entity has something to overwrite.
	Response string
}

func testNamer() schema.Namer {
	return schema.NamingStrategy{IdentifierMaxLength: 64}
}

func TestOverwriteConflict(t *testing.T) {
	tests := []struct {
		name              string
		model             any
		expectedErr       error
		expectedTarget    []string
		expectedUpdates   []string
		expectedDoNothing bool
		expectedUnmatched []string
	}{
		{
			name:        "no primary key is rejected",
			model:       new(noPKEntity),
			expectedErr: ErrNoPrimaryKey,
		},
		{
			name:            "natural primary key overwrites every other column",
			model:           new(hashPKEntity),
			expectedTarget:  []string{"hash"},
			expectedUpdates: []string{"block_number", "timestamp"},
		},
		{
			name:  "default null columns are overwritten",
			model: new(nullDefaultEntity),
			// gorm's UpdateAll drops these when no row in the batch sets them.
			expectedTarget:  []string{"hash"},
			expectedUpdates: []string{"response", "payment_reference", "destination_tag"},
		},
		{
			name:  "sequence primary key reports the unique index it cannot arbitrate",
			model: new(surrogatePKEntity),
			// id is database-computed, so it is never overwritten.
			expectedTarget:    []string{"id"},
			expectedUpdates:   []string{"hash", "block_number"},
			expectedUnmatched: []string{"idx_surrogate_pk_entities_hash"},
		},
		{
			name:              "entity of only a primary key skips the update",
			model:             new(pkOnlyEntity),
			expectedTarget:    []string{"hash"},
			expectedDoNothing: true,
		},
		{
			name:            "unique on the sole primary key is not a conflicting constraint",
			model:           new(uniqueOnPKEntity),
			expectedTarget:  []string{"id"},
			expectedUpdates: []string{"response"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			conflict, unmatched, err := overwriteConflict(testNamer(), tc.model)
			if tc.expectedErr != nil {
				require.ErrorIs(t, err, tc.expectedErr)
				return
			}

			require.NoError(t, err)

			target := make([]string, 0, len(conflict.Columns))
			for _, col := range conflict.Columns {
				target = append(target, col.Name)
			}
			require.Equal(t, tc.expectedTarget, target, "conflict target must always be named")

			require.Equal(t, tc.expectedDoNothing, conflict.DoNothing)

			updates := make([]string, 0, len(conflict.DoUpdates))
			for _, assignment := range conflict.DoUpdates {
				updates = append(updates, assignment.Column.Name)
			}
			require.ElementsMatch(t, tc.expectedUpdates, updates)

			require.ElementsMatch(t, tc.expectedUnmatched, unmatched)
		})
	}
}
