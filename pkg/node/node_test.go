package node

import (
	"context"
	"fmt"
	"k8s.io/apimachinery/pkg/runtime"
	"testing"

	"github.com/amargherio/mechanic/internal/appstate"
	"github.com/amargherio/mechanic/internal/config"
	"github.com/stretchr/testify/assert"

	"go.uber.org/zap/zaptest"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// Mock for event recorder, required for some of the node operation logic
type MockRecorder struct {
	Events []string
}

func (m *MockRecorder) Event(object runtime.Object, eventtype, reason, message string) {
	m.Events = append(m.Events, eventtype+" "+reason+" "+message)
}

func (m *MockRecorder) Eventf(object runtime.Object, eventtype, reason, messageFmt string, args ...interface{}) {
	m.Events = append(m.Events, eventtype+" "+reason+" "+fmt.Sprintf(messageFmt, args...))
}

func (m *MockRecorder) PastEventf(object runtime.Object, timestamp metav1.Time, eventtype, reason, messageFmt string, args ...interface{}) {
	m.Events = append(m.Events, eventtype+" "+reason+" "+fmt.Sprintf(messageFmt, args...))
}

func (m *MockRecorder) AnnotatedEventf(object runtime.Object, annotations map[string]string, eventtype, reason, messageFmt string, args ...interface{}) {
	m.Events = append(m.Events, eventtype+" "+reason+" "+fmt.Sprintf(messageFmt, args...))
}

func TestCordonNode(t *testing.T) {
	logger := zaptest.NewLogger(t)
	defer logger.Sync() // flushes buffer, if any
	sugar := logger.Sugar()

	tests := []struct {
		name           string
		prepNodeFunc   func(*v1.Node)
		expectError    bool
		expectedCordon bool
	}{
		{
			name: "node already cordoned",
			prepNodeFunc: func(n *v1.Node) {
				n.Spec.Unschedulable = true
			},
			expectError:    false,
			expectedCordon: true,
		},
		{
			name:           "cordon success",
			prepNodeFunc:   func(n *v1.Node) {},
			expectError:    false,
			expectedCordon: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			nodeName := "test-node"
			node := &v1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   nodeName,
					Labels: make(map[string]string),
				},
				Spec: v1.NodeSpec{Unschedulable: false},
			}
			tc.prepNodeFunc(node)

			clientset := fake.NewClientset(node)
			// create the node in the fake clientset
			_, err := clientset.CoreV1().Nodes().Create(context.Background(), node, metav1.CreateOptions{})
			if err != nil {
				t.Errorf("Error creating node: %v", err)
			}

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

			cordoned, err := CordonNode(ctx, clientset, node, &state)
			if (err != nil) != tc.expectError {
				t.Errorf("CordonNode() error = %v, expectError %v", err, tc.expectError)
				return
			}
			state.IsCordoned = cordoned
			updatedNode, _ := clientset.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})

			assert.Equal(t, tc.expectedCordon, updatedNode.Spec.Unschedulable, "Expected node.Spec.Unschedulable to be %v, got %v", tc.expectedCordon, updatedNode.Spec.Unschedulable)
			assert.Equal(t, tc.expectedCordon, state.IsCordoned, "Expected state.IsCordoned to be %v, got %v", tc.expectedCordon, state.IsCordoned)
		})
	}
}

func TestDrainNode(t *testing.T) {
	logger := zaptest.NewLogger(t)
	defer logger.Sync() // flushes buffer, if any
	sugar := logger.Sugar()

	nodeName := "test-node"

	tests := []struct {
		name          string
		nodeName      string
		expectError   bool
		expectedState bool
	}{
		{
			name:          "drain success",
			nodeName:      nodeName,
			expectError:   false,
			expectedState: true,
		},
	}

	for _, tc := range tests {
		node := &v1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name:   nodeName,
				Labels: make(map[string]string),
			},
		}
		clientset := fake.NewClientset(node)

		t.Run(tc.name, func(t *testing.T) {
			state := appstate.State{
				HasEventScheduled: true,
				IsCordoned:        true,
				IsDrained:         false,
				ShouldDrain:       true,
			}

			vals := config.ContextValues{
				Logger: sugar,
				State:  &state,
			}

			ctx := context.WithValue(context.Background(), "values", vals)

			drained, err := DrainNode(ctx, clientset, node)
			if (err != nil) != tc.expectError {
				t.Errorf("DrainNode() error = %v, expectError %v", err, tc.expectError)
			}
			state.IsDrained = drained

			assert.Equal(t, tc.expectedState, state.IsDrained, "Expected state.IsDrained to be %v, got %v", tc.expectedState, state.IsDrained)
		})
	}
}

func TestValidateCordon(t *testing.T) {
	logger := zaptest.NewLogger(t)
	defer logger.Sync() // flushes buffer, if any
	log := logger.Sugar()

	tests := []struct {
		name          string
		prepNodeFunc  func(*v1.Node)
		expectedState appstate.State
		expectedNode  *v1.Node
		inputState    appstate.State
	}{
		{
			name: "node cordoned, state uncordoned, upcoming event",
			prepNodeFunc: func(n *v1.Node) {
				n.Spec.Unschedulable = true
			},
			inputState: appstate.State{
				HasEventScheduled: true,
				IsCordoned:        false,
			},
			expectedState: appstate.State{
				HasEventScheduled: true,
				IsCordoned:        true,
			},
			expectedNode: &v1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node",
				},
				Spec: v1.NodeSpec{Unschedulable: true},
			},
		},
		{
			name: "node uncordoned, state cordoned, upcoming event",
			prepNodeFunc: func(node *v1.Node) {
				node.Spec.Unschedulable = false
			},
			inputState: appstate.State{
				HasEventScheduled: true,
				IsCordoned:        true,
			},
			expectedState: appstate.State{
				HasEventScheduled: true,
				IsCordoned:        true,
			},
			expectedNode: &v1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node",
				},
				Spec: v1.NodeSpec{Unschedulable: true},
			},
		},
		{
			name: "node cordoned, state uncordoned, no upcoming event",
			prepNodeFunc: func(node *v1.Node) {
				node.Spec.Unschedulable = true
			},
			expectedState: appstate.State{
				HasEventScheduled: false,
				IsCordoned:        false,
			},
			expectedNode: &v1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "test-node"},
				Spec:       v1.NodeSpec{Unschedulable: true},
			},
			inputState: appstate.State{
				HasEventScheduled: false,
				IsCordoned:        false,
			},
		},
		{
			name: "node cordoned, state uncordoned, no upcoming event, mechanic managed",
			prepNodeFunc: func(node *v1.Node) {
				node.ObjectMeta.Labels["mechanic.cordoned"] = "true"
				node.Spec.Unschedulable = true
			},
			expectedState: appstate.State{
				HasEventScheduled: false,
				IsCordoned:        false,
			},
			expectedNode: &v1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "test-node"},
				Spec:       v1.NodeSpec{Unschedulable: false},
			},
			inputState: appstate.State{
				HasEventScheduled: false,
				IsCordoned:        false,
			},
		},
		{
			name: "node uncordoned, state cordoned, no upcoming event",
			prepNodeFunc: func(node *v1.Node) {
				node.ObjectMeta.Labels["mechanic.cordoned"] = "true"
				node.Spec.Unschedulable = false
			},
			inputState: appstate.State{
				HasEventScheduled: false,
				IsCordoned:        true,
			},
			expectedState: appstate.State{
				HasEventScheduled: false,
				IsCordoned:        false,
			},
			expectedNode: &v1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "test-node"},
				Spec:       v1.NodeSpec{Unschedulable: false},
			},
		},
		{
			name: "node uncordoned, state uncordoned, no upcoming event",
			prepNodeFunc: func(node *v1.Node) {
				node.Spec.Unschedulable = false
			},
			inputState: appstate.State{
				HasEventScheduled: false,
				IsCordoned:        false,
			},
			expectedState: appstate.State{
				HasEventScheduled: false,
				IsCordoned:        false,
			},
			expectedNode: &v1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "test-node"},
				Spec:       v1.NodeSpec{Unschedulable: false},
			},
		},
		{
			name: "node cordoned, state cordoned, no upcoming event",
			prepNodeFunc: func(node *v1.Node) {
				node.ObjectMeta.Labels["mechanic.cordoned"] = "true"
				node.Spec.Unschedulable = true
			},
			inputState: appstate.State{
				HasEventScheduled: false,
				IsCordoned:        true,
			},
			expectedState: appstate.State{
				HasEventScheduled: false,
				IsCordoned:        false,
			},
			expectedNode: &v1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node",
				},
				Spec: v1.NodeSpec{Unschedulable: false},
			},
		},
		{
			name: "should reset drain state when uncordoning a node we manage",
			prepNodeFunc: func(node *v1.Node) {
				node.ObjectMeta.Labels["mechanic.cordoned"] = "true"
				node.Spec.Unschedulable = false
			},
			inputState: appstate.State{
				HasEventScheduled: false,
				IsCordoned:        true,
				IsDrained:         true,
				ShouldDrain:       true,
			},
			expectedState: appstate.State{
				HasEventScheduled: false,
				IsCordoned:        false,
				IsDrained:         false,
				ShouldDrain:       false,
			},
			expectedNode: &v1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "test-node"},
				Spec:       v1.NodeSpec{Unschedulable: false},
			},
		},
	}

	for _, tc := range tests {
		recorder := &MockRecorder{}

		t.Run(tc.name, func(t *testing.T) {
			vals := config.ContextValues{
				Logger: log,
				State:  &tc.inputState,
			}

			ctx := context.WithValue(context.Background(), "values", vals)

			nodeName := "test-node"
			node := &v1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   nodeName,
					Labels: make(map[string]string),
				},
				Spec: v1.NodeSpec{Unschedulable: false},
			}
			tc.prepNodeFunc(node)
			clientset := fake.NewClientset(node)

			_, err := clientset.CoreV1().Nodes().Create(context.Background(), node, metav1.CreateOptions{})
			if err != nil {
				t.Errorf("Error creating node: %v", err)
			}

			ValidateCordon(ctx, clientset, node, recorder, &tc.inputState)
			updatedNode, _ := clientset.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})

			assert.Equal(t, tc.expectedState, tc.inputState, "Expected state to be %v, got %v", tc.expectedState, tc.inputState)
			assert.Equal(t, tc.expectedNode.Spec.Unschedulable, updatedNode.Spec.Unschedulable, "Expected node.Spec.Unschedulable to be %v, got %v", tc.expectedNode.Spec.Unschedulable, updatedNode.Spec.Unschedulable)
		})
	}
}

func TestCheckNodeConditions(t *testing.T) {
	logger := zaptest.NewLogger(t)
	defer logger.Sync() // flushes buffer, if any
	log := logger.Sugar()

	tests := []struct {
		name             string
		prepNodeFunc     func(*v1.Node)
		expectedResponse bool
	}{
		{
			name: "node has VMScheduledEvent",
			prepNodeFunc: func(n *v1.Node) {
				n.Status.Conditions = append(n.Status.Conditions, v1.NodeCondition{
					Type:   v1.NodeConditionType("VMEventScheduled"),
					Status: v1.ConditionTrue,
				})
			},
			expectedResponse: true,
		},
		{
			name: "node has no VMScheduledEvent",
			prepNodeFunc: func(n *v1.Node) {
			},
			expectedResponse: false,
		},
	}

	for _, tc := range tests {
		nodeName := "test-node"
		node := &v1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: nodeName},
			Spec:       v1.NodeSpec{Unschedulable: false},
		}

		t.Run(tc.name, func(t *testing.T) {
			vals := config.ContextValues{
				Logger: log,
				State: &appstate.State{
					HasEventScheduled: false, IsCordoned: false, ShouldDrain: false, IsDrained: false},
			}
			ctx := context.WithValue(context.Background(), "values", vals)

			tc.prepNodeFunc(node)
			response := CheckNodeConditions(ctx, node)
			assert.Equal(t, tc.expectedResponse, response, "Expected response to be %v, got %v", tc.expectedResponse, response)
		})
	}
}
