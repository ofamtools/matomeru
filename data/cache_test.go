package data

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestReadCache(t *testing.T) {
	at := time.Date(2026, 11, 22, 9, 0, 0, 0, time.UTC)
	c := &ReadCache{Now: func() time.Time { return at }}
	if _, ok := c.Get("links"); ok || c.MarkStale("links") {
		t.Fatal("an empty cache has the key")
	}
	items := []Record{{Kind: "links.bookmark", ID: "a"}, {Kind: "links.bookmark", ID: "b"}}
	first := c.Put("links", items, 7)
	if first.Generation != 1 || first.Total != 7 || first.Stale || !first.UpdatedAt.Equal(at) || len(first.Items) != 2 {
		t.Fatalf("first = %+v", first)
	}
	// What is read is a copy: changing it, or what was put, changes nothing held.
	items[0].ID, first.Items[1].ID = "changed", "changed"
	got, _ := c.Get("links")
	if got.Items[0].ID != "a" || got.Items[1].ID != "b" {
		t.Fatalf("the cache shares its items: %+v", got.Items)
	}
	if !c.MarkStale("links") {
		t.Fatal("MarkStale did not find the key")
	}
	if got, _ = c.Get("links"); !got.Stale || got.Generation != 1 || len(got.Items) != 2 {
		t.Fatalf("stale = %+v", got)
	}
	if second := c.Put("links", nil, 0); second.Generation != 2 || second.Stale {
		t.Fatalf("second = %+v", second)
	}
	c.Drop("links")
	if _, ok := c.Get("links"); ok || len(c.Keys()) != 0 {
		t.Fatal("Drop kept the key")
	}
}

func TestReadCacheFetchEffect(t *testing.T) {
	c := NewReadCache()
	eff := c.FetchEffect("ext.links", "links", func(context.Context) ([]Record, int, error) {
		return []Record{{Kind: "links.bookmark", ID: "a"}}, 1, nil
	})
	if eff.Slot != "ext.links" || eff.Command != CmdCacheFetched {
		t.Fatalf("effect = %+v", eff)
	}
	v, err := eff.Run(context.Background())
	snap, ok := v.(Snapshot)
	if err != nil || !ok || snap.Generation != 1 || snap.Total != 1 {
		t.Fatalf("run = %+v, %v", v, err)
	}
	// A fetch that fails keeps the items and marks them stale.
	boom := errors.New("tool is offline")
	if _, err := c.FetchEffect("ext.links", "links", func(context.Context) ([]Record, int, error) { return nil, 0, boom }).Run(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if got, _ := c.Get("links"); !got.Stale || len(got.Items) != 1 || got.Generation != 1 {
		t.Fatalf("after a failed fetch = %+v", got)
	}
	// A fetch whose slot was taken by a newer one stores nothing.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.FetchEffect("ext.links", "links", func(context.Context) ([]Record, int, error) {
		return []Record{{ID: "late"}}, 1, nil
	}).Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled fetch: %v", err)
	}
	if got, _ := c.Get("links"); got.Generation != 1 || got.Items[0].ID != "a" {
		t.Fatalf("a cancelled fetch was stored: %+v", got)
	}
}

func TestReadCacheConcurrent(t *testing.T) {
	c := NewReadCache()
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			for range 200 {
				c.Put("k", []Record{{ID: "x"}}, i)
				c.MarkStale("k")
				c.Get("k")
				c.Keys()
			}
		})
	}
	wg.Wait()
	if got, _ := c.Get("k"); got.Generation != 1600 {
		t.Fatalf("generation = %d, want 1600", got.Generation)
	}
}
