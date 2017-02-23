repostm is a low-performance software transactional memory (STM) implementation
in Go, made primarily as a learning experience and an exploration into modeling
an STM implementation after version control systems such as CVS, Subversion, or
Perforce.

[![Build Status](https://travis-ci.org/qpliu/repostm.svg?branch=master)](https://travis-ci.org/qpliu/repostm)

# Implementation

This STM implementation stores data as slices of bytes, which are copied out on
reads, and copied in on commits.  Hence, low-performance.

Data is encoded into byte slices and decoded from byte slices using
`encoding/gob`.

# Examples

```go
func Consume(repo *repostm.Repo, percentage, cap uint, totalHandle, availableHandle, allocationHandle repostm.Handle, wg *sync.WaitGroup) {
	defer wg.Done()
	total := totalHandle.Checkout()
	available := availableHandle.Checkout()
	allocation := allocationHandle.Checkout()
	for {
		version, err := repo.Revert(total, available, allocation)
		if err != nil {
			return
		}
		if allocation.Value.(uint) >= cap {
			return
		}
		currentCap = percentage*total.Value.(uint)/100
		if currentCap > cap {
			currentCap = cap
		}
		if allocation.Value.(uint) < currentCap {
			take := currentCap - allocation.Value.(uint)
			if take > available.Value.(uint) {
				take = available.Value.(uint)
			}
			if take > 0 {
				available.Value = available.Value.(uint) - take
				allocation.Value = allocation.Value.(uint) + take
			}
			version, err = repo.Commit(total, available, allocation)
			if err != nil && err != repostm.ErrStaleData {
				return
			}
		}
		if _, err := repo.WaitForCommit(version); err != nil {
			return
		}
	}
}

func AddTotal(repo *repostm.Repo, total, available *repostm.Memory, new uint) {
	for {
		if _, err := repo.Revert(total, available); err != nil {
			panic(err)
		}
		total.Value = total.Value.(uint) + new
		available.Value = available.Value.(uint) + new
		if _, err := repo.Commit(total, available); err == nil {
			return
		} else if err != repostm.ErrStaleData {
			panic(err)
		}
	}
}

func main() {
	wg := &sync.WaitGroup{}
	repo := repostm.New()
	totalHandle := repo.Add(0)
	availableHandle := repo.Add(0)

	allocations := []*Memory{}
	for i := 0; i < 5; i++ {
		allocation := repo.Add(0)
		allocations = append(allocations, allocation.Checkout())
		wg.Add(1)
		go Consume(repo, 25, 100, totalHandle, availableHandle, allcoation, wg)
	}

	total := totalHandle.Checkout()
	available := availableHandle.Checkout()
	for i := 0; i < 600; i++ {
		AddTotal(repo, total, available, 1)
		// check that the allocations and available add up to the total
		// check that each allocation is no more than 25% of the total
		// check that each allocation is no more than 100
	}

	repo.Close()
	wg.Wait()
}
```
