// Copyright (C) 2014 Daniel Harrison

package hfile

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"log"
	"sort"
)

type Scanner struct {
	reader  *Reader
	idx     int
	buf     *bytes.Reader
	lastKey *[]byte
}

func NewScanner(r *Reader) Scanner {
	return Scanner{r, 0, nil, nil}
}

func (s *Scanner) Reset() {
	s.idx = 0
	s.buf = nil
	s.lastKey = nil
}

func (s *Scanner) findBlock(key []byte) int {
	remaining := len(s.reader.index) - s.idx - 1
	if s.reader.debug {
		log.Printf("[Scanner.findBlock] cur %d, remaining %d\n", s.idx, remaining)
	}

	if remaining <= 0 {
		if s.reader.debug {
			log.Println("[Scanner.findBlock] last block")
		}
		return s.idx // s.cur is the last block, so it is only choice.
	}

	if s.reader.index[s.idx+1].IsAfter(key) {
		if s.reader.debug {
			log.Println("[Scanner.findBlock] next block is past key")
		}
		return s.idx
	}

	offset := sort.Search(remaining, func(i int) bool {
		return s.reader.index[s.idx+i+1].IsAfter(key)
	})

	return s.idx + offset
}

func (s *Scanner) CheckIfKeyOutOfOrder(key []byte) error {
	if s.lastKey != nil && bytes.Compare(*s.lastKey, key) > 0 {
		return fmt.Errorf("Keys our of order! %v > %v", *s.lastKey, key)
	}
	s.lastKey = &key
	return nil
}

func (s *Scanner) blockFor(key []byte) (*bytes.Reader, error, bool) {
	err := s.CheckIfKeyOutOfOrder(key)
	if err != nil {
		return nil, err, false
	}

	if s.reader.index[s.idx].IsAfter(key) {
		if s.reader.debug {
			log.Printf("[Scanner.blockFor] curBlock after key %s (cur: %d, start: %s)\n",
				hex.EncodeToString(key),
				s.idx,
				hex.EncodeToString(s.reader.index[s.idx].firstKeyBytes),
			)
		}
		return nil, nil, false
	}

	idx := s.findBlock(key)
	if s.reader.debug {
		log.Printf("[Scanner.blockFor] findBlock (key: %s) picked %d (starts: %s). Cur: %d (starts: %s)\n",
			hex.EncodeToString(key),
			idx,
			hex.EncodeToString(s.reader.index[idx].firstKeyBytes),
			s.idx,
			hex.EncodeToString(s.reader.index[s.idx].firstKeyBytes),
		)
	}

	if idx != s.idx || s.buf == nil { // need to load a new block
		data, err := s.reader.GetBlock(idx)
		if err != nil {
			if s.reader.debug {
				log.Printf("[Scanner.blockFor] read err %s (key: %s, idx: %d, start: %s)\n",
					err,
					hex.EncodeToString(key),
					idx,
					hex.EncodeToString(s.reader.index[idx].firstKeyBytes),
				)
			}
			return nil, err, false
		}
		s.idx = idx
		s.buf = data
	} else {
		if s.reader.debug {
			log.Println("[Scanner.blockFor] Re-using current block")
		}
	}

	return s.buf, nil, true
}

func (s *Scanner) GetFirst(key []byte) ([]byte, error, bool) {
	data, err, ok := s.blockFor(key)

	if !ok {
		if s.reader.debug {
			log.Printf("[Scanner.GetFirst] No Block for key: %s (err: %s, found: %s)\n", hex.EncodeToString(key), err, ok)
		}
		return nil, err, ok
	}

	value, _, found := s.getValuesFromBuffer(data, key, true)
	return value, nil, found
}

func (s *Scanner) GetAll(key []byte) ([][]byte, error) {
	data, err, ok := s.blockFor(key)

	if !ok {
		if s.reader.debug {
			log.Printf("[Scanner.GetAll] No Block for key: %s (err: %s, found: %s)\n", hex.EncodeToString(key), err, ok)
		}
		return nil, err
	}

	_, found, _ := s.getValuesFromBuffer(data, key, false)
	return found, err
}

func (s *Scanner) getValuesFromBuffer(buf *bytes.Reader, key []byte, first bool) ([]byte, [][]byte, bool) {
	var acc [][]byte

	if s.reader.debug {
		log.Printf("[Scanner.getValuesFromBuffer] buf before %d\n", buf.Len())
	}

	for buf.Len() > 0 {
		var keyLen, valLen uint32
		binary.Read(buf, binary.BigEndian, &keyLen)
		binary.Read(buf, binary.BigEndian, &valLen)
		keyBytes := make([]byte, keyLen)
		valBytes := make([]byte, valLen)
		buf.Read(keyBytes)
		buf.Read(valBytes)
		cmp := bytes.Compare(keyBytes, key)
		if cmp == 0 {
			if first {
				if s.reader.debug {
					log.Printf("[Scanner.getValuesFromBuffer] buf after %d\n", buf.Len())
				}
				return valBytes, nil, true
			} else {
				acc = append(acc, valBytes)
			}
		}
		if cmp > 0 {
			if s.reader.debug {
				log.Printf("[Scanner.getValuesFromBuffer] past key %s vs %s. buf remaining %d\n",
					hex.EncodeToString(key),
					hex.EncodeToString(keyBytes),
					buf.Len(),
				)
			}
			buf.Seek(-(int64(keyLen + valLen + 8)), 1)
			return nil, acc, len(acc) > 0
		}
	}
	if s.reader.debug {
		log.Printf("[Scanner.getValuesFromBuffer] walked off block\n")
	}
	return nil, acc, len(acc) > 0
}
