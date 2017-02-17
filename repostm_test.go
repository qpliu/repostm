package repostm

import (
	"bytes"
	"sync"
	"testing"
	"time"
)

func Test1(t *testing.T) {
	repo := New()
	defer repo.Close()
	h := repo.Add([]byte{})
	var wg sync.WaitGroup
	f := func(n byte, count int) {
		defer wg.Done()
		for i := 0; i < count; i++ {
			err := h.Atomically(func(value interface{}) (interface{}, error) {
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
	defer repo.Close()
	h := repo.Add(1)
	m1 := h.Checkout()
	m2 := h.Checkout()
	if m1.Value != 1 {
		t.Errorf("Value should be 1: %s", m1.Value)
	}
	m1.Value = 2
	if _, err := m1.Commit(); err != nil {
		t.Errorf("Commit: %s", err)
	}
	if m2.Value != 1 {
		t.Errorf("Value should be 1: %s", m2.Value)
	}
	if _, err := m2.Commit(); err != ErrStaleData {
		t.Fail()
	}
	if _, err := m2.Update(); err != nil {
		t.Errorf("Update: %s", err)
	}
	if m2.Value != 2 {
		t.Errorf("Value should be 2: %s", m2.Value)
	}
	m2.Value = 3
	if _, err := m2.Commit(); err != nil {
		t.Errorf("Commit: %s", err)
	}

}

func TestWrongRepo(t *testing.T) {
	r1 := New()
	defer r1.Close()
	r2 := New()
	defer r2.Close()
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
	defer repo.Close()
	h := repo.Add(1)
	l, err := repo.Lock()
	if err != nil {
		t.Errorf("Lock: %s", err)
	}
	m := h.Checkout()
	if _, err := m.Commit(); err != ErrLocked {
		t.Errorf("Commit: %s", err)
	}
	m2 := h.Checkout()
	if m2.Value != 1 {
		t.Errorf("Value should be 1: %s", m2.Value)
	}
	m.Value = 2
	if _, err := m.CommitWithLock(l); err != nil {
		t.Errorf("CommitWithLock: %s", err)
	}
	if _, err := m2.Update(); err != nil {
		t.Errorf("Update: %s", err)
	}
	if m2.Value != 2 {
		t.Errorf("Value should be 2: %s", m2.Value)
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
	defer repo.Close()
	h := repo.Add([]byte{})
	version, err := h.Checkout().Update()
	sleepTime := 100 * time.Millisecond
	go func() {
		time.Sleep(sleepTime)
		if _, err := h.Checkout().Commit(); err != nil {
			t.Errorf("WaitForUnlock: %s", err)
		}
	}()
	newVersion, err := repo.WaitForCommit(version)
	if err != nil {
		t.Errorf("WaitForUnlock: %s", err)
	}
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
	defer repo.Close()
	l, err := repo.Lock()
	if err != nil {
		t.Errorf("Lock: %s", err)
	}
	sleepTime := 100 * time.Millisecond
	go func() {
		time.Sleep(sleepTime)
		l.Release()
	}()
	if err := repo.WaitForUnlock(); err != nil {
		t.Errorf("WaitForUnlock: %s", err)
	}
	if startTime.Add(sleepTime).After(time.Now()) {
		t.Fail()
	}
}
