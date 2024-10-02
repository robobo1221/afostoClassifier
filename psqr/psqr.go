package psqr

import "sync"

// Psqr collects observations and returns an estimate of requested p-quantile, as described in the P-Square algorithm
type Psqr struct {
	sync.Mutex

	Perc  float64
	Count int
	Q     [5]float64
	N     [5]int
	Np    [5]float64
	Dn    [5]float64
}

// NewPsqr returns a new instance of Psqr
func NewPsqr(q float64) *Psqr {
	p := &Psqr{}
	p.Perc = q
	p.Reset()
	return p
}

// Add collects a new observation, updates marker positions and the current estimate
func (p *Psqr) Add(v float64) float64 {
	sign := func(f float64) int {
		if f < 0.0 {
			return -1
		}
		return 1
	}

	parabolic := func(i, d int) float64 {
		qi, qip1, qim1 := p.Q[i], p.Q[i+1], p.Q[i-1]
		ni, nip1, nim1 := float64(p.N[i]), float64(p.N[i+1]), float64(p.N[i-1])
		df := float64(d)
		return qi + df/(nip1-nim1)*((ni-nim1+df)*(qip1-qi)/(nip1-ni)+(nip1-ni-df)*(qi-qim1)/(ni-nim1))
	}

	linear := func(i, d int) float64 {
		df := float64(d)
		return p.Q[i] + df*(p.Q[i+d]-p.Q[i])/float64(p.N[i+d]-p.N[i])
	}

	if p.Count < 5 {
		// store the first observations
		p.Q[p.Count], p.Count = v, p.Count+1

		if p.Count == 5 {
			// sort the first observations
			for i := 1; i < p.Count; i++ {
				for j := i; j > 0 && p.Q[j-1] > p.Q[j]; j-- {
					p.Q[j], p.Q[j-1] = p.Q[j-1], p.Q[j]
				}
			}
		}

		// note that p.Q[2] is meaningless at this point
		return p.Q[2]
	}

	p.Count = p.Count + 1

	// find cell k such that [qk < xj < qk+1] and adjust extreme values if necessary
	var k int
	for k = 0; k < 5; k++ {
		if v < p.Q[k] {
			break
		}
	}

	if k == 0 {
		k = 1
		p.Q[0] = v
	} else if k == 5 {
		k = 4
		p.Q[4] = v
	}

	// increment positions of markers k+1 through 5
	for i := k; i < 5; i++ {
		p.N[i]++
	}

	// update desired positions for all markers
	for i := 0; i < 5; i++ {
		p.Np[i] = p.Np[i] + p.Dn[i]
	}

	// adjust heights of markers 2-4 if necessary
	for i := 1; i < 4; i++ {
		d := p.Np[i] - float64(p.N[i])
		if (d >= 1.0 && p.N[i+1]-p.N[i] > 1) || (d <= -1.0 && p.N[i-1]-p.N[i] < -1) {
			ds := sign(d)
			qp := parabolic(i, ds)

			if p.Q[i-1] < qp && qp < p.Q[i+1] {
				p.Q[i] = qp
			} else {
				p.Q[i] = linear(i, ds)
			}
			p.N[i] = p.N[i] + ds
		}
	}

	// return the current estimate of p-quantile
	return p.Q[2]
}

// Get returns the current estimate of p-quantile
func (p *Psqr) Get() float64 {
	return p.Q[2]
}

func (p *Psqr) Reset() {
	q := p.Perc

	p.Count = 0

	// calculate and store the increment in desired marker positions
	p.Dn[0], p.Dn[1], p.Dn[2], p.Dn[3], p.Dn[4] = 0.0, q*0.5, q, (1+q)*0.5, 1.0

	// set initial marker positions
	for i := 0; i < 5; i++ {
		p.N[i] = i + 1
		p.Np[i] = p.Dn[i]*4 + 1
	}
}
