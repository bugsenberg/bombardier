package internal

import (
	"errors"
	"math"
	"sort"
)

// ReadonlyFloat64Histogram is a readonly histogram with float64 keys
type ReadonlyFloat64Histogram interface {
	Get(float64) uint64
	VisitAll(func(float64, uint64) bool)
	Count() uint64
}

// float64Aggregates holds aggregates calculated from float64 histograms
type float64Aggregates struct {
	Sum   float64
	Count uint64
	Max   float64
	Pairs []struct {
		k float64
		v uint64
	}
}

// NewFloat64HistogramAggregates calculates aggregates from a ReadonlyFloat64Histogram
func NewFloat64HistogramAggregates(histogram ReadonlyFloat64Histogram) (*float64Aggregates, error) {
	aggregates := new(float64Aggregates)
	aggregates.Pairs = make([]struct {
		k float64
		v uint64
	}, 0, histogram.Count())
	histogram.VisitAll(func(f float64, c uint64) bool {
		if math.IsInf(f, 0) || math.IsNaN(f) {
			return true
		}
		if f > aggregates.Max {
			aggregates.Max = f
		}
		aggregates.Sum += f * float64(c)
		aggregates.Count += c
		aggregates.Pairs = append(aggregates.Pairs, struct {
			k float64
			v uint64
		}{f, c})
		return true
	})
	if aggregates.Count < 1 {
		return nil, errors.New("Not enough values")
	}
	sort.Slice(aggregates.Pairs, func(i, j int) bool {
		return aggregates.Pairs[i].k < aggregates.Pairs[j].k
	})
	return aggregates, nil
}

// percentilesMap gives the values for a list of percentiles given as input
func (a *float64Aggregates) percentilesMap(percentiles []float64) map[float64]float64 {
	percentilesMap := map[float64]float64{}
	for _, pc := range percentiles {
		if _, calculated := percentilesMap[pc]; calculated {
			continue
		}
		if pc < 0 || pc > 1 {
			// Drop percentiles outside of [0, 1] range
			continue
		}
		rank := uint64(pc*float64(a.Count) + 0.5)
		total := uint64(0)
		for _, p := range a.Pairs {
			total += p.v
			if total >= rank {
				percentilesMap[pc] = p.k
				break
			}
		}
	}
	return percentilesMap
}
