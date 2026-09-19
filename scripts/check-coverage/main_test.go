package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSummarizeCoverageWeightsStatements(t *testing.T) {
	profile := strings.NewReader(`mode: atomic
AtoiTalkAPI/internal/example/a.go:1.1,2.1 10 1
AtoiTalkAPI/internal/example/a.go:3.1,4.1 1 0
`)

	summary, err := summarizeCoverage(profile, nil)
	require.NoError(t, err)
	require.Equal(t, 11, summary.totalStatements)
	require.Equal(t, 10, summary.coveredStatements)
	require.InDelta(t, 90.91, summary.percentage(), 0.01)
}

func TestSummarizeCoverageExcludesPackagesFromTotal(t *testing.T) {
	profile := strings.NewReader(`mode: atomic
AtoiTalkAPI/internal/kept/a.go:1.1,2.1 4 1
AtoiTalkAPI/internal/generated/a.go:1.1,2.1 20 0
`)

	summary, err := summarizeCoverage(profile, []string{"internal/generated"})
	require.NoError(t, err)
	require.Equal(t, 4, summary.totalStatements)
	require.Equal(t, 4, summary.coveredStatements)
	require.Equal(t, 100.0, summary.percentage())
}

func TestSummarizeCoverageRejectsMalformedProfile(t *testing.T) {
	_, err := summarizeCoverage(strings.NewReader("mode: atomic\ninvalid\n"), nil)
	require.Error(t, err)
}

func TestCoverageSummaryHandlesEmptyProfile(t *testing.T) {
	summary, err := summarizeCoverage(strings.NewReader("mode: atomic\n"), nil)
	require.NoError(t, err)
	require.Equal(t, 0.0, summary.percentage())
}
