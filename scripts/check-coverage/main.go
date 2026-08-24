package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type packageCoverage struct {
	covered    int
	statements int
}

func main() {
	profilePath := flag.String("profile", "coverage.out", "path to a go coverprofile")
	minimum := flag.Float64("min", 85, "minimum percentage required for every package")
	exclude := flag.String(
		"exclude",
		"internal/model,internal/bootstrap,cmd/api,cmd/scheduler,ent,docs,scripts,internal/adapter/mocks,internal/repository/mocks,internal/service/mocks,internal/websocket/mocks",
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

	coverage := make(map[string]packageCoverage)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "mode: ") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) != 3 {
			fail("invalid coverage profile line: %q", line)
		}

		packagePath := filepath.ToSlash(fields[0])
		packagePath = packagePath[:strings.LastIndex(packagePath, "/")]
		covered, err := strconv.Atoi(fields[2])
		if err != nil {
			fail("parse execution count in %q: %v", line, err)
		}

		stats := coverage[packagePath]
		stats.statements++
		if covered > 0 {
			stats.covered++
		}
		coverage[packagePath] = stats
	}
	if err := scanner.Err(); err != nil {
		fail("read coverage profile: %v", err)
	}

	packages := make([]string, 0, len(coverage))
	for packagePath := range coverage {
		if !isExcluded(packagePath, excluded) {
			packages = append(packages, packagePath)
		}
	}
	sort.Strings(packages)

	failed := false
	for _, packagePath := range packages {
		stats := coverage[packagePath]
		percentage := 100 * float64(stats.covered) / float64(stats.statements)
		fmt.Printf("%6.2f%% %s (%d/%d statements)\n", percentage, packagePath, stats.covered, stats.statements)
		if percentage < *minimum {
			failed = true
		}
	}

	if failed {
		fail("one or more packages are below %.2f%% coverage", *minimum)
	}
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
