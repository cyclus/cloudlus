package scen

import "sort"

type smoothFn func(x float64) float64

type sample struct {
	X float64
	Y float64
}

type sampleSet []sample

func (s sampleSet) Len() int           { return len(s) }
func (s sampleSet) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }
func (s sampleSet) Less(i, j int) bool { return s[i].X < s[j].X }

// interpolate generates a function that interpolates linearly between the X,Y
// points in samples.  It extrapolates linearly outside of the start and end
// bounds of the samples using the nearest (linear) slope.  The samples do not
// need to be in any particular order.  Multiple samples at the same X point
// are not allowed.
func interpolate(samples []sample) smoothFn {
	ss := make([]sample, len(samples))
	copy(ss, samples)
	sort.Sort(sampleSet(ss))
	return func(x float64) (y float64) {
		for i := range ss[:len(ss)-1] {
			left := ss[i].X
			right := ss[i+1].X
			if x <= right {
				lefty := ss[i].Y
				righty := ss[i+1].Y
				return lefty + (x-left)/(right-left)*(righty-lefty)
			}
		}

		// if x is beyond last sample x-val, just extrapolate slope between
		// last two samples' x-vals.
		end := len(ss) - 1
		left := ss[end-1].X
		right := ss[end].X
		lefty := ss[end-1].Y
		righty := ss[end].Y
		return lefty + (x-left)/(right-left)*(righty-lefty)
	}
}

func productOf(fn1, fn2 smoothFn) smoothFn {
	return func(x float64) (y float64) {
		return fn1(x) * fn2(x)
	}
}

func integrateMid(fn smoothFn, x1, x2 float64, ninterval int) float64 {
	dx := (x2 - x1) / float64(ninterval)
	tot := 0.0
	for i := 0; i < ninterval; i++ {
		x := x1 + (float64(i)+0.5)*dx
		dA := fn(x) * dx
		tot += dA
	}
	return tot
}

func sampleUniformProb(fn smoothFn, x1, x2 float64, nsample, ninterval int) (xs []float64) {
	totA := integrateMid(fn, x1, x2, ninterval*nsample)
	sampleA := totA / float64(nsample)

	dx := (x2 - x1) / float64(ninterval*nsample)
	tot := 0.0
	for i := 0; i < ninterval*nsample; i++ {
		x := x1 + (float64(i)+0.5)*dx
		dA := fn(x) * dx
		tot += dA
		if tot >= float64(len(xs)+1)*sampleA {
			xs = append(xs, x)
		}
	}
	return xs
}

func zip(disrups []Disruption, objs []float64) []sample {
	if len(disrups) != len(objs) {
		panic("cannot zip slices of unequal length")
	}

	samples := make([]sample, len(disrups))
	for i := range disrups {
		samples = append(samples, sample{float64(disrups[i].Time), objs[i]})
	}
	return samples
}

func extractProbs(disrups []Disruption) []sample {
	samples := []sample{}
	for _, d := range disrups {
		samples = append(samples, sample{float64(d.Time), d.Prob})
	}
	return samples
}
