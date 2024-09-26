package imds

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap/zaptest"

	"github.com/amargherio/mechanic/internal/appstate"
	"github.com/amargherio/mechanic/internal/config"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type TestCase struct {
	name            string
	mockResponse    ScheduledEventsResponse
	expectedResult  bool
	drainConditions config.DrainConditions
}

func TestCheckIfDrainRequired(t *testing.T) {
	tests := []TestCase{
		{
			name: "empty IMDS response - no events",
			mockResponse: ScheduledEventsResponse{
				IncarnationID: 1,
				Events:        []ScheduledEvent{},
			},
			expectedResult: false,
			drainConditions: config.DrainConditions{
				DrainOnFreeze:    true,
				DrainOnReboot:    true,
				DrainOnRedeploy:  true,
				DrainOnPreempt:   true,
				DrainOnTerminate: true,
			},
		},
		{
			name: "scheduled event that doesn't impact target node",
			mockResponse: ScheduledEventsResponse{
				IncarnationID: 1,
				Events: []ScheduledEvent{
					{
						EventId:      "test",
						Type:         Reboot,
						ResourceType: "VirtualMachine",
						Resources:    []string{"test-vmss_4"},
						EventStatus:  Scheduled,
						NotBefore:    time.Now().Add(1 * time.Hour),
						Description:  "test",
						EventSource:  Platform,
						Duration:     3 * time.Second,
					},
				},
			},
			expectedResult: false,
			drainConditions: config.DrainConditions{
				DrainOnFreeze:    false,
				DrainOnPreempt:   true,
				DrainOnReboot:    true,
				DrainOnRedeploy:  true,
				DrainOnTerminate: true,
			},
		},
		{
			name: "scheduled event that requires drain",
			mockResponse: ScheduledEventsResponse{
				IncarnationID: 11,
				Events: []ScheduledEvent{
					{
						EventId:      "test",
						Type:         Preempt,
						ResourceType: "VirtualMachine",
						Resources:    []string{"test-vmss_1"},
						EventStatus:  Scheduled,
						NotBefore:    time.Now().Add(1 * time.Hour),
						Description:  "good bye spot node",
						EventSource:  Platform,
						Duration:     3 * time.Second,
					},
				},
			},
			expectedResult: true,
			drainConditions: config.DrainConditions{
				DrainOnFreeze:    false,
				DrainOnPreempt:   true,
				DrainOnReboot:    false,
				DrainOnRedeploy:  true,
				DrainOnTerminate: true,
			},
		},
		{
			name: "scheduled event that doesn't require drain",
			mockResponse: ScheduledEventsResponse{
				IncarnationID: 2,
				Events: []ScheduledEvent{
					{
						EventId:      "test",
						Type:         Freeze,
						ResourceType: "VirtualMachine",
						Resources:    []string{"test-vmss_1"},
						EventStatus:  Scheduled,
						NotBefore:    time.Now().Add(1 * time.Hour),
						Description:  "test",
						EventSource:  Platform,
						Duration:     3 * time.Second,
					},
				},
			},
			expectedResult: false,
			drainConditions: config.DrainConditions{
				DrainOnFreeze:    false,
				DrainOnPreempt:   true,
				DrainOnReboot:    false,
				DrainOnRedeploy:  true,
				DrainOnTerminate: true,
			},
		},
		{
			name: "live migration that does requires drain",
			mockResponse: ScheduledEventsResponse{
				IncarnationID: -1,
				Events: []ScheduledEvent{
					{
						Description:  "Virtual machine is being paused because of a memory-preserving Live Migration operation.",
						Duration:     5,
						EventId:      "73578921-FFE4-4A5B-95C7-FEB9BBBB3B09",
						EventSource:  Platform,
						EventStatus:  Scheduled,
						Type:         Freeze,
						NotBefore:    time.Now().Add(1 * time.Hour),
						ResourceType: "VirtualMachine",
						Resources:    []string{"_test-vmss_1"},
					},
				},
			},
			expectedResult: true,
			drainConditions: config.DrainConditions{
				DrainOnFreeze:    false,
				DrainOnPreempt:   true,
				DrainOnReboot:    false,
				DrainOnRedeploy:  true,
				DrainOnTerminate: true,
			},
		},
		{
			name: "non-LM freeze that requires a drain",
			mockResponse: ScheduledEventsResponse{
				IncarnationID: -1,
				Events: []ScheduledEvent{
					{
						Description:  "freeze maintenance",
						Duration:     5,
						EventId:      "73578921-FFE4-4A5B-95C7-FEB9BBBB3B09",
						EventSource:  Platform,
						EventStatus:  Scheduled,
						Type:         Freeze,
						NotBefore:    time.Now().Add(1 * time.Hour),
						ResourceType: "VirtualMachine",
						Resources:    []string{"_test-vmss_1"},
					},
				},
			},
			expectedResult: true,
			drainConditions: config.DrainConditions{
				DrainOnFreeze:    true,
				DrainOnPreempt:   true,
				DrainOnReboot:    false,
				DrainOnRedeploy:  true,
				DrainOnTerminate: true,
			},
		},
		{
			name: "no drain, all events are turned off",
			mockResponse: ScheduledEventsResponse{
				IncarnationID: -1,
				Events: []ScheduledEvent{
					{
						Description:  "reimage",
						Duration:     5,
						EventId:      "73578921-FFE4-4A5B-95C7-FEB9BBBB3B09",
						EventSource:  Platform,
						EventStatus:  Scheduled,
						Type:         Redeploy,
						NotBefore:    time.Now().Add(1 * time.Hour),
						ResourceType: "VirtualMachine",
						Resources:    []string{"_test-vmss_1"},
					},
				},
			},
			expectedResult: false,
			drainConditions: config.DrainConditions{
				DrainOnFreeze:    false,
				DrainOnPreempt:   false,
				DrainOnReboot:    false,
				DrainOnRedeploy:  false,
				DrainOnTerminate: false,
			},
		},
	}

	logger := zaptest.NewLogger(t)
	defer logger.Sync() // flushes buffer, if any
	sugar := logger.Sugar()

	for _, tc := range tests {

		ctrl := gomock.NewController(t)
		t.Run(tc.name, func(t *testing.T) {
			state := appstate.State{
				HasEventScheduled: false,
				IsCordoned:        false,
				IsDrained:         false,
				ShouldDrain:       false,
			}

			vals := config.ContextValues{
				Logger: sugar,
				State:  &state,
			}

			ctx := context.WithValue(context.Background(), "values", vals)

			mockIMDS := configureMocks(tc, ctrl)

			node := &v1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "test-vmss000001"},
			}

			b, err := CheckIfDrainRequired(ctx, mockIMDS, node, &tc.drainConditions)
			if err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
			state.ShouldDrain = b

			assert.Equal(t, tc.expectedResult, state.ShouldDrain)
		})
	}
}

func configureMocks(t TestCase, ctrl *gomock.Controller) *MockIMDS {
	mock := NewMockIMDS(ctrl)
	mock.
		EXPECT().
		QueryIMDS(gomock.Any()).
		Return(t.mockResponse, nil)

	return mock
}
