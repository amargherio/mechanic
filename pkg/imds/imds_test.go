package imds

import (
	"context"
	"go.uber.org/zap/zaptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type TestCase struct {
	name           string
	mockResponse   ScheduledEventsResponse
	expectedResult bool
}

func TestCheckIfDrainRequired(t *testing.T) {
	tests := []TestCase{
		{
			name: "empty IMDS response - no events",
			mockResponse: ScheduledEventsResponse{
				IncarnationID: "1",
				Events:        []ScheduledEvent{},
			},
			expectedResult: false,
		},
		{
			name: "scheduled event that doesn't impact target node",
			mockResponse: ScheduledEventsResponse{
				IncarnationID: "1",
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
		},
		{
			name: "scheduled event that requires drain",
			mockResponse: ScheduledEventsResponse{
				IncarnationID: "1",
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
		},
		{
			name: "scheduled event that doesn't require drain",
			mockResponse: ScheduledEventsResponse{
				IncarnationID: "1",
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
		},
		{
			name: "live migration that requires drain",
			mockResponse: ScheduledEventsResponse{
				IncarnationID: "1",
				Events: []ScheduledEvent{
					{
						EventId:      "test",
						Type:         Freeze,
						ResourceType: "VirtualMachine",
						Resources:    []string{"test-vmss_1"},
						EventStatus:  Scheduled,
						NotBefore:    time.Now().Add(1 * time.Hour),
						Description:  "memory-preserving Live Migration blah blah",
						EventSource:  Platform,
						Duration:     3 * time.Second,
					},
				},
			},
			expectedResult: true,
		},
	}

	logger := zaptest.NewLogger(t)
	defer logger.Sync() // flushes buffer, if any
	sugar := logger.Sugar()
	ctx := context.WithValue(context.Background(), "logger", sugar)

	for _, tc := range tests {

		ctrl := gomock.NewController(t)
		t.Run(tc.name, func(t *testing.T) {
			mockIMDS := configureMocks(tc, ctrl)

			node := &v1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "test-vmss000001"},
			}

			result, err := CheckIfDrainRequired(ctx, mockIMDS, node)
			if err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
			assert.Equal(t, tc.expectedResult, result)
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
