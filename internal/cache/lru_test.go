package cache

import (
	"strconv"
	"sync"
	"testing"
)

func TestLRUEvictsLeastRecentlyUsedEntry(t *testing.T) {
	cache := NewLRU[string, int](2)
	cache.Put("one", 1)
	cache.Put("two", 2)
	if _, ok := cache.Get("one"); !ok {
		t.Fatal("expected cache hit")
	}
	cache.Put("three", 3)
	if _, ok := cache.Get("two"); ok {
		t.Fatal("least recently used entry survived eviction")
	}
	if value, ok := cache.Get("one"); !ok || value != 1 {
		t.Fatalf("retained entry = %d, %t", value, ok)
	}
}

func TestLRUIsBoundedUnderConcurrentUse(t *testing.T) {
	cache := NewLRU[string, int](8)
	var wait sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			for index := 0; index < 100; index++ {
				key := strconv.Itoa((worker + index) % 32)
				cache.Put(key, index)
				cache.Get(key)
			}
		}(worker)
	}
	wait.Wait()
	if size := cache.Len(); size > 8 {
		t.Fatalf("cache size = %d", size)
	}
	cache.Clear()
	if cache.Len() != 0 {
		t.Fatal("cache was not cleared")
	}
}
