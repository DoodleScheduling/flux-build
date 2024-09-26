package cache

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestCache(t *testing.T) {
	g := NewWithT(t)
	// create a cache that can hold 2 items
	cache := New[string]()

	// Get an Item from the cache
	if _, found := cache.Get("key1"); found {
		t.Error("Item should not be found")
	}

	// Add an item to the cache
	err := cache.Add("key1", "value1")
	g.Expect(err).ToNot(HaveOccurred())

	// Get the item from the cache
	item, found := cache.Get("key1")
	g.Expect(found).To(BeTrue())
	g.Expect(item).To(Equal("value1"))

	// Add another item to the cache
	err = cache.Add("key2", "value2")
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(cache.ItemCount()).To(Equal(2))

	// Get the item from the cache
	item, found = cache.Get("key2")
	g.Expect(found).To(BeTrue())
	g.Expect(item).To(Equal("value2"))

	//Add an item to the cache
	err = cache.Add("key3", "value3")
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(cache.ItemCount()).To(Equal(3))

	// Replace an item in the cache
	cache.Set("key2", "value3")

	// Get the item from the cache
	item, found = cache.Get("key2")
	g.Expect(found).To(BeTrue())
	g.Expect(item).To(Equal("value3"))

	// new int cache
	cache2 := New[int]()

	// Add an item to the cache
	err = cache2.Add(1, "value1")
	g.Expect(err).ToNot(HaveOccurred())

	// Get the item from the cache
	item, found = cache2.Get(1)
	g.Expect(found).To(BeTrue())
	g.Expect(item).To(Equal("value1"))

	item, found = cache2.GetOrLock(3)
	g.Expect(found).To(BeFalse())

	go func() {
		// Locks until item is set.
		item, found = cache2.GetOrLock(3)
		g.Expect(found).To(BeTrue())
		g.Expect(item).To(Equal("value3"))
	}()

	cache2.SetUnlock(3, "value3")
}
