package main

import (
	"container/list"
	"sync"
	"sync/atomic"
	"time"
)

type QueueBatch struct {
	mutex sync.Mutex
	queue *list.List
}

type Element struct {
	id      string
	element any
}

type MapBatch struct {
	mutex    sync.Mutex
	elements map[string]any
}

type Batch struct {
	mutex  sync.Mutex
	once   sync.Once
	e_size atomic.Int64
}

func crt_mbatch() *MapBatch {
	return &MapBatch{elements: map[string]any{}}
}

func crt_batch() *Batch {
	return &Batch{}
}

/*
we send requests with a batch and checking if theres more request then we push, if we had more than 30 batchs and there was more requests in we put those requests in workin batches, so batches can grow or get smaller or completly destroyed
*/

func (batch *Batch) add_elmnt(mapBatch *MapBatch, element Element, timeout int, callback func()) {

	mapBatch.mutex.Lock()
	capturedLen := len(mapBatch.elements)
	mapBatch.elements[element.id] = element.element
	mapBatch.mutex.Unlock()

	batch.e_size.Add(1)

	ticker := time.NewTicker(10 * time.Millisecond)
	timeouta := time.NewTicker(time.Duration(timeout) * time.Millisecond)

	ready := make(chan any, 1)

	go func() {
		for range ticker.C {
			if int64(capturedLen) != batch.e_size.Load() {
				timeouta.Reset(time.Duration(timeout) * time.Millisecond)
			}
		}
	}()

	go func() {
		<-timeouta.C
		timeouta.Stop()
		ticker.Stop()
		close(ready)
	}()

	<-ready
	batch.mutex.Lock()
	go batch.once.Do(callback)
	batch.mutex.Unlock()

}
