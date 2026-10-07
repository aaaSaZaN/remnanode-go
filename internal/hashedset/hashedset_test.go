package hashedset

import (
	"testing"
)

func TestHashedSetParity(t *testing.T) {
	hs := New("user-1-uuid", "user-2-uuid", "test-user")
	expected := "-64bc79b014c680c2"
	if hs.Hash64String() != expected {
		t.Fatalf("expected %s, got %s", expected, hs.Hash64String())
	}

	// test delete
	hs.Delete("test-user")
	// if we re-add test-user, it should match expected again
	hs.Add("test-user")
	if hs.Hash64String() != expected {
		t.Fatalf("after re-add expected %s, got %s", expected, hs.Hash64String())
	}
}
