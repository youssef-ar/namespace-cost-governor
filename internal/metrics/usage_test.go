package metrics

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestComputeCoreHours(t *testing.T) {
	samples := []Sample{
		{Pod: "a", TimeStamp: time.Now(), Value: 0.5},
		{Pod: "b", TimeStamp: time.Now(), Value: 1.0},
		{Pod: "c", TimeStamp: time.Now(), Value: 1.5},
	}
	got := ComputeCoreHours(samples, 2)
	want := (0.5 + 1.0 + 1.5) / 3 * 2
	if got != want {
		t.Errorf("ComputeCoreHours = %f, want %f", got, want)
	}
}

func TestComputeCoreHours_Empty(t *testing.T) {
	got := ComputeCoreHours(nil, 2)
	if got != 0 {
		t.Errorf("ComputeCoreHours = %f, want 0", got)
	}
}

func TestComputeGiBHours(t *testing.T) {
	samples := []Sample{
		{Pod: "a", TimeStamp: time.Now(), Value: float64(1 << 30)},
		{Pod: "b", TimeStamp: time.Now(), Value: float64(2 << 30)},
	}
	got := ComputeGiBHours(samples, 3)
	want := (1.0 + 2.0) / 2 * 3
	if got != want {
		t.Errorf("ComputeGiBHours = %f, want %f", got, want)
	}
}

func TestComputeGiBHours_Empty(t *testing.T) {
	got := ComputeGiBHours(nil, 3)
	if got != 0 {
		t.Errorf("ComputeGiBHours = %f, want 0", got)
	}
}

func TestAverageCPUPercent(t *testing.T) {
	samples := []Sample{
		{Pod: "a", TimeStamp: time.Now(), Value: 0.25},
		{Pod: "b", TimeStamp: time.Now(), Value: 0.75},
	}
	got := AverageCPUPercent(samples)
	want := (0.25 + 0.75) / 2 * 100
	if got != want {
		t.Errorf("AverageCPUPercent = %f, want %f", got, want)
	}
}

func TestAverageCPUPercent_Empty(t *testing.T) {
	got := AverageCPUPercent(nil)
	if got != 0 {
		t.Errorf("AverageCPUPercent = %f, want 0", got)
	}
}

func TestSumValues_MultipleSamples(t *testing.T) {
	got := SumValues([]Sample{{Value: 1.25}, {Value: 2.5}, {Value: 0.25}})
	assert.Equal(t, 4.0, got)
}

func TestSumValues_Empty(t *testing.T) {
	assert.Equal(t, 0.0, SumValues(nil))
}

func TestSumValues_SingleSample(t *testing.T) {
	assert.Equal(t, 3.75, SumValues([]Sample{{Value: 3.75}}))
}

func TestSumValues_ZeroValues(t *testing.T) {
	assert.Equal(t, 0.0, SumValues([]Sample{{Value: 0}, {Value: 0}}))
}
