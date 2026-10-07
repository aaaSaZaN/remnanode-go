package hashedset

import (
	"strconv"
	"strings"
)

type HashedSet struct {
	items    map[string]struct{}
	hashHigh int32
	hashLow  int32
}

// new creates a new hashedset
func New(initial ...string) *HashedSet {
	hs := &HashedSet{
		items: make(map[string]struct{}),
	}
	for _, item := range initial {
		hs.Add(item)
	}
	return hs
}

// djb2 dual hash
func djb2Dual(str string) (int32, int32) {
	var high int32 = 5381
	var low int32 = 5387

	for i := 0; i < len(str); i++ {
		char := int32(str[i])
		high = (high << 5) + high + char
		low = (low << 6) + low + char*37
	}

	return high, low
}

// add inserts a string into the set
func (hs *HashedSet) Add(str string) bool {
	if _, exists := hs.items[str]; exists {
		return false
	}
	hs.items[str] = struct{}{}
	h, l := djb2Dual(str)
	hs.hashHigh ^= h
	hs.hashLow ^= l
	return true
}

// delete removes a string from the set
func (hs *HashedSet) Delete(str string) bool {
	if _, exists := hs.items[str]; !exists {
		return false
	}
	delete(hs.items, str)
	h, l := djb2Dual(str)
	hs.hashHigh ^= h
	hs.hashLow ^= l
	return true
}

// has returns true if string is in the set
func (hs *HashedSet) Has(str string) bool {
	_, exists := hs.items[str]
	return exists
}

// clear resets the set and its accumulator
func (hs *HashedSet) Clear() {
	hs.items = make(map[string]struct{})
	hs.hashHigh = 0
	hs.hashLow = 0
}

// size returns the count of items in the set
func (hs *HashedSet) Size() int {
	return len(hs.items)
}

// hashHigh returns the high 32 bits as signed int32
func (hs *HashedSet) HashHigh() int32 {
	return hs.hashHigh
}

// hashLow returns the low 32 bits as signed int32
func (hs *HashedSet) HashLow() int32 {
	return hs.hashLow
}

func padStartHex(val int32) string {
	s := strconv.FormatInt(int64(val), 16)
	if len(s) < 8 {
		s = strings.Repeat("0", 8-len(s)) + s
	}
	return s
}

// hash64String returns string representation
func (hs *HashedSet) Hash64String() string {
	return padStartHex(hs.hashHigh) + padStartHex(hs.hashLow)
}

// elements returns a slice of all items in the set
func (hs *HashedSet) Elements() []string {
	res := make([]string, 0, len(hs.items))
	for k := range hs.items {
		res = append(res, k)
	}
	return res
}
