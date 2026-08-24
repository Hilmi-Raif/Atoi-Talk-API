package main

import (
	"flag"
	"fmt"
	"os"
	"sort"

	"golang.org/x/tools/cover"
)

type blockKey struct {
	file      string
	startLine int
	startCol  int
	endLine   int
	endCol    int
	numStmt   int
}

func main() {
	output := flag.String("output", "coverage-merged.out", "output coverprofile")
	flag.Parse()
	if flag.NArg() == 0 {
		fail("at least one input profile is required")
	}

	mode := ""
	blocks := make(map[blockKey]int)
	for _, path := range flag.Args() {
		profiles, err := cover.ParseProfiles(path)
		if err != nil {
			fail("parse %s: %v", path, err)
		}
		for _, profile := range profiles {
			if mode == "" {
				mode = profile.Mode
			} else if mode != profile.Mode {
				fail("coverage modes differ: %s and %s", mode, profile.Mode)
			}
			for _, block := range profile.Blocks {
				key := blockKey{profile.FileName, block.StartLine, block.StartCol, block.EndLine, block.EndCol, block.NumStmt}
				if mode == "set" {
					if block.Count > 0 {
						blocks[key] = 1
					}
					continue
				}
				blocks[key] += block.Count
			}
		}
	}

	keys := make([]blockKey, 0, len(blocks))
	for key := range blocks {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].file != keys[j].file {
			return keys[i].file < keys[j].file
		}
		if keys[i].startLine != keys[j].startLine {
			return keys[i].startLine < keys[j].startLine
		}
		return keys[i].startCol < keys[j].startCol
	})

	file, err := os.Create(*output)
	if err != nil {
		fail("create %s: %v", *output, err)
	}
	defer func() { _ = file.Close() }()
	if _, err := fmt.Fprintf(file, "mode: %s\n", mode); err != nil {
		fail("write %s: %v", *output, err)
	}
	for _, key := range keys {
		if _, err := fmt.Fprintf(file, "%s:%d.%d,%d.%d %d %d\n", key.file, key.startLine, key.startCol, key.endLine, key.endCol, key.numStmt, blocks[key]); err != nil {
			fail("write %s: %v", *output, err)
		}
	}
}

func fail(format string, args ...interface{}) {
	// #nosec G705 -- This CLI writes diagnostics to stderr, never to a browser.
	fmt.Fprintln(os.Stderr, "coverage merge failed:", fmt.Sprintf(format, args...))
	os.Exit(1)
}
