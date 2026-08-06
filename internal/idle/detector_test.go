package idle

import (
	"testing"

	"github.com/stretchr/testify/assert"
	costv1alpha1 "github.com/youssef-ar/namespace-cost-governor/api/v1alpha1"
	"github.com/youssef-ar/namespace-cost-governor/internal/metrics"
)

func TestIsExcluded_ExactMatch(t *testing.T) {
	assert.True(t, IsExcluded("payments-db", []costv1alpha1.Exclusion{{Name: "payments-db"}}))
}

func TestIsExcluded_NameDoesNotMatch(t *testing.T) {
	assert.False(t, IsExcluded("payments-api", []costv1alpha1.Exclusion{{Name: "payments-db"}}))
}

func TestIsExcluded_EmptyExclusions(t *testing.T) {
	assert.False(t, IsExcluded("payments-api", nil))
}

func TestIsExcluded_PartialMatch(t *testing.T) {
	assert.False(t, IsExcluded("payments-db-replica", []costv1alpha1.Exclusion{{Name: "payments-db"}}))
}

func TestIsExcluded_MatchesOneOfMultiple(t *testing.T) {
	assert.True(t, IsExcluded("payments-db", []costv1alpha1.Exclusion{{Name: "payments-api"}, {Name: "payments-db"}}))
}

func TestAverageByPod_MultipleSamplesForSamePod(t *testing.T) {
	got := averageByPod([]metrics.Sample{{Pod: "pod-a", Value: 2}, {Pod: "pod-a", Value: 4}})
	assert.Equal(t, 3.0, got["pod-a"])
}

func TestAverageByPod_OneSamplePerPod(t *testing.T) {
	got := averageByPod([]metrics.Sample{{Pod: "pod-a", Value: 2.5}, {Pod: "pod-b", Value: 7}})
	assert.Equal(t, map[string]float64{"pod-a": 2.5, "pod-b": 7}, got)
}

func TestAverageByPod_MultiplePodsIndependently(t *testing.T) {
	got := averageByPod([]metrics.Sample{{Pod: "pod-a", Value: 2}, {Pod: "pod-a", Value: 6}, {Pod: "pod-b", Value: 10}, {Pod: "pod-b", Value: 14}})
	assert.Equal(t, map[string]float64{"pod-a": 4, "pod-b": 12}, got)
}

func TestPodToDeployment_TwoSuffixes(t *testing.T) {
	assert.Equal(t, "payments-api", PodToDeployment("payments-api-6d4f9b-xkq2p"))
}

func TestPodToDeployment_NoSuffix(t *testing.T) {
	assert.Equal(t, "payments", PodToDeployment("payments"))
}

func TestPodToDeployment_OneSuffix(t *testing.T) {
	assert.Equal(t, "payments-api", PodToDeployment("payments-api"))
}
