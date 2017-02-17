// Package repostm is a low-performance software transactional memory (STM)
// implementation, made primarily as a learning experience and an exploration
// into modeling an STM implementation after version control systems such as
// CVS, Subversion, or Perforce.
package repostm

import (
	"bytes"
	"encoding/gob"
	"errors"
	"reflect"
)

// Handle identifies particular piece of memory and may be shared.
type Handle struct {
	canonical *canonical
	typ       reflect.Type
}

// Memory is a local copy of a particular piece of memory and must not be
// shared.
type Memory struct {
	canonical   *canonical
	version     MemoryVersion
	repoVersion RepoVersion
	bytes       []byte
	Value       interface{}
}

// Repo is a repository of shared memory.
type Repo struct {
	version          RepoVersion
	currentLock      *Lock
	close            chan bool
	update           chan batch
	commit           chan batch
	lock             chan batch
	unlock           chan batch
	waitForCommit    chan batch
	waitersForCommit []batch
	waitForUnlock    chan batch
	waitersForUnlock []batch
}

// RepoVersion is the version of a Repo and is updated each time anything is
// committed in the Repo.
type RepoVersion struct {
	version uint64
}

// MemoryVersion is the version of a particular piece of memory and is updated
// in the Repo each time that piece of memory is committed and is updated in
// the local Memory when that local copy is updated.
type MemoryVersion struct {
	version uint64
}

type canonical struct {
	repo    *Repo
	version MemoryVersion
	bytes   []byte
}

type batch struct {
	memory    []*Memory
	checkOnly []*Memory
	revert    bool
	lock      *Lock
	version   RepoVersion
	result    chan result
}

type result struct {
	lock    *Lock
	version RepoVersion
	err     error
}

// Lock is returned when locking a Repo. Commits without the Lock will fail
// until the Lock is released.
type Lock struct {
	repo *Repo
}

var (
	// ErrLocked is returned when committing to a locked Repo.
	ErrLocked = errors.New("repostm: Repo locked")
	// ErrWrongRepo is returned when updating from or committing to the
	// wrong Repo.
	ErrWrongRepo = errors.New("repostm: Wrong Repo")
	// ErrInvalidLock is returned when unlocking a Repo that is not locked
	// with the given Lock.
	ErrInvalidLock = errors.New("repostm: Invalid lock")
	// ErrStaleData is returned when committing memory that is not up to
	// date.
	ErrStaleData = errors.New("repostm: Stale data")
	// ErrRepoClosed is returned by WaitForUnlock and WaitForCommit if the
	// Repo is closed.
	ErrRepoClosed = errors.New("repostm: Repo closed")
)

// New creates a new Repo.
func New() *Repo {
	repo := &Repo{
		version:       RepoVersion{1},
		currentLock:   nil,
		close:         make(chan bool),
		update:        make(chan batch),
		commit:        make(chan batch),
		lock:          make(chan batch),
		unlock:        make(chan batch),
		waitForCommit: make(chan batch),
		waitForUnlock: make(chan batch),
	}
	go func() {
		defer func() {
			close(repo.close)
			close(repo.update)
			close(repo.commit)
			close(repo.lock)
			close(repo.unlock)
			close(repo.waitForCommit)
			close(repo.waitForUnlock)
			for _, w := range repo.waitersForCommit {
				w.result <- result{err: ErrRepoClosed}
			}
			for _, w := range repo.waitersForUnlock {
				w.result <- result{err: ErrRepoClosed}
			}
		}()
	selectLoop:
		for {
			select {
			case <-repo.close:
				return
			case b := <-repo.update:
				for _, m := range b.memory {
					if m.canonical.repo != repo {
						b.result <- result{err: ErrWrongRepo}
						continue selectLoop
					}
				}
				for _, m := range b.memory {
					m.repoVersion = repo.version
					if b.revert || m.version != m.canonical.version {
						m.version = m.canonical.version
						m.bytes = m.bytes[0:0]
						m.bytes = append(m.bytes, m.canonical.bytes...)
					}
				}
				b.result <- result{version: repo.version}
			case b := <-repo.commit:
				if repo.currentLock != nil && repo.currentLock != b.lock {
					b.result <- result{err: ErrLocked}
					continue selectLoop
				}
				for _, m := range b.memory {
					if m.canonical.repo != repo {
						b.result <- result{err: ErrWrongRepo}
						continue selectLoop
					}
					if m.version != m.canonical.version {
						b.result <- result{err: ErrStaleData}
						continue selectLoop
					}
				}
				for _, m := range b.checkOnly {
					if m.canonical.repo != repo {
						b.result <- result{err: ErrWrongRepo}
						continue selectLoop
					}
					if m.version != m.canonical.version {
						b.result <- result{err: ErrStaleData}
						continue selectLoop
					}
				}
				repo.version.version++
				for _, m := range b.memory {
					m.canonical.version.version++
					m.canonical.bytes = m.canonical.bytes[0:0]
					m.canonical.bytes = append(m.canonical.bytes, m.bytes...)
					m.version = m.canonical.version
					m.repoVersion = repo.version
				}
				for _, m := range b.checkOnly {
					m.repoVersion = repo.version
				}
				b.result <- result{version: repo.version}
				for _, w := range repo.waitersForCommit {
					w.result <- result{version: repo.version}
				}
				repo.waitersForCommit = repo.waitersForCommit[0:0]
			case b := <-repo.lock:
				if repo.currentLock == nil {
					repo.currentLock = &Lock{repo}
					b.result <- result{lock: repo.currentLock}
				} else {
					b.result <- result{err: ErrLocked}
				}
			case b := <-repo.unlock:
				if b.lock == repo.currentLock {
					repo.currentLock = nil
					b.result <- result{}
					for _, w := range repo.waitersForUnlock {
						w.result <- result{}
					}
					repo.waitersForUnlock = repo.waitersForUnlock[0:0]
				} else {
					b.result <- result{err: ErrInvalidLock}
				}
			case b := <-repo.waitForCommit:
				if repo.version == b.version {
					repo.waitersForCommit = append(repo.waitersForCommit, b)
				} else {
					b.result <- result{version: repo.version}
				}
			case b := <-repo.waitForUnlock:
				if repo.currentLock != nil {
					repo.waitersForUnlock = append(repo.waitersForUnlock, b)
				} else {
					b.result <- result{}
				}
			}
		}
	}()
	return repo
}

// Close closes the Repo, closing its channels and ending its goroutine.
func (repo *Repo) Close() {
	repo.close <- true
}

// Update overwrites the local copies of memory with out of date data with
// the latest data in the Repo.  Up to date copies are not modified.
func (repo *Repo) Update(memory ...*Memory) (RepoVersion, error) {
	result := make(chan result)
	versions := make([]MemoryVersion, len(memory))
	for i, mem := range memory {
		versions[i] = mem.version
	}
	repo.update <- batch{revert: false, memory: memory, result: result}
	res := <-result
	if res.err == nil {
		for i, mem := range memory {
			if versions[i] != mem.version {
				mem.Reset()
			}
		}
	}
	return res.version, res.err
}

// Revert overwrites the local copies of memory with the latest data in the
// Repo, including up to date copies.
func (repo *Repo) Revert(memory ...*Memory) (RepoVersion, error) {
	result := make(chan result)
	repo.update <- batch{revert: true, memory: memory, result: result}
	res := <-result
	if res.err == nil {
		for _, mem := range memory {
			mem.Reset()
		}
	}
	return res.version, res.err
}

// Commit commits the local copies of memory to the Repo.
func (repo *Repo) Commit(memory ...*Memory) (RepoVersion, error) {
	return repo.CommitWithLock(nil, memory...)
}

// CommitWithLock commits the local copies of memory to the Repo, which is
// locked with the Lock.
func (repo *Repo) CommitWithLock(lock *Lock, memory ...*Memory) (RepoVersion, error) {
	var changed []*Memory
	var checkOnly []*Memory
	for _, mem := range memory {
		if mem.precommit() {
			changed = append(changed, mem)
		} else {
			checkOnly = append(checkOnly, mem)
		}
	}
	result := make(chan result)
	repo.commit <- batch{lock: lock, memory: changed, checkOnly: checkOnly, result: result}
	res := <-result
	if res.err != nil {
		repo.Revert(changed...)
	}
	return res.version, res.err
}

// Lock locks the Repo.
func (repo *Repo) Lock() (*Lock, error) {
	result := make(chan result)
	repo.lock <- batch{result: result}
	res := <-result
	return res.lock, res.err
}

// Unlock unlocks the Repo.
func (repo *Repo) Unlock(lock *Lock) error {
	result := make(chan result)
	repo.unlock <- batch{lock: lock, result: result}
	return (<-result).err
}

// WaitForUnlock waits until the Repo is unlocked.
func (repo *Repo) WaitForUnlock() error {
	result := make(chan result)
	repo.waitForUnlock <- batch{result: result}
	res := <-result
	return res.err
}

// WaitForCommit waits until the Repo has seen a commit since the given
// RepoVersion.
func (repo *Repo) WaitForCommit(version RepoVersion) (RepoVersion, error) {
	result := make(chan result)
	repo.waitForCommit <- batch{version: version, result: result}
	res := <-result
	return res.version, res.err
}

// Add commits a new piece of memory containing the given value to the Repo.
// Does not check for locks and does not update the Repo version, and thus
// does not wake those waiting in WaitForCommit.
func (repo *Repo) Add(value interface{}) Handle {
	gob.Register(value)
	var b bytes.Buffer
	if err := gob.NewEncoder(&b).Encode(value); err != nil {
		panic(err)
	}
	return Handle{
		canonical: &canonical{
			repo:    repo,
			version: MemoryVersion{1},
			bytes:   b.Bytes(),
		},
		typ: reflect.TypeOf(value),
	}
}

// Checkout creates a new local copy of the piece of memory associated with
// the Handle.
func (repo *Repo) Checkout(handle Handle) *Memory {
	memory := &Memory{
		canonical: handle.canonical,
		version:   MemoryVersion{0},
		bytes:     nil,
		Value:     reflect.Zero(handle.typ).Interface(),
	}
	if _, err := memory.Revert(); err != nil {
		panic(err)
	}
	return memory
}

// Handle returns the Handle referring to the piece of memory.
func (memory *Memory) Handle() Handle {
	return Handle{canonical: memory.canonical, typ: reflect.TypeOf(memory.Value)}
}

// Update overwrites the local copy of memory with out of date data with
// the latest data in the Repo.  An up to date copy is not modified.
func (memory *Memory) Update() (RepoVersion, error) {
	return memory.canonical.repo.Update(memory)
}

// Revert overwrites the local copy of memory with the latest data in the
// Repo, even when it is up to date.
func (memory *Memory) Revert() (RepoVersion, error) {
	return memory.canonical.repo.Revert(memory)
}

// Commit commits the local copy of memory to the Repo.
func (memory *Memory) Commit() (RepoVersion, error) {
	return memory.canonical.repo.Commit(memory)
}

// CommitWithLock commits the local copy of memory to the Repo, which is
// locked with the Lock.
func (memory *Memory) CommitWithLock(lock *Lock) (RepoVersion, error) {
	return memory.canonical.repo.CommitWithLock(lock, memory)
}

// Reset resets local copy of the Value to its value at the last Update
// or Commit.
func (memory *Memory) Reset() {
	value := reflect.New(reflect.TypeOf(memory.Value))
	if err := gob.NewDecoder(bytes.NewBuffer(memory.bytes)).DecodeValue(value); err != nil {
		panic(err)
	}
	memory.Value = reflect.Indirect(value).Interface()
}

func (memory *Memory) precommit() bool {
	var b bytes.Buffer
	if err := gob.NewEncoder(&b).Encode(memory.Value); err != nil {
		panic(err)
	}
	newBytes := b.Bytes()
	if bytes.Compare(newBytes, memory.bytes) == 0 {
		return false
	}
	memory.bytes = newBytes
	return true
}

// Version returns the version of the local copy of memory.
func (memory *Memory) Version() MemoryVersion {
	return memory.version
}

// RepoVersion returns the version of the Repo this local copy of memory
// was last updated with.
func (memory *Memory) RepoVersion() RepoVersion {
	return memory.repoVersion
}

// Atomically modify and commit this piece of memory, retrying until it
// succeeds.
func (memory *Memory) Atomically(f func(interface{}) (interface{}, error)) error {
	for {
		value, err := f(memory.Value)
		if err != nil {
			return err
		}
		memory.Value = value
		_, err = memory.Commit()
		if err != ErrStaleData {
			return err
		}
	}
}

// Release releases the Lock on the Repo.
func (lock *Lock) Release() error {
	return lock.repo.Unlock(lock)
}

// Atomically modify and commit this piece of memory, retrying until it
// succeeds.
func (handle Handle) Atomically(f func(interface{}) (interface{}, error)) error {
	return handle.canonical.repo.Checkout(handle).Atomically(f)
}

// Checkout creates a new local copy of the piece of memory associated with
// the Handle.
func (handle Handle) Checkout() *Memory {
	return handle.canonical.repo.Checkout(handle)
}
