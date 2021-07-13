package internal

import (
	"errors"
	"sort"
)

// ReadonlyUint64Histogram is a readonly histogram with uint64 keys
type ReadonlyUint64Histogram interface {
	Get(uint64) uint64
	VisitAll(func(uint64, uint64) bool)
	Count() uint64
}

// uint64Aggregates holds aggregates calculated from uint64 histograms
type uint64Aggregates struct {
	Sum   uint64
	Count uint64
	Max   uint64
	Pairs []struct {
		k uint64
		v uint64
	}
}

// NewUint64HistogramAggregates calculates aggregates from a ReadonlyUint64Histogram
func NewUint64HistogramAggregates(histogram ReadonlyUint64Histogram) (*uint64Aggregates, error) {
	aggregates := new(uint64Aggregates)
	aggregates.Pairs = make([]struct {
		k uint64
		v uint64
	}, 0, histogram.Count())
	histogram.VisitAll(func(f uint64, c uint64) bool {
		if f > aggregates.Max {
			aggregates.Max = f
		}
		aggregates.Sum += f * c
		aggregates.Count += c
		aggregates.Pairs = append(aggregates.Pairs, struct{ k, v uint64 }{f, c})
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
func (a *uint64Aggregates) percentilesMap(percentiles []float64) map[float64]uint64 {
	percentilesMap := map[float64]uint64{}
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
