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

func (s *State) LockState() {
	s.Lock.Lock()
}

func (s *State) UnlockState() {
	s.Lock.Unlock()
}
