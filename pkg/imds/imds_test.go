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
	name                     string
	mockResponse             ScheduledEventsResponse
	expectedResult           bool
	scheduledDrainConditions config.ScheduledEventDrainConditions
	optionalDrainConditions  config.OptionalDrainConditions
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
			scheduledDrainConditions: config.ScheduledEventDrainConditions{
				Freeze:        true,
				Reboot:        true,
				Redeploy:      true,
				Preempt:       true,
				Terminate:     true,
				LiveMigration: true,
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
			scheduledDrainConditions: config.ScheduledEventDrainConditions{
				Freeze:        false,
				Preempt:       true,
				Reboot:        true,
				Redeploy:      true,
				Terminate:     true,
				LiveMigration: false,
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
			scheduledDrainConditions: config.ScheduledEventDrainConditions{
				Freeze:        false,
				Preempt:       true,
				Reboot:        false,
				Redeploy:      true,
				Terminate:     true,
				LiveMigration: false,
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
			scheduledDrainConditions: config.ScheduledEventDrainConditions{
				Freeze:        false,
				Preempt:       true,
				Reboot:        false,
				Redeploy:      true,
				Terminate:     true,
				LiveMigration: false,
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
			scheduledDrainConditions: config.ScheduledEventDrainConditions{
				Freeze:        false,
				Preempt:       true,
				Reboot:        false,
				Redeploy:      true,
				Terminate:     true,
				LiveMigration: true,
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
			scheduledDrainConditions: config.ScheduledEventDrainConditions{
				Freeze:        true,
				Preempt:       true,
				Reboot:        false,
				Redeploy:      true,
				Terminate:     true,
				LiveMigration: false,
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
			scheduledDrainConditions: config.ScheduledEventDrainConditions{
				Freeze:        false,
				Preempt:       false,
				Reboot:        false,
				Redeploy:      false,
				Terminate:     false,
				LiveMigration: false,
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
				HasDrainableCondition:     false,
				ConditionIsScheduledEvent: false,
				IsCordoned:                false,
				IsDrained:                 false,
				ShouldDrain:               false,
			}

			vals := config.ContextValues{
				Logger: sugar,
				State:  &state,
			}

			ctx := context.WithValue(context.Background(), "values", &vals)

			mockIMDS := configureMocks(tc, ctrl)

			node := &v1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "test-vmss000001"},
			}

			b, err := CheckIfDrainRequired(ctx, mockIMDS, node, &tc.scheduledDrainConditions, &tc.optionalDrainConditions)
			if err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
			state.ShouldDrain = b

			assert.Equal(t, tc.expectedResult, state.ShouldDrain)
		})
	}
}

func TestCheckIfFreezeOrLiveMigration(t *testing.T) {
	tests := []TestCase{
		{
			name: "empty IMDS response - no events",
			mockResponse: ScheduledEventsResponse{
				IncarnationID: 1,
				Events:        []ScheduledEvent{},
			},
			expectedResult: false,
			scheduledDrainConditions: config.ScheduledEventDrainConditions{
				Freeze:        false,
				LiveMigration: true,
			},
		},
		{
			name: "non-freeze scheduled event - should not drain",
			mockResponse: ScheduledEventsResponse{
				IncarnationID: 1,
				Events: []ScheduledEvent{
					{
						EventId:      "test",
						Type:         Reboot,
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
			scheduledDrainConditions: config.ScheduledEventDrainConditions{
				Freeze:        false,
				LiveMigration: true,
			},
		},
		{
			name: "live migration event - should drain",
			mockResponse: ScheduledEventsResponse{
				IncarnationID: 1,
				Events: []ScheduledEvent{
					{
						EventId:      "test",
						Type:         Freeze,
						ResourceType: "VirtualMachine",
						Resources:    []string{"test-vmss_1"},
						EventStatus:  Scheduled,
						NotBefore:    time.Now().Add(1 * time.Hour),
						Description:  "Virtual machine is being paused because of a memory-preserving Live Migration operation.",
						EventSource:  Platform,
						Duration:     3 * time.Second,
					},
				},
			},
			expectedResult: true,
			scheduledDrainConditions: config.ScheduledEventDrainConditions{
				Freeze:        false,
				LiveMigration: true,
			},
		},
		{
			name: "live migration event with LiveMigration disabled - should not drain",
			mockResponse: ScheduledEventsResponse{
				IncarnationID: 1,
				Events: []ScheduledEvent{
					{
						EventId:      "test",
						Type:         Freeze,
						ResourceType: "VirtualMachine",
						Resources:    []string{"test-vmss_1"},
						EventStatus:  Scheduled,
						NotBefore:    time.Now().Add(1 * time.Hour),
						Description:  "Virtual machine is being paused because of a memory-preserving Live Migration operation.",
						EventSource:  Platform,
						Duration:     3 * time.Second,
					},
				},
			},
			expectedResult: false,
			scheduledDrainConditions: config.ScheduledEventDrainConditions{
				Freeze:        false,
				LiveMigration: false,
			},
		},
		{
			name: "regular freeze event - should not drain",
			mockResponse: ScheduledEventsResponse{
				IncarnationID: 1,
				Events: []ScheduledEvent{
					{
						EventId:      "test",
						Type:         Freeze,
						ResourceType: "VirtualMachine",
						Resources:    []string{"test-vmss_1"},
						EventStatus:  Scheduled,
						NotBefore:    time.Now().Add(1 * time.Hour),
						Description:  "Regular freeze maintenance.",
						EventSource:  Platform,
						Duration:     3 * time.Second,
					},
				},
			},
			expectedResult: false,
			scheduledDrainConditions: config.ScheduledEventDrainConditions{
				Freeze:        false,
				LiveMigration: true,
			},
		},
		{
			name: "freeze event for different node - should not drain",
			mockResponse: ScheduledEventsResponse{
				IncarnationID: 1,
				Events: []ScheduledEvent{
					{
						EventId:      "test",
						Type:         Freeze,
						ResourceType: "VirtualMachine",
						Resources:    []string{"test-vmss_2"},
						EventStatus:  Scheduled,
						NotBefore:    time.Now().Add(1 * time.Hour),
						Description:  "Virtual machine is being paused because of a memory-preserving Live Migration operation.",
						EventSource:  Platform,
						Duration:     3 * time.Second,
					},
				},
			},
			expectedResult: false,
			scheduledDrainConditions: config.ScheduledEventDrainConditions{
				Freeze:        false,
				LiveMigration: true,
			},
		},
	}

	logger := zaptest.NewLogger(t)
	defer logger.Sync()
	sugar := logger.Sugar()

	for _, tc := range tests {
		ctrl := gomock.NewController(t)
		t.Run(tc.name, func(t *testing.T) {
			state := appstate.State{
				HasDrainableCondition:     false,
				ConditionIsScheduledEvent: false,
				IsCordoned:                false,
				IsDrained:                 false,
				ShouldDrain:               false,
			}

			vals := config.ContextValues{
				Logger: sugar,
				State:  &state,
			}

			ctx := context.WithValue(context.Background(), "values", &vals)

			mockIMDS := configureMocks(tc, ctrl)

			node := &v1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "test-vmss000001"},
			}

			b, err := CheckIfFreezeOrLiveMigration(ctx, mockIMDS, node, &tc.scheduledDrainConditions)
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
