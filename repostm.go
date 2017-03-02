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
	"sync"
)

// Handle identifies particular piece of memory and may be shared.
type Handle struct {
	canonical *canonical
}

// Memory is a local copy of a particular piece of memory and must not be
// shared.
type Memory struct {
	canonical   *canonical
	version     MemoryVersion
	repoVersion RepoVersion
	bytes       []byte
	Value       interface{}
	flag        bool
}

// Repo is a repository of shared memory.
type Repo struct {
	version            RepoVersion
	mutex              sync.Mutex
	repoLock           *repoLock
	waitForCommit      sync.Cond
	waitForUnlock      sync.Cond
	waitForWriteUnlock sync.Cond
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
	repo        *Repo
	mutex       sync.RWMutex
	writeLocked bool
	version     MemoryVersion
	typ         reflect.Type
	bytes       []byte
}

// Lock is returned when locking a Repo. Commits without the Lock will fail
// until the Lock is released.
type Lock struct {
	repoLock *repoLock
}

type repoLock struct {
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
	// ErrRetryAfterCommit should be returned by functions passed into
	// Atomically to wait for another commit and then retry.
	ErrRetryAfterCommit = errors.New("repostm: Retry after commit")
	// ErrTypeChanged is returned if the type of Memory.Value is not
	// the same as the type of the value used for Add.
	ErrTypeChanged = errors.New("repostm: Type changed")
)

// New creates a new Repo.
func New() *Repo {
	repo := &Repo{}
	repo.waitForCommit.L = &repo.mutex
	repo.waitForUnlock.L = &repo.mutex
	repo.waitForWriteUnlock.L = &repo.mutex
	return repo
}

func (repo *Repo) validate(memory []*Memory) error {
	for _, m := range memory {
		if m.canonical.repo != repo {
			return ErrWrongRepo
		}
		if !reflect.TypeOf(m.Value).AssignableTo(m.canonical.typ) {
			return ErrTypeChanged
		}
		m.flag = false
	}
	return nil
}

func (repo *Repo) readLock(memory []*Memory) (RepoVersion, error) {
	if err := repo.validate(memory); err != nil {
		return RepoVersion{}, err
	}
	repo.mutex.Lock()
	defer repo.mutex.Unlock()
checkingForWriteLocks:
	for {
		for _, m := range memory {
			if m.canonical.writeLocked {
				repo.waitForWriteUnlock.Wait()
				continue checkingForWriteLocks
			}
		}
		break
	}
	for _, m := range memory {
		m.canonical.mutex.RLock()
	}
	return repo.version, nil
}

func readUnlock(memory []*Memory) {
	for _, m := range memory {
		m.canonical.mutex.RUnlock()
	}
}

func (repo *Repo) writeLock(repoLock *repoLock, memory []*Memory) error {
	repo.mutex.Lock()
	defer repo.mutex.Unlock()
	if repoLock != repo.repoLock {
		if repoLock == nil {
			return ErrLocked
		} else {
			return ErrInvalidLock
		}
	}
	for _, m := range memory {
		if m.canonical.writeLocked {
			return ErrStaleData
		}
	}
	for _, m := range memory {
		m.canonical.writeLocked = true
		m.canonical.mutex.Lock()
	}
	return nil
}

func (repo *Repo) writeUnlock(bumpVersion bool, memory []*Memory) RepoVersion {
	repo.mutex.Lock()
	defer repo.mutex.Unlock()
	if bumpVersion {
		repo.version.version++
		repo.waitForCommit.Broadcast()
	}
	for _, m := range memory {
		m.canonical.writeLocked = false
		m.canonical.mutex.Unlock()
	}
	repo.waitForWriteUnlock.Broadcast()
	return repo.version
}

// Update overwrites the local copies of memory with out of date data with
// the latest data in the Repo.  Up to date copies are not modified.
func (repo *Repo) Update(memory ...*Memory) (RepoVersion, error) {
	repoVersion, err := repo.readLock(memory)
	if err != nil {
		return repoVersion, err
	}
	for _, m := range memory {
		if m.version != m.canonical.version {
			m.flag = true
			m.version = m.canonical.version
			m.bytes = append(m.bytes[0:0], m.canonical.bytes...)
		}
	}
	readUnlock(memory)
	for _, m := range memory {
		m.repoVersion = repoVersion
		if m.flag {
			m.Reset()
		}
	}
	return repoVersion, nil
}

// Revert overwrites the local copies of memory with the latest data in the
// Repo, including up to date copies.
func (repo *Repo) Revert(memory ...*Memory) (RepoVersion, error) {
	repoVersion, err := repo.readLock(memory)
	if err != nil {
		return repoVersion, err
	}
	for _, m := range memory {
		m.version = m.canonical.version
		m.bytes = append(m.bytes[0:0], m.canonical.bytes...)
	}
	readUnlock(memory)
	for _, m := range memory {
		m.repoVersion = repoVersion
		m.Reset()
	}
	return repoVersion, nil
}

// Commit commits the local copies of memory to the Repo.  If the commit
// fails due to being out of date, the local copies are updated to the
// latest data in the Repo.
func (repo *Repo) Commit(memory ...*Memory) (RepoVersion, error) {
	return repo.commit(nil, memory)
}

// CommitWithLock commits the local copies of memory to the Repo, which is
// locked with the Lock.  If the commit fails due to being out of date, the
// local copies are updated to the latest data in the Repo.
func (repo *Repo) CommitWithLock(lock Lock, memory ...*Memory) (RepoVersion, error) {
	return repo.commit(lock.repoLock, memory)
}

func (repo *Repo) commit(repoLock *repoLock, memory []*Memory) (RepoVersion, error) {
	if err := repo.validate(memory); err != nil {
		return RepoVersion{}, err
	}
	var b bytes.Buffer
	for _, m := range memory {
		b.Reset()
		if err := gob.NewEncoder(&b).Encode(m.Value); err != nil {
			panic(err)
		}
		newBytes := b.Bytes()
		if bytes.Compare(newBytes, m.bytes) != 0 {
			m.flag = true
			m.bytes = append(m.bytes[0:0], newBytes...)
		}
	}

	err := repo.writeLock(repoLock, memory)
	if err != nil {
		version, err2 := repo.Revert(memory...)
		if err2 != nil {
			panic(err2)
		}
		return version, err
	}
	for _, m := range memory {
		if m.version != m.canonical.version {
			err = ErrStaleData
			break
		}
	}
	if err == nil {
		for _, m := range memory {
			if m.flag {
				m.canonical.version.version++
				m.version = m.canonical.version
				m.canonical.bytes = append(m.canonical.bytes[0:0], m.bytes...)
			}
		}
	} else {
		for _, m := range memory {
			if m.flag || m.version != m.canonical.version {
				m.flag = true
				m.version = m.canonical.version
				m.bytes = append(m.bytes[0:0], m.canonical.bytes...)
			}
		}

	}
	repoVersion := repo.writeUnlock(err == nil, memory)

	if err != nil {
		for _, m := range memory {
			if m.flag {
				m.Reset()
			}
		}
	}
	for _, m := range memory {
		m.repoVersion = repoVersion
	}
	return repoVersion, err

}

// Lock locks the Repo.
func (repo *Repo) Lock() (Lock, error) {
	repo.mutex.Lock()
	defer repo.mutex.Unlock()
	if repo.repoLock != nil {
		return Lock{}, ErrLocked
	}
	repo.repoLock = &repoLock{repo}
	return Lock{repo.repoLock}, nil
}

// Unlock unlocks the Repo.
func (repo *Repo) Unlock(lock Lock) error {
	if lock.repoLock == nil {
		return ErrInvalidLock
	}
	repo.mutex.Lock()
	defer repo.mutex.Unlock()
	if lock.repoLock != repo.repoLock {
		return ErrInvalidLock
	}
	repo.repoLock = nil
	repo.waitForUnlock.Broadcast()
	return nil
}

// WaitForUnlock waits until the Repo is unlocked.
func (repo *Repo) WaitForUnlock() {
	repo.mutex.Lock()
	defer repo.mutex.Unlock()
	for repo.repoLock != nil {
		repo.waitForUnlock.Wait()
	}
}

// WaitForCommit waits until the Repo has seen a commit since the given
// RepoVersion.
func (repo *Repo) WaitForCommit(version RepoVersion) RepoVersion {
	repo.mutex.Lock()
	defer repo.mutex.Unlock()
	for repo.version == version {
		repo.waitForCommit.Wait()
	}
	return repo.version
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
	return Handle{&canonical{
		repo:  repo,
		bytes: b.Bytes(),
		typ:   reflect.TypeOf(value),
	}}
}

// Atomically modify and commit the memory, retrying until it succeeds.
func (repo *Repo) Atomically(update func() error, memory ...*Memory) (RepoVersion, error) {
	version, err := repo.Update(memory...)
	if err != nil {
		return version, err
	}
	for {
		err = update()
		if err == ErrRetryAfterCommit {
			version = repo.WaitForCommit(version)
			version, err = repo.Update(memory...)
			if err != nil {
				return version, err
			}
		} else if err != nil {
			return version, err
		} else {
			version, err = repo.Commit(memory...)
			if err != ErrStaleData {
				return version, err
			}
		}
	}
}

// Checkout creates a new local copy of the piece of memory associated with
// the Handle.
func (repo *Repo) Checkout(handle ...Handle) []*Memory {
	memory := make([]*Memory, len(handle))
	for i, h := range handle {
		memory[i] = &Memory{
			canonical: h.canonical,
			Value:     reflect.Zero(h.canonical.typ).Interface(),
		}
	}
	if _, err := repo.Revert(memory...); err != nil {
		panic(err)
	}
	return memory
}

// Handle returns the Handle referring to the piece of memory.
func (memory *Memory) Handle() Handle {
	return Handle{memory.canonical}
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
func (memory *Memory) CommitWithLock(lock Lock) (RepoVersion, error) {
	return memory.canonical.repo.CommitWithLock(lock, memory)
}

// Reset resets local copy of the Value to its value at the last Update
// or Commit.
func (memory *Memory) Reset() {
	value := reflect.New(reflect.TypeOf(memory.Value))
	if err := gob.NewDecoder(bytes.NewReader(memory.bytes)).DecodeValue(value); err != nil {
		panic(err)
	}
	memory.Value = reflect.Indirect(value).Interface()
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
func (memory *Memory) Atomically(f func(interface{}) (interface{}, error)) (RepoVersion, error) {
	return memory.canonical.repo.Atomically(func() error {
		value, err := f(memory.Value)
		if err != nil {
			return err
		}
		memory.Value = value
		return nil
	}, memory)
}

// Release releases the Lock on the Repo.
func (lock Lock) Release() error {
	if lock.repoLock == nil {
		return ErrInvalidLock
	}
	return lock.repoLock.repo.Unlock(lock)
}

// Atomically modify and commit this piece of memory, retrying until it
// succeeds.
func (handle Handle) Atomically(f func(interface{}) (interface{}, error)) (RepoVersion, error) {
	return handle.canonical.repo.Checkout(handle)[0].Atomically(f)
}

// Checkout creates a new local copy of the piece of memory associated with
// the Handle.
func (handle Handle) Checkout() *Memory {
	return handle.canonical.repo.Checkout(handle)[0]
}
