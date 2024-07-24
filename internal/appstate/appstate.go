package appstate

type State struct {
	HasEventScheduled bool
	IsCordoned        bool
	IsDrained         bool
	ShouldDrain       bool
}
