package main

import (
	"encoding/csv"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
)

type bucket struct {
	upperBound float64
	count      float64
}

func parseArray(value string) ([]float64, error) {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "[")
	value = strings.TrimSuffix(value, "]")
	if value == "" {
		return nil, nil
	}

	parts := strings.Split(value, ",")
	result := make([]float64, 0, len(parts))
	for _, part := range parts {
		cleaned := strings.TrimSpace(part)
		cleaned = strings.Trim(cleaned, "'\"")
		lower := strings.ToLower(cleaned)
		var number float64
		switch lower {
		case "inf", "+inf", "infinity", "+infinity":
			number = math.Inf(1)
		case "-inf", "-infinity":
			number = math.Inf(-1)
		case "nan":
			number = math.NaN()
		default:
			var err error
			number, err = strconv.ParseFloat(cleaned, 64)
			if err != nil {
				return nil, err
			}
		}
		result = append(result, number)
	}
	return result, nil
}

func quantile(q float64, bounds, counts []float64) float64 {
	if math.IsNaN(q) || len(bounds) != len(counts) || len(bounds) < 2 {
		return math.NaN()
	}
	if q < 0 {
		return math.Inf(-1)
	}
	if q > 1 {
		return math.Inf(1)
	}

	buckets := make([]bucket, 0, len(bounds))
	for index := range bounds {
		buckets = append(buckets, bucket{upperBound: bounds[index], count: counts[index]})
	}
	sort.Slice(buckets, func(left, right int) bool {
		return buckets[left].upperBound < buckets[right].upperBound
	})
	if !math.IsInf(buckets[len(buckets)-1].upperBound, 1) {
		return math.NaN()
	}

	coalesced := make([]bucket, 0, len(buckets))
	for _, current := range buckets {
		if len(coalesced) > 0 && coalesced[len(coalesced)-1].upperBound == current.upperBound {
			coalesced[len(coalesced)-1].count += current.count
			continue
		}
		coalesced = append(coalesced, current)
	}
	maxCount := coalesced[0].count
	for index := 1; index < len(coalesced); index++ {
		if coalesced[index].count < maxCount {
			coalesced[index].count = maxCount
			continue
		}
		maxCount = coalesced[index].count
	}

	observations := coalesced[len(coalesced)-1].count
	if observations <= 0 {
		return math.NaN()
	}
	rank := q * observations
	index := sort.Search(len(coalesced)-1, func(candidate int) bool {
		return coalesced[candidate].count >= rank
	})
	if index == len(coalesced)-1 {
		return coalesced[len(coalesced)-2].upperBound
	}

	bucketStart := 0.0
	bucketEnd := coalesced[index].upperBound
	bucketCount := coalesced[index].count
	if index > 0 {
		bucketStart = coalesced[index-1].upperBound
		bucketCount -= coalesced[index-1].count
		rank -= coalesced[index-1].count
	}
	if bucketCount <= 0 {
		return bucketEnd
	}
	return bucketStart + (bucketEnd-bucketStart)*(rank/bucketCount)
}

func main() {
	reader := csv.NewReader(os.Stdin)
	reader.LazyQuotes = true
	reader.TrimLeadingSpace = true

	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil || len(record) < 3 {
			fmt.Println("nan")
			continue
		}

		bounds, boundsErr := parseArray(record[0])
		counts, countsErr := parseArray(record[1])
		q, quantileErr := strconv.ParseFloat(strings.TrimSpace(record[2]), 64)
		if boundsErr != nil || countsErr != nil || quantileErr != nil {
			fmt.Println("nan")
			continue
		}
		res := quantile(q, bounds, counts)
		if math.IsNaN(res) {
			fmt.Println("nan")
		} else {
			fmt.Printf("%.6f\n", res)
		}
	}
}
