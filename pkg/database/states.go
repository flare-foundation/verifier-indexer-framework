package database

import (
	"context"
	"sync"
	"time"

	"github.com/pkg/errors"
	"golang.org/x/sync/errgroup"
	"gorm.io/gorm"
)

const (
	LastChainIndexState     string = "last_chain_block"
	LastDatabaseIndexState  string = "last_database_block"
	FirstDatabaseIndexState string = "first_database_block"
)

var (
	stateNames = []string{
		FirstDatabaseIndexState,
		LastDatabaseIndexState,
		LastChainIndexState,
	}

	// States captures the state of the DB giving guaranties which
	// blocks were indexed. The global variable is used/modified by
	// the indexer as well as the history drop functionality.
	GlobalStates = NewStates()
)

type State struct {
	ID             uint64 `gorm:"primaryKey;unique"`
	Name           string `gorm:"type:varchar(50);index"`
	Index          uint64
	BlockTimestamp uint64
	Updated        time.Time
}

func (s *State) UpdateIndex(newIndex, blockTimestamp uint64) {
	s.Index = newIndex
	s.Updated = time.Now()
}

func (s *State) NewState(newIndex, blockTimestamp uint64) *State {
	return &State{ID: s.ID, Name: s.Name, Index: newIndex, BlockTimestamp: blockTimestamp, Updated: time.Now()}
}

type DBStates struct {
	States map[string]*State
	mu     sync.RWMutex
}

func NewStates() *DBStates {
	return &DBStates{States: make(map[string]*State)}
}

func (s *DBStates) UpdateIndex(name string, newIndex, blockTimestamp uint64) {
	s.mu.Lock()
	s.States[name].UpdateIndex(newIndex, blockTimestamp)
	s.mu.Unlock()
}

func (s *DBStates) DeleteIndex(name string) {
	s.mu.Lock()
	s.States[name] = nil
	s.mu.Unlock()
}

func (states *DBStates) GetAndUpdate(ctx context.Context, db *gorm.DB) error {
	newStates, err := GetDBStates(ctx, db)
	if err != nil {
		return err
	}

	states.UpdateStates(newStates)
	if (states.States[FirstDatabaseIndexState] == nil && states.States[FirstDatabaseIndexState] != nil) ||
		(states.States[FirstDatabaseIndexState] != nil && states.States[FirstDatabaseIndexState] == nil) {
		return errors.New("state error")
	}

	return nil
}

func (s *DBStates) UpdateStates(newStates map[string]*State) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for name, state := range newStates {
		s.States[name] = state
	}
}

func GetDBStates(ctx context.Context, db *gorm.DB) (map[string]*State, error) {
	newStates := make(map[string]*State)
	var mu sync.Mutex
	eg, ctx := errgroup.WithContext(ctx)

	for i := range stateNames {
		name := stateNames[i]

		eg.Go(func() error {
			state := new(State)
			err := db.WithContext(ctx).Where(&State{Name: name}).First(state).Error
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return errors.Wrap(err, "db.Where")
			}
			if errors.Is(err, gorm.ErrRecordNotFound) {
				state = nil
			}

			mu.Lock()
			newStates[name] = state
			mu.Unlock()

			return nil
		})
	}

	if err := eg.Wait(); err != nil {
		return nil, err
	}

	return newStates, nil
}

func (s *DBStates) updateDB(db *gorm.DB, name string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return db.Save(s.States[name]).Error
}

func (s *DBStates) deleteInDB(db *gorm.DB, name string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return db.Delete(s.States[name]).Error
}

func (s *DBStates) Update(db *gorm.DB, name string, newIndex, blockTimestamp uint64) error {
	s.UpdateIndex(name, newIndex, blockTimestamp)
	return s.updateDB(db, name)
}

func (s *DBStates) Delete(db *gorm.DB, name string) error {
	err := s.deleteInDB(db, name)
	if err != nil {
		return err
	}
	s.DeleteIndex(name)

	return nil
}
