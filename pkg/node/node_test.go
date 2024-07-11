package node

import (
	"context"
	"github.com/stretchr/testify/assert"
	"testing"

	"go.uber.org/zap/zaptest"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestCordonNode(t *testing.T) {
	logger := zaptest.NewLogger(t)
	defer logger.Sync() // flushes buffer, if any
	sugar := logger.Sugar()
	ctx := context.WithValue(context.Background(), "logger", sugar)

	nodeName := "test-node"
	node := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: nodeName},
		Spec:       v1.NodeSpec{Unschedulable: false},
	}

	clientset := fake.NewSimpleClientset(node)

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
			tc.prepNodeFunc(node)

			err := CordonNode(ctx, clientset, node)
			if (err != nil) != tc.expectError {
				t.Errorf("CordonNode() error = %v, expectError %v", err, tc.expectError)
				return
			}
			assert.Equal(t, tc.expectedCordon, node.Spec.Unschedulable, "Expected node.Spec.Unschedulable to be %v, got %v", tc.expectedCordon, node.Spec.Unschedulable)
		})
	}
}

func TestDrainNode(t *testing.T) {
	logger := zaptest.NewLogger(t)
	defer logger.Sync() // flushes buffer, if any
	sugar := logger.Sugar()
	ctx := context.WithValue(context.Background(), "logger", sugar)

	nodeName := "test-node"
	node := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: nodeName},
	}

	clientset := fake.NewSimpleClientset(node)

	tests := []struct {
		name        string
		nodeName    string
		expectError bool
	}{
		{
			name:        "drain success",
			nodeName:    nodeName,
			expectError: false,
		},
		// Additional test cases can be added here
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := DrainNode(ctx, clientset, node)
			if (err != nil) != tc.expectError {
				t.Errorf("DrainNode() error = %v, expectError %v", err, tc.expectError)
			}
		})
	}
}
