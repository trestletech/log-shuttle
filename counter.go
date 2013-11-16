package main

import (
	"sync/atomic"
	"time"
)

type Counter struct {
	value         uint64
	LastIncrement time.Time
}

func (c *Counter) Read() uint64 {
	return c.value
}

func (c *Counter) ReadAndReset() uint64 {
	for {
		oldCount := c.value
		if atomic.CompareAndSwapUint64(&c.value, oldCount, 0) {
			return oldCount
		}
	}
}

func (c *Counter) Add(u uint64) uint64 {
	c.LastIncrement = time.Now()
	return atomic.AddUint64(&c.value, u)
}
