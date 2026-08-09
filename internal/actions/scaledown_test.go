package actions

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/youssef-ar/namespace-cost-governor/api/v1alpha1"
	"github.com/youssef-ar/namespace-cost-governor/internal/idle"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func actionTestClient(t *testing.T, objects ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, appsv1.AddToScheme(scheme))
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
}

func deploymentForTest(replicas int32, annotations map[string]string) *appsv1.Deployment {
	return &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "payments-api", Namespace: "default", Annotations: annotations}, Spec: appsv1.DeploymentSpec{Replicas: &replicas}}
}

func TestScaleDown_ThreeReplicas(t *testing.T) {
	c := actionTestClient(t, deploymentForTest(3, nil))
	require.NoError(t, ScaleDown(context.Background(), c, idle.IdleWorkload{Name: "payments-api", Namespace: "default"}))

	got := &appsv1.Deployment{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: "payments-api", Namespace: "default"}, got))
	assert.Equal(t, int32(0), *got.Spec.Replicas)
	assert.Equal(t, "namespace-cost-governor", got.Annotations[AnnotationScaledDownBy])
	assert.NotEmpty(t, got.Annotations[AnnotationScaledDownAt])
	assert.Equal(t, "3", got.Annotations[AnnotationOriginalReplicas])
	assert.Equal(t, "idle:cpu<threshold:30m", got.Annotations[AnnotationReason])
}

func TestScaleDown_AlreadyScaledDownByOperator(t *testing.T) {
	c := actionTestClient(t, deploymentForTest(3, map[string]string{AnnotationScaledDownBy: "namespace-cost-governor"}))
	require.NoError(t, ScaleDown(context.Background(), c, idle.IdleWorkload{Name: "payments-api", Namespace: "default"}))
	got := &appsv1.Deployment{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: "payments-api", Namespace: "default"}, got))
	assert.Equal(t, int32(3), *got.Spec.Replicas)
}

func TestScaleDown_AlreadyAtZero(t *testing.T) {
	c := actionTestClient(t, deploymentForTest(0, nil))
	require.NoError(t, ScaleDown(context.Background(), c, idle.IdleWorkload{Name: "payments-api", Namespace: "default"}))
	got := &appsv1.Deployment{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: "payments-api", Namespace: "default"}, got))
	assert.Nil(t, got.Annotations)
}

func TestScaleDown_NotFound(t *testing.T) {
	err := ScaleDown(context.Background(), actionTestClient(t), idle.IdleWorkload{Name: "missing", Namespace: "default"})
	assert.Error(t, err)
}

func TestRestore_WithOperatorAnnotations(t *testing.T) {
	annotations := map[string]string{AnnotationScaledDownBy: "namespace-cost-governor", AnnotationScaledDownAt: "now", AnnotationOriginalReplicas: "3", AnnotationReason: "idle"}
	c := actionTestClient(t, deploymentForTest(0, annotations))
	require.NoError(t, Restore(context.Background(), c, "default", "payments-api"))
	got := &appsv1.Deployment{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: "payments-api", Namespace: "default"}, got))
	assert.Equal(t, int32(3), *got.Spec.Replicas)
	assert.NotContains(t, got.Annotations, AnnotationScaledDownBy)
	assert.NotContains(t, got.Annotations, AnnotationScaledDownAt)
	assert.NotContains(t, got.Annotations, AnnotationOriginalReplicas)
	assert.NotContains(t, got.Annotations, AnnotationReason)
}

func TestRestore_NotScaledDownByOperator(t *testing.T) {
	c := actionTestClient(t, deploymentForTest(0, nil))
	require.NoError(t, Restore(context.Background(), c, "default", "payments-api"))
	got := &appsv1.Deployment{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: "payments-api", Namespace: "default"}, got))
	assert.Equal(t, int32(0), *got.Spec.Replicas)
}

func TestRestore_MissingOriginalReplicas(t *testing.T) {
	c := actionTestClient(t, deploymentForTest(0, map[string]string{AnnotationScaledDownBy: "namespace-cost-governor"}))
	assert.Error(t, Restore(context.Background(), c, "default", "payments-api"))
}

func TestRestore_InvalidOriginalReplicas(t *testing.T) {
	c := actionTestClient(t, deploymentForTest(0, map[string]string{AnnotationScaledDownBy: "namespace-cost-governor", AnnotationOriginalReplicas: "three"}))
	assert.Error(t, Restore(context.Background(), c, "default", "payments-api"))
}

func TestIsScaledDownByOperator_OperatorAnnotation(t *testing.T) {
	assert.True(t, IsScaledDownByOperator(*deploymentForTest(0, map[string]string{AnnotationScaledDownBy: "namespace-cost-governor"})))
}

func TestIsScaledDownByOperator_MissingAnnotation(t *testing.T) {
	assert.False(t, IsScaledDownByOperator(*deploymentForTest(0, map[string]string{})))
}

func TestIsScaledDownByOperator_NilAnnotations(t *testing.T) {
	assert.False(t, IsScaledDownByOperator(*deploymentForTest(0, nil)))
}
