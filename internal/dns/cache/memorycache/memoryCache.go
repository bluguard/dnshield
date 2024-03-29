package memorycache

import (
	"context"
	"errors"
	"hash/fnv"
	"log"
	"net"
	"sync"
	"time"

	"github.com/bluguard/dnshield/internal/dns/cache"
	"github.com/bluguard/dnshield/internal/dns/dto"
)

// estimate cost of one entry is 50 bytes
const cost int64 = 50
const defaultTTL = 60

const (
	v4Suffix = "_v4"
	v6Suffix = "_v6"
)

var _ cache.Cache = &MemoryCache{}

// MemoryCache an in memory cache implementation
type MemoryCache struct {
	memory          map[uint32]net.IP
	lock            *sync.RWMutex
	deadlines       *deadlineFolder
	remainingMemory int64
	totalCapacity   int64
	baseTTL         uint32
	forceBaseTTL    bool
}

// NewMemoryCache instantiate a new cache
func NewMemoryCache(ctx context.Context, wg *sync.WaitGroup, size int64, baseTTL uint32, forceTTL bool, gcDelay time.Duration) *MemoryCache {
	res := MemoryCache{
		memory:          make(map[uint32]net.IP),
		lock:            &sync.RWMutex{},
		deadlines:       &deadlineFolder{memory: make([]deadline, 0, 50)},
		remainingMemory: size,
		totalCapacity:   size,
		baseTTL:         baseTTL,
		forceBaseTTL:    forceTTL,
	}

	wg.Add(1)
	if baseTTL > 0 {
		go gcScheduler(ctx, wg, &res, gcDelay)
	} else {
		wg.Done()
	}

	return &res
}

// ResolveV4 implements cache.Cache
func (c *MemoryCache) ResolveV4(name string) (dto.Record, error) {
	ip, err := c.resolve(name + v4Suffix)
	if err != nil {
		return dto.Record{}, err
	}
	return dto.Record{
		Name:  name,
		Type:  dto.A,
		Class: dto.IN,
		TTL:   defaultTTL,
		Data:  ip.To4(),
	}, nil
}

// ResolveV6 implements cache.Cache
func (c *MemoryCache) ResolveV6(name string) (dto.Record, error) {
	ip, err := c.resolve(name + v6Suffix)
	if err != nil {
		return dto.Record{}, err
	}
	return dto.Record{
		Name:  name,
		Type:  dto.AAAA,
		Class: dto.IN,
		TTL:   defaultTTL,
		Data:  ip.To16(),
	}, nil
}

func (c *MemoryCache) resolve(name string) (net.IP, error) {
	res := c.get(name)
	if res == nil {
		return nil, errors.New("no entry found for " + name)
	}
	return res, nil
}

// Feed implements cache.Cache
func (c *MemoryCache) Feed(record dto.Record) {
	if c.totalCapacity < cost {
		return
	}
	ttl := record.TTL
	if record.TTL < c.baseTTL {
		if !c.forceBaseTTL {
			return
		}
		ttl = c.baseTTL // force to the minimum ttl
	}
	c.put(computeName(record.Name, record.Type), computeData(record.Data, record.Type), time.Duration(ttl)*time.Second)
}

// Clear implements cache.Cache
func (c *MemoryCache) Clear() {
	c.lock.Lock()
	defer c.lock.Unlock()
	for k := range c.memory {
		delete(c.memory, k)
	}
	c.deadlines.shiftLeftOf(len(c.deadlines.memory))
}

func (c *MemoryCache) put(key string, address net.IP, ttl time.Duration) {

	c.lock.Lock()
	defer c.lock.Unlock()

	if c.remainingMemory < cost {
		log.Println("cache is full")
		c.freeNextDeadline()
	} else {
		c.remainingMemory -= cost
	}

	hkey := hash(key)
	if _, ok := c.memory[hkey]; ok {
		return
	}
	c.memory[hkey] = address
	c.deadlines.insert(deadline{expiry: time.Now().Add(ttl), key: hkey})
}

func (c *MemoryCache) get(key string) net.IP {
	c.lock.RLock()
	defer c.lock.RUnlock()
	res, ok := c.memory[hash(key)]
	if !ok {
		return nil
	}
	return res
}

func (c *MemoryCache) gc() {
	c.lock.Lock()
	start := time.Now()
	log.Println("trigger gc")
	defer c.lock.Unlock()
	count := 0
	now := time.Now()
	for _, d := range c.deadlines.memory {
		if !d.expiry.Before(now) {
			// the list of deadlines is sorted, no need to range over all elements
			break
		}

		count++
		delete(c.memory, d.key)
	}
	i := count
	c.deadlines.shiftLeftOf(i)
	log.Println("GC cleared", count, "entries in", time.Since(start))
	c.remainingMemory += cost * int64(count)
}

func (c *MemoryCache) freeNextDeadline() {
	delete(c.memory, c.deadlines.memory[0].key)
	c.deadlines.shiftLeftOf(1)
}

func hash(s string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return h.Sum32()
}
func computeName(s string, t dto.Type) string {
	switch t {
	case dto.A:
		return s + v4Suffix
	case dto.AAAA:
		return s + v6Suffix
	default:
		return s + v4Suffix
	}
}

func computeData(iP net.IP, t dto.Type) net.IP {
	switch t {
	case dto.A:
		return iP.To4()
	case dto.AAAA:
		return iP.To16()
	default:
		return nil
	}
}

func gcScheduler(ctx context.Context, wg *sync.WaitGroup, memoryCache *MemoryCache, gcDelay time.Duration) {
	defer wg.Done()
	ticker := time.NewTicker(gcDelay)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			memoryCache.gc()
		}
	}
}
