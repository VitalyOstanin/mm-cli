package cli

import "testing"

func TestLRUAddEvictsLeastRecentlyUsed(t *testing.T) {
	c := newSeenSet(2)
	if !c.add("a") || !c.add("b") {
		t.Fatal("new keys must report true")
	}
	if c.add("a") {
		t.Fatal("repeated key must report false")
	}
	c.add("c")
	if c.has("b") || !c.has("a") || !c.has("c") {
		t.Fatalf("want b evicted, a and c kept: a=%v b=%v c=%v", c.has("a"), c.has("b"), c.has("c"))
	}
}

func TestLRUGetPutValues(t *testing.T) {
	c := newLRU[int](2)
	c.put("a", 1)
	c.put("b", 2)
	if v, ok := c.get("a"); !ok || v != 1 {
		t.Fatalf("get a = %d, %v", v, ok)
	}
	c.put("c", 3) // b is the least recently used
	if _, ok := c.get("b"); ok {
		t.Fatal("b must be evicted")
	}
	c.put("a", 10)
	if v, _ := c.get("a"); v != 10 {
		t.Fatalf("a not replaced: %d", v)
	}
	if _, ok := c.get("zzz"); ok {
		t.Fatal("missing key found")
	}
}

func TestPostCacheKeepsNegativeEntry(t *testing.T) {
	c := newPostCache(2)
	c.put(postInfo{id: "p1"})
	if p, ok := c.get("p1"); !ok || p.author != "" {
		t.Fatalf("negative entry lost: %+v %v", p, ok)
	}
}
