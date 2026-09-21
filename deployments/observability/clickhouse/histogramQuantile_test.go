package main

import (
	"math"
	"testing"
)

func TestQuantileInterpolatesHistogramBuckets(t *testing.T) {
	got := quantile(0.5, []float64{10, 20, 30, math.Inf(1)}, []float64{1, 3, 4, 4})
	if got != 15 {
		t.Fatalf("quantile() = %v, want 15", got)
	}
}

func TestQuantileReturnsNaNWithoutInfiniteBucket(t *testing.T) {
	got := quantile(0.95, []float64{10, 20}, []float64{1, 2})
	if !math.IsNaN(got) {
		t.Fatalf("quantile() = %v, want NaN", got)
	}
}
