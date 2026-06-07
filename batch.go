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
	mutex      sync.Mutex
	b_size     int64
	e_size     atomic.Int64
	runnedOnce bool
}

func crt_mbatch() *MapBatch {
	return &MapBatch{elements: map[string]any{}}
}

func crt_qbatch() *QueueBatch {
	return &QueueBatch{queue: list.New()}
}

func crt_batch(b_size int64) *Batch {
	return &Batch{b_size: b_size, runnedOnce: false}
}

func (batch *Batch) add_elmnt(queueBatch *QueueBatch, mapBatch *MapBatch, element Element, timeout int, callback func()) {
	queueBatch.mutex.Lock()
	if batch.e_size.Load() == batch.b_size {
		// move this batch to fulled ones
		// we use a batch thats not stuffed
		queueBatch.queue.PushBack(element)
		return
	}

	for batch.e_size.Load() != batch.b_size && queueBatch.queue.Len() != 0 {
		front := queueBatch.queue.Front()
		mapBatch.mutex.Lock()
		mapBatch.elements[front.Value.(Element).id] = front.Value.(Element).element
		mapBatch.mutex.Unlock()
		batch.e_size.Add(1)
		queueBatch.queue.Remove(front)
	}

	capturedAtom := batch.e_size.Load()
	queueBatch.mutex.Unlock()

	mapBatch.mutex.Lock()
	mapBatch.elements[element.id] = element.element
	mapBatch.mutex.Unlock()

	batch.e_size.Add(1)

	ticker := time.NewTicker(10 * time.Millisecond)
	timeouta := time.NewTicker(time.Duration(timeout) * time.Millisecond)

	ready := make(chan any, 1)

	go func() {
		for range ticker.C {
			if capturedAtom != batch.e_size.Load() {
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
	if !batch.runnedOnce {
		callback()
		batch.runnedOnce = true
	}
	batch.mutex.Unlock()

}
