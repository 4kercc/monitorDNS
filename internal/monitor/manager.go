package monitor

import (
	"context"
	"sync"
	"time"

	"monitorDNS/internal/store"
)

type Manager struct {
	store *store.Store

	mu      sync.Mutex
	runners map[int64]*runner
}

type runner struct {
	domain store.Domain
	cancel context.CancelFunc
}

func NewManager(st *store.Store) *Manager {
	return &Manager{
		store:   st,
		runners: map[int64]*runner{},
	}
}

func (m *Manager) Start(ctx context.Context) {
	// Initial sync and then periodic sync to pick up new config.
	m.sync(ctx)

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			m.stopAll()
			return
		case <-ticker.C:
			m.sync(ctx)
		}
	}
}

func (m *Manager) sync(ctx context.Context) {
	domains, err := m.store.ListDomains(ctx)
	if err != nil {
		return
	}

	enabled := map[int64]store.Domain{}
	for _, d := range domains {
		if d.Enabled {
			enabled[d.ID] = d
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Stop removed/disabled runners.
	for id, r := range m.runners {
		if _, ok := enabled[id]; !ok {
			r.cancel()
			delete(m.runners, id)
		}
	}

	// Start or reconfigure enabled runners.
	for id, d := range enabled {
		existing, ok := m.runners[id]
		if ok && sameConfig(existing.domain, d) {
			continue
		}
		if ok {
			existing.cancel()
			delete(m.runners, id)
		}

		rctx, cancel := context.WithCancel(ctx)
		m.runners[id] = &runner{domain: d, cancel: cancel}
		go m.runDomain(rctx, d)
	}
}

func sameConfig(a, b store.Domain) bool {
	return a.Domain == b.Domain &&
		a.RecordType == b.RecordType &&
		a.IntervalSeconds == b.IntervalSeconds &&
		a.Enabled == b.Enabled
}

func (m *Manager) stopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, r := range m.runners {
		r.cancel()
		delete(m.runners, id)
	}
}

func (m *Manager) runDomain(ctx context.Context, d store.Domain) {
	interval := time.Duration(d.IntervalSeconds) * time.Second
	if interval <= 0 {
		interval = 30 * time.Second
	}

	// Run immediately once so the UI has initial data quickly.
	m.runOnce(ctx, d)

	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.runOnce(ctx, d)
		}
	}
}

func (m *Manager) runOnce(ctx context.Context, d store.Domain) {
	checkedAt := time.Now()
	value, errStr := Lookup(d.Domain, d.RecordType)
	_, _, _, _ = m.store.InsertCheck(ctx, d.ID, checkedAt, value, errStr)
}

