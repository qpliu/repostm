package repostm

import (
	"bytes"
	"sync"
	"testing"
	"time"
)

func Test1(t *testing.T) {
	repo := New()
	h := repo.Add([]byte{})
	var wg sync.WaitGroup
	f := func(n byte, count int) {
		defer wg.Done()
		for i := 0; i < count; i++ {
			_, err := h.Atomically(func(value interface{}) (interface{}, error) {
				return append(value.([]byte), n), nil
			})
			if err != nil {
				t.Errorf("Atomically: %s", err)
			}
		}
	}
	wg.Add(3)
	go f(0, 50)
	go f(1, 50)
	go f(2, 50)
	wg.Wait()
	counts := []byte{0, 0, 0}
	for _, b := range h.Checkout().Value.([]byte) {
		counts[b]++
	}
	if bytes.Compare([]byte{50, 50, 50}, counts) != 0 {
		t.Fail()
	}
}

func TestStaleCommit(t *testing.T) {
	repo := New()
	h := repo.Add(1)
	m1 := h.Checkout()
	m2 := h.Checkout()
	if m1.Version() != m2.Version() {
		t.Error("Version should be equal")
	}
	if m1.RepoVersion() != m2.RepoVersion() {
		t.Error("RepoVersion should be equal")
	}
	if m1.Value != 1 {
		t.Errorf("Value should be 1: %v", m1.Value)
	}
	m1.Value = 2
	if _, err := m1.Commit(); err != nil {
		t.Errorf("Commit: %s", err)
	}
	if m2.Value != 1 {
		t.Errorf("Value should be 1: %v", m2.Value)
	}
	if m1.Version() == m2.Version() {
		t.Error("Version should not be equal")
	}
	if m1.RepoVersion() == m2.RepoVersion() {
		t.Error("RepoVersion should not be equal")
	}
	if _, err := m2.Commit(); err != ErrStaleData {
		t.Errorf("Should be ErrStaleData: %v", err)
	}
	if m2.Value != 2 {
		t.Errorf("Value should be 2: %v", m2.Value)
	}
	if m1.Version() != m2.Version() {
		t.Error("Version should be equal")
	}
	if m1.RepoVersion() != m2.RepoVersion() {
		t.Error("RepoVersion should be equal")
	}
	if _, err := m2.Update(); err != nil {
		t.Errorf("Update: %s", err)
	}
	if m2.Value != 2 {
		t.Errorf("Value should be 2: %v", m2.Value)
	}
	m2.Value = 3
	if _, err := m2.Commit(); err != nil {
		t.Errorf("Commit: %s", err)
	}

}

func TestTypeChanged(t *testing.T) {
	repo := New()
	m := repo.Add("test").Checkout()
	if m.Value != "test" {
		t.Errorf("Value should be test: %v", m.Value)
	}
	m.Value = 1
	if _, err := m.Commit(); err != ErrTypeChanged {
		t.Errorf("Should be ErrTypeChanged: %v", err)
	}
}

func TestWrongRepo(t *testing.T) {
	r1 := New()
	r2 := New()
	m1 := r1.Add([]byte{}).Checkout()
	m2 := r2.Add([]byte{}).Checkout()
	if _, err := r1.Update(m1); err != nil {
		t.Errorf("Update: %s", err)
	}
	if _, err := r1.Update(m1, m2); err != ErrWrongRepo {
		t.Fail()
	}
	if _, err := r1.Update(m2); err != ErrWrongRepo {
		t.Fail()
	}
	if _, err := r1.Commit(m1); err != nil {
		t.Errorf("Commit: %s", err)
	}
	if _, err := r1.Commit(m1, m2); err != ErrWrongRepo {
		t.Fail()
	}
	if _, err := r1.Commit(m2); err != ErrWrongRepo {
		t.Fail()
	}
}

func TestLock(t *testing.T) {
	repo := New()
	h := repo.Add(1)
	l, err := repo.Lock()
	if err != nil {
		t.Errorf("Lock: %s", err)
	}
	if l2, err := repo.Lock(); err != ErrLocked {
		t.Errorf("Lock: %s", err)
	} else {
		if err := l2.Release(); err != ErrInvalidLock {
			t.Errorf("Release: %s", err)
		}
	}
	m := h.Checkout()
	if _, err := m.Commit(); err != ErrLocked {
		t.Errorf("Commit: %s", err)
	}
	m2 := h.Checkout()
	if m2.Value != 1 {
		t.Errorf("Value should be 1: %v", m2.Value)
	}
	m.Value = 2
	if _, err := m.CommitWithLock(l); err != nil {
		t.Errorf("CommitWithLock: %s", err)
	}
	if _, err := m2.Update(); err != nil {
		t.Errorf("Update: %s", err)
	}
	if m2.Value != 2 {
		t.Errorf("Value should be 2: %v", m2.Value)
	}
	if err := l.Release(); err != nil {
		t.Errorf("Release: %s", err)
	}
	if err := l.Release(); err != ErrInvalidLock {
		t.Fail()
	}
	m.Value = 3
	if _, err := m.Commit(); err != nil {
		t.Errorf("Commit: %s", err)
	}
}

func TestWaitForCommit(t *testing.T) {
	startTime := time.Now()
	repo := New()
	h := repo.Add([]byte{})
	version, err := h.Checkout().Update()
	if err != nil {
		t.Errorf("WaitForCommit: %s", err)
	}
	sleepTime := 100 * time.Millisecond
	go func() {
		time.Sleep(sleepTime)
		if _, err := h.Checkout().Commit(); err != nil {
			t.Errorf("WaitForCommit: %s", err)
		}
	}()
	newVersion := repo.WaitForCommit(version)
	if newVersion == version {
		t.Fail()
	}
	if startTime.Add(sleepTime).After(time.Now()) {
		t.Fail()
	}
}

func TestWaitForUnlock(t *testing.T) {
	startTime := time.Now()
	repo := New()
	l, err := repo.Lock()
	if err != nil {
		t.Errorf("Lock: %s", err)
	}
	sleepTime := 100 * time.Millisecond
	go func() {
		time.Sleep(sleepTime)
		l.Release()
	}()
	repo.WaitForUnlock()
	if startTime.Add(sleepTime).After(time.Now()) {
		t.Fail()
	}
}

func TestExample(t *testing.T) {
	repo := New()
	var wg sync.WaitGroup

	allocationPercentage := 25
	allocationCap := 100
	consumerCount := 5
	productionCap := 600

	totalHandle := repo.Add(0)
	availableHandle := repo.Add(0)
	allocationHandles := make([]Handle, consumerCount)
	for i := range allocationHandles {
		allocationHandles[i] = repo.Add(0)
	}

	consumer := func(allocationHandle Handle) {
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

	check := func(total, available *Memory, allocations []*Memory) {
		repo.Update(append([]*Memory{total, available}, allocations...)...)
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
		allocations := make([]*Memory, len(allocationHandles))
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
	allocations := make([]*Memory, len(allocationHandles))
	for i, allocationHandle := range allocationHandles {
		allocations[i] = allocationHandle.Checkout()
	}
	check(total, available, allocations)
}
