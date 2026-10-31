package download

// plan cuts a file into the ranges its threads start on: one per minPart,
// at most threads, boundaries on align, the last range taking the rest.
func plan(size int64, threads int) []*part {
	if size <= 0 {
		return nil
	}
	n := min(max(size/minPart, 1), int64(threads))
	step := size / n
	step -= step % align
	parts := make([]*part, n)
	for i := range n {
		start, end := i*step, (i+1)*step
		if i == n-1 {
			end = size
		}
		parts[i] = &part{Start: start, End: end, Pos: start}
	}
	return parts
}

// next hands a thread its next range: a free one, else the back half of the
// busy range with the most left, so a thread that finishes early helps with
// the slowest instead of idling. nil when there is nothing worth taking.
func (a *attempt) next() *part {
	if a.single.Load() {
		return nil
	}
	a.j.pmu.Lock()
	defer a.j.pmu.Unlock()
	var big *part
	for _, p := range a.j.parts {
		if !p.busy && p.Pos < p.End {
			p.busy = true
			return p
		}
		if p.busy && (big == nil || p.End-p.Pos > big.End-big.Pos) {
			big = p
		}
	}
	if big == nil || big.End-big.Pos < 2*minSplit {
		return nil
	}
	cut := big.Pos + (big.End-big.Pos)/2
	cut -= cut % align
	if cut-big.Pos < minSplit || big.End-cut < minSplit {
		return nil
	}
	// The busy thread's buffer never reaches cut: it reads at most bufSize
	// past Pos, and cut is at least minSplit past it.
	p := &part{Start: cut, End: big.End, Pos: cut, busy: true}
	big.End = cut
	a.j.parts = append(a.j.parts, p)
	return p
}

// left is the bytes the ranges still have to fetch.
func left(parts []*part) int64 {
	var n int64
	for _, p := range parts {
		n += p.End - p.Pos
	}
	return n
}
