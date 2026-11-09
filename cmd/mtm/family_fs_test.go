package main

import (
	"testing"

	"ofam.dev/oden"
)

var _ oden.CachedFS = familyFS{}

func TestFamilyFSDiscoverCacheKey(t *testing.T) {
	t.Parallel()
	a := familyFS{hidden: map[string]bool{"o-proj": true, "o-x": true}}
	b := familyFS{hidden: map[string]bool{"o-x": true, "o-proj": true}}
	for i := 0; i < 20; i++ {
		if a.DiscoverCacheKey() != b.DiscoverCacheKey() {
			t.Fatal("key depends on map order")
		}
	}
	if a.DiscoverCacheKey() == (familyFS{hidden: map[string]bool{"o-proj": true}}).DiscoverCacheKey() {
		t.Fatal("key must change with the hidden set")
	}
	if (familyFS{}).DiscoverCacheKey() == "" {
		t.Fatal("empty hidden set must still have a key")
	}
}
