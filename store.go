package raftwal

import (
	"encoding/binary"
	"sync"

	"github.com/hashicorp/raft"
	"github.com/tidwall/wal"
)

// Level of durability
type Level int

// Level of durability
const (
	Low    Level = -1
	Medium Level = 0
	High   Level = 1
)

// LogStore is a write ahead Raft log
type LogStore struct {
	mu    sync.Mutex
	log   *wal.Log
	buf   []byte
	batch wal.Batch
}

var _ raft.LogStore = &LogStore{}

// Open the Raft log
func Open(path string, durability Level) (*LogStore, error) {
	s := new(LogStore)
	opts := *wal.DefaultOptions
	opts.Durability = wal.Durability(durability)
	// opts.LogFormat = wal.JSON
	var err error
	s.log, err = wal.Open(path, &opts)
	if err != nil {
		return nil, err
	}
	return s, nil
}

// Close the Raft log
func (s *LogStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.log.Close()
}

// FirstIndex returns the first known index from the Raft log.
func (s *LogStore) FirstIndex() (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.log.FirstIndex()
}

// LastIndex returns the last known index from the Raft log.
func (s *LogStore) LastIndex() (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.log.LastIndex()
}

// GetLog is used to retrieve a log from FastLogDB at a given index.
func (s *LogStore) GetLog(index uint64, log *raft.Log) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.log.Read(index)
	if err != nil {
		if err == wal.ErrNotFound {
			return raft.ErrLogNotFound
		}
		return err
	}
	log.Index = index
	if len(data) == 0 {
		return wal.ErrCorrupt
	}
	log.Type = raft.LogType(data[0])
	data = data[1:]
	var n int
	log.Term, n = binary.Uvarint(data)
	if n <= 0 {
		return wal.ErrCorrupt
	}
	data = data[n:]
	size, n := binary.Uvarint(data)
	if n <= 0 {
		return wal.ErrCorrupt
	}
	data = data[n:]
	if uint64(len(data)) < size {
		return wal.ErrCorrupt
	}
	log.Data = data[:size]
	data = data[size:]
	size, n = binary.Uvarint(data)
	if n <= 0 {
		return wal.ErrCorrupt
	}
	data = data[n:]
	if uint64(len(data)) < size {
		return wal.ErrCorrupt
	}
	log.Extensions = data[:size]
	data = data[size:]
	if len(data) > 0 {
		return wal.ErrCorrupt
	}
	return nil
}

func appendUvarint(dst []byte, x uint64) []byte {
	var buf [10]byte
	n := binary.PutUvarint(buf[:], x)
	dst = append(dst, buf[:n]...)
	return dst
}

func appendLog(dst []byte, log *raft.Log) []byte {
	dst = append(dst, byte(log.Type))
	dst = appendUvarint(dst, log.Term)
	dst = appendUvarint(dst, uint64(len(log.Data)))
	dst = append(dst, log.Data...)
	dst = appendUvarint(dst, uint64(len(log.Extensions)))
	dst = append(dst, log.Extensions...)
	return dst
}

// StoreLog is used to store a single raft log
func (s *LogStore) StoreLog(log *raft.Log) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.buf = s.buf[:0]
	s.buf = appendLog(s.buf, log)
	return s.log.Write(log.Index, s.buf)
}

// StoreLogs is used to store a set of raft logs
func (s *LogStore) StoreLogs(logs []*raft.Log) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.batch.Clear()
	for _, log := range logs {
		s.buf = s.buf[:0]
		s.buf = appendLog(s.buf, log)
		s.batch.Write(log.Index, s.buf)
	}
	return s.log.WriteBatch(&s.batch)
}

// DeleteRange is used to delete logs within a given range inclusively.
func (s *LogStore) DeleteRange(min, max uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	first, err := s.log.FirstIndex()
	if err != nil {
		return err
	}
	last, err := s.log.LastIndex()
	if err != nil {
		return err
	}
	if min == first {
		if err := s.log.TruncateFront(max + 1); err != nil {
			return err
		}
	} else if max == last {
		if err := s.log.TruncateBack(min - 1); err != nil {
			return err
		}
	} else {
		return wal.ErrOutOfRange
	}
	return nil
}
