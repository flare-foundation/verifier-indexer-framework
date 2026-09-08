package database

import (
	"fmt"
	"strings"

	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"
)

// entityConflicts holds each entity table's ON CONFLICT clause.
type entityConflicts struct {
	block       clause.OnConflict
	transaction clause.OnConflict
	event       clause.OnConflict
}

// skipConflict is v1.1.1's clause: a conflicting row is skipped, never repaired.
var skipConflict = clause.OnConflict{DoNothing: true}

// conflictClause returns the entity's ON CONFLICT clause: an overwrite on the
// primary key where the schema allows one, otherwise skipConflict with the
// reason, so New can warn.
//
// The update set comes from the schema, not the rows: gorm's UpdateAll set omits
// every column no row in the current batch populates, freezing it.
func conflictClause(namer schema.Namer, model any) (clause.OnConflict, string, error) {
	s, err := parseSchema(namer, model)
	if err != nil {
		return clause.OnConflict{}, "", err
	}

	// PostgreSQL needs a conflict target to update, and the primary key is the
	// only index AutoMigrate guarantees.
	if len(s.PrimaryFieldDBNames) == 0 {
		return skipConflict, "no primary key", nil
	}

	if unmatched := unmatchedUniqueIndexes(s); len(unmatched) != 0 {
		return skipConflict, fmt.Sprintf(
			"unique constraints the primary key cannot arbitrate (%s)", strings.Join(unmatched, ", ")), nil
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

	// An entity of nothing but its primary key carries no values to repair.
	if len(updates) == 0 {
		return clause.OnConflict{Columns: target, DoNothing: true}, "", nil
	}

	return clause.OnConflict{
		Columns:   target,
		DoUpdates: clause.AssignmentColumns(updates),
	}, "", nil
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
