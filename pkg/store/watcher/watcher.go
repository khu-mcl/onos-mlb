// SPDX-FileCopyrightText: 2020-present Open Networking Foundation <info@opennetworking.org>
//
// SPDX-License-Identifier: LicenseRef-ONF-Member-1.0

package watcher

import (
	"github.com/google/uuid"
	"github.com/onosproject/onos-mlb/pkg/store/event"
	"sync"
)

type EventChannel chan event.Event

type Watchers struct {
	watchers map[uuid.UUID]Watcher
	rm sync.RWMutex
}

type Watcher struct {
	id uuid.UUID
	ch chan<- event.Event
}

func NewWatchers() *Watchers {
	return &Watchers{
		watchers: make(map[uuid.UUID]Watcher),
	}
}

// Send sends an event for all registered watchers
func (ws *Watchers) Send(event event.Event) {
	ws.rm.RLock()
	go func() {
		for _, watcher := range ws.watchers {
			watcher.ch <- event
		}
	}()
	ws.rm.RUnlock()
}

// AddWatcher adds a watcher
func (ws *Watchers) AddWatcher(id uuid.UUID, ch chan<- event.Event) error {
	ws.rm.Lock()
	watcher := Watcher{
		id: id,
		ch: ch,
	}
	ws.watchers[id] = watcher
	ws.rm.Unlock()
	return nil

}

// RemoveWatcher removes a watcher
func (ws *Watchers) RemoveWatcher(id uuid.UUID) error {
	ws.rm.Lock()
	watchers := make(map[uuid.UUID]Watcher, len(ws.watchers)-1)
	for _, watcher := range ws.watchers {
		if watcher.id != id {
			watchers[id] = watcher

		}
	}
	ws.watchers = watchers
	ws.rm.Unlock()
	return nil

}
