package repostm

import (
	"sync"
	"testing"
)

func equal(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func Test1(t *testing.T) {
	repo := New()
	h := repo.Add([]byte{})
	var wg sync.WaitGroup
	f := func(n byte, count int) {
		defer wg.Done()
		for i := 0; i < count; i++ {
			err := h.Atomically(func(bytes []byte) ([]byte, error) {
				return append(bytes, n), nil
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
	for _, b := range h.Checkout().Bytes {
		counts[b]++
	}
	if !equal([]byte{50, 50, 50}, counts) {
		t.Fail()
	}
}

func TestStaleCommit(t *testing.T) {
	//...
}

func TestLock(t *testing.T) {
	//...
}

func TestWaitForCommit(t *testing.T) {
	//...
}

func TestWaitForUnlock(t *testing.T) {
	//...
}
