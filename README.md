repostm is a low-performance software transactional memory (STM) implementation
in Go, made primarily as a learning experience and an exploration into modeling
an STM implementation after version control systems such as CVS, Subversion, or
Perforce.

[![GoDoc](https://godoc.org/github.com/qpliu/repostm?status.svg)](https://godoc.org/github.com/qpliu/repostm)
[![Build Status](https://travis-ci.org/qpliu/repostm.svg?branch=master)](https://travis-ci.org/qpliu/repostm)

# Implementation

This STM implementation stores data as slices of bytes, which are copied out on
reads, and copied in on commits.  Hence, low-performance.

Data is encoded into byte slices and decoded from byte slices using
`encoding/gob`.

# Example

Example of a concurrent producer and consumers, where each consumer will take
no more than 25% of the current total produced.

```go
func TestExample(t *testing.T) {
	repo := repostm.New()
	var wg sync.WaitGroup

	allocationPercentage := 25
	allocationCap := 100
	consumerCount := 5
	productionCap := 600

	totalHandle := repo.Add(0)
	availableHandle := repo.Add(0)
	allocationHandles := make([]repostm.Handle, consumerCount)
	for i := range allocationHandles {
		allocationHandles[i] = repo.Add(0)
	}

	consumer := func(allocationHandle repostm.Handle) {
		defer wg.Done()
		total := totalHandle.Checkout()
		available := availableHandle.Checkout()
		allocation := allocationHandle.Checkout()
		for allocation.Value.(int) < allocationCap {
			repo.Atomically(func() error {
				currentCap := allocationPercentage * total.Value.(int) / 100
				if currentCap > allocationCap {
					currentCap = allocationCap
				}
				if allocation.Value.(int) >= currentCap {
					return ErrRetryAfterCommit
				}
				take := currentCap - allocation.Value.(int)
				if take > available.Value.(int) {
					take = available.Value.(int)
				}
				if take > 0 {
					available.Value = available.Value.(int) - take
					allocation.Value = allocation.Value.(int) + take
					return nil
				} else if currentCap < allocationCap {
					return ErrRetryAfterCommit
				} else {
					return nil
				}
			}, total, available, allocation)
		}
	}

	for _, allocationHandle := range allocationHandles {
		wg.Add(1)
		go consumer(allocationHandle)
	}

	check := func(total, available *repostm.Memory, allocations []*repostm.Memory) {
		repo.Update(append([]*repostm.Memory{total, available}, allocations...)...)
		if total.Value.(int) < available.Value.(int) {
			t.Errorf("total %d < available %d", total.Value, available.Value)
		}
		allocationTotal := 0
		for i, allocation := range allocations {
			allocationTotal += allocation.Value.(int)
			if total.Value.(int)*allocationPercentage/100 < allocation.Value.(int) {
				t.Errorf("%d%% of total %d < allocation[%d] %d", allocationPercentage, total.Value, i, allocation.Value)
			}
		}
		if total.Value.(int) != available.Value.(int)+allocationTotal {
			t.Errorf("total %d != available %d + allocations %d", total.Value, available.Value, allocationTotal)
		}
	}

	producer := func() {
		defer wg.Done()
		total := totalHandle.Checkout()
		available := availableHandle.Checkout()
		allocations := make([]*repostm.Memory, len(allocationHandles))
		for i, allocationHandle := range allocationHandles {
			allocations[i] = allocationHandle.Checkout()
		}
		for total.Value.(int) < productionCap {
			repo.Atomically(func() error {
				total.Value = total.Value.(int) + 1
				available.Value = available.Value.(int) + 1
				return nil
			}, total, available)
			check(total, available, allocations)
		}
	}

	wg.Add(1)
	go producer()
	wg.Wait()

	total := totalHandle.Checkout()
	available := availableHandle.Checkout()
	allocations := make([]*repostm.Memory, len(allocationHandles))
	for i, allocationHandle := range allocationHandles {
		allocations[i] = allocationHandle.Checkout()
	}
	check(total, available, allocations)
}
```
