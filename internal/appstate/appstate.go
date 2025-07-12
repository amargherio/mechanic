package appstate

import "sync"

type State struct {
	Lock                      sync.Mutex
	HasDrainableCondition     bool
	ConditionIsScheduledEvent bool
	IsCordoned                bool
	IsDrained                 bool
	ShouldDrain               bool
}

func (s *State) LockState() bool {
	return s.Lock.TryLock()
}

func (s *State) UnlockState() {
	s.Lock.Unlock()
}
