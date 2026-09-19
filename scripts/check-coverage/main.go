package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type packageCoverage struct {
	coveredStatements int
	totalStatements   int
}

type coverageSummary struct {
	coveredStatements int
	totalStatements   int
	packages          map[string]packageCoverage
}

func (s coverageSummary) percentage() float64 {
	if s.totalStatements == 0 {
		return 0
	}
	return 100 * float64(s.coveredStatements) / float64(s.totalStatements)
}

func main() {
	profilePath := flag.String("profile", "coverage.out", "path to a go coverprofile")
	minimum := flag.Float64("min", 85, "minimum total statement coverage percentage")
	exclude := flag.String(
		"exclude",
		"internal/domain/model,internal/bootstrap,cmd/api,cmd/scheduler,ent,docs,scripts,internal/infrastructure/mocks,internal/infrastructure/database/repository/mocks,internal/api/application/mocks,internal/messaging/events/mocks",
		"comma-separated package suffixes/prefixes to exclude",
	)
	flag.Parse()

	file, err := os.Open(*profilePath)
	if err != nil {
		fail("open coverage profile: %v", err)
	}
	defer func() { _ = file.Close() }()

	excluded := make([]string, 0)
	for _, value := range strings.Split(*exclude, ",") {
		value = strings.TrimSpace(value)
		if value != "" {
			excluded = append(excluded, value)
		}
	}

	summary, err := summarizeCoverage(file, excluded)
	if err != nil {
		fail("read coverage profile: %v", err)
	}

	packages := make([]string, 0, len(summary.packages))
	for packagePath := range summary.packages {
		packages = append(packages, packagePath)
	}
	sort.Strings(packages)

	for _, packagePath := range packages {
		stats := summary.packages[packagePath]
		percentage := 100 * float64(stats.coveredStatements) / float64(stats.totalStatements)
		fmt.Printf("%6.2f%% %s (%d/%d statements)\n", percentage, packagePath, stats.coveredStatements, stats.totalStatements)
	}

	percentage := summary.percentage()
	fmt.Printf("%6.2f%% total (%d/%d statements)\n", percentage, summary.coveredStatements, summary.totalStatements)
	if percentage < *minimum {
		fail("total coverage %.2f%% is below %.2f%%", percentage, *minimum)
	}
}

func summarizeCoverage(reader io.Reader, excluded []string) (coverageSummary, error) {
	summary := coverageSummary{packages: make(map[string]packageCoverage)}
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "mode: ") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) != 3 {
			return coverageSummary{}, fmt.Errorf("invalid coverage profile line: %q", line)
		}

		packagePath := filepath.ToSlash(fields[0])
		packagePath = packagePath[:strings.LastIndex(packagePath, "/")]
		if isExcluded(packagePath, excluded) {
			continue
		}

		statements, err := strconv.Atoi(fields[1])
		if err != nil {
			return coverageSummary{}, fmt.Errorf("parse statement count in %q: %w", line, err)
		}
		count, err := strconv.Atoi(fields[2])
		if err != nil {
			return coverageSummary{}, fmt.Errorf("parse execution count in %q: %w", line, err)
		}

		stats := summary.packages[packagePath]
		stats.totalStatements += statements
		summary.totalStatements += statements
		if count > 0 {
			stats.coveredStatements += statements
			summary.coveredStatements += statements
		}
		summary.packages[packagePath] = stats
	}
	if err := scanner.Err(); err != nil {
		return coverageSummary{}, err
	}
	return summary, nil
}

func isExcluded(packagePath string, excluded []string) bool {
	for _, suffix := range excluded {
		if packagePath == suffix ||
			strings.HasSuffix(packagePath, "/"+suffix) ||
			strings.HasPrefix(packagePath, suffix+"/") ||
			strings.Contains(packagePath, "/"+suffix+"/") {
			return true
		}
	}
	return false
}

func fail(format string, args ...interface{}) {
	// #nosec G705 -- This CLI writes diagnostics to stderr, never to a browser.
	fmt.Fprintln(os.Stderr, "coverage check failed:", fmt.Sprintf(format, args...))
	os.Exit(1)
}
