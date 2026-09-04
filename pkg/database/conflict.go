package database

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"
)

// ErrNoPrimaryKey reports an entity with no primary key. PostgreSQL needs a
// conflict target, and the primary key is the only index AutoMigrate guarantees.
var ErrNoPrimaryKey = errors.New("entity has no primary key")

// entityConflicts holds each entity table's ON CONFLICT clause.
type entityConflicts struct {
	block       clause.OnConflict
	transaction clause.OnConflict
	event       clause.OnConflict
}

// overwriteConflict returns the ON CONFLICT clause overwriting an existing row
// of the entity, plus the unique constraints it cannot arbitrate.
//
// The update set comes from the schema, not the rows: gorm's UpdateAll set omits
// every column no row in the current batch populates, freezing it.
func overwriteConflict(namer schema.Namer, model any) (clause.OnConflict, []string, error) {
	s, err := schema.Parse(model, &sync.Map{}, namer)
	if err != nil {
		return clause.OnConflict{}, nil, fmt.Errorf("failed to parse entity schema: %w", err)
	}

	if len(s.PrimaryFieldDBNames) == 0 {
		return clause.OnConflict{}, nil, fmt.Errorf("%w: %s", ErrNoPrimaryKey, s.Table)
	}

	target := make([]clause.Column, 0, len(s.PrimaryFieldDBNames))
	for _, name := range s.PrimaryFieldDBNames {
		target = append(target, clause.Column{Name: name})
	}

	updates := make([]string, 0, len(s.DBNames))
	for _, name := range s.DBNames {
		if overwritableColumn(s.FieldsByDBName[name]) {
			updates = append(updates, name)
		}
	}

	unmatched := unmatchedUniqueIndexes(s)

	// An entity of nothing but its primary key carries no values to repair.
	if len(updates) == 0 {
		return clause.OnConflict{Columns: target, DoNothing: true}, unmatched, nil
	}

	return clause.OnConflict{
		Columns:   target,
		DoUpdates: clause.AssignmentColumns(updates),
	}, unmatched, nil
}

// overwritableColumn reports whether re-indexing owns a column. A
// database-computed default (serial, now()) does not; `default:null` only marks
// nullability, so the indexer still owns it.
func overwritableColumn(field *schema.Field) bool {
	if field.PrimaryKey || !field.Creatable || !field.Updatable || field.AutoCreateTime != 0 {
		return false
	}

	return !field.HasDefaultValue ||
		field.DefaultValueInterface != nil ||
		strings.EqualFold(field.DefaultValue, "null")
}

// unmatchedUniqueIndexes returns the entity's unique constraints the
// primary-key target does not cover; violating one raises a unique violation
// instead of updating. Constraints declared outside entity tags are invisible.
func unmatchedUniqueIndexes(s *schema.Schema) []string {
	primary := make(map[string]struct{}, len(s.PrimaryFieldDBNames))
	for _, name := range s.PrimaryFieldDBNames {
		primary[name] = struct{}{}
	}

	var unmatched []string

	for _, name := range s.DBNames {
		if !s.FieldsByDBName[name].Unique {
			continue
		}

		// `unique` on a sole primary-key column is redundant, not conflicting.
		if _, isPrimary := primary[name]; isPrimary && len(primary) == 1 {
			continue
		}

		unmatched = append(unmatched, name)
	}

	for _, index := range s.ParseIndexes() {
		if index.Class != "UNIQUE" || arbitratesPrimaryKey(index, primary) {
			continue
		}

		unmatched = append(unmatched, index.Name)
	}

	return unmatched
}

// arbitratesPrimaryKey reports whether the index covers exactly the primary key.
func arbitratesPrimaryKey(index *schema.Index, primary map[string]struct{}) bool {
	if len(index.Fields) != len(primary) {
		return false
	}

	for _, field := range index.Fields {
		if _, ok := primary[field.DBName]; !ok {
			return false
		}
	}

	return true
}
