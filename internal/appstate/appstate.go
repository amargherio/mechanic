package appstate

import "sync"

type State struct {
	Lock              sync.Mutex
	HasEventScheduled bool
	IsCordoned        bool
	IsDrained         bool
	ShouldDrain       bool
}
