package cache

import (
	"container/list"
	"sync"
)

type entry[K comparable, V any] struct {
	key   K
	value V
}

type LRU[K comparable, V any] struct {
	mu      sync.Mutex
	maximum int
	entries map[K]*list.Element
	order   *list.List
}

func NewLRU[K comparable, V any](maximum int) *LRU[K, V] {
	if maximum < 1 {
		maximum = 1
	}
	return &LRU[K, V]{
		maximum: maximum,
		entries: make(map[K]*list.Element, maximum),
		order:   list.New(),
	}
}

func (cache *LRU[K, V]) Get(key K) (V, bool) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	element, ok := cache.entries[key]
	if !ok {
		var zero V
		return zero, false
	}
	cache.order.MoveToFront(element)
	return element.Value.(entry[K, V]).value, true
}

func (cache *LRU[K, V]) Put(key K, value V) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if element, ok := cache.entries[key]; ok {
		element.Value = entry[K, V]{key: key, value: value}
		cache.order.MoveToFront(element)
		return
	}
	element := cache.order.PushFront(entry[K, V]{key: key, value: value})
	cache.entries[key] = element
	if cache.order.Len() <= cache.maximum {
		return
	}
	oldest := cache.order.Back()
	delete(cache.entries, oldest.Value.(entry[K, V]).key)
	cache.order.Remove(oldest)
}

func (cache *LRU[K, V]) Len() int {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.order.Len()
}

func (cache *LRU[K, V]) Delete(key K) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	element, ok := cache.entries[key]
	if !ok {
		return
	}
	delete(cache.entries, key)
	cache.order.Remove(element)
}

func (cache *LRU[K, V]) Clear() {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.entries = make(map[K]*list.Element, cache.maximum)
	cache.order.Init()
}
