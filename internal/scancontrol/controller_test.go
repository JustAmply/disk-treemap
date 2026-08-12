package scancontrol

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestProfilesResolveToValidSettings(t *testing.T) {
	for _, profile := range []Profile{ProfileBalanced, ProfileThroughput, ProfileLowImpact, ProfileFixed} {
		limits := profile.Limits(8)
		if limits.Initial.Concurrency < limits.Min.Concurrency || limits.Initial.Concurrency > limits.Max.Concurrency {
			t.Fatalf("%s concurrency outside limits: %+v", profile, limits)
		}
		if limits.Initial.BatchSize < limits.Min.BatchSize || limits.Initial.BatchSize > limits.Max.BatchSize {
			t.Fatalf("%s batch size outside limits: %+v", profile, limits)
		}
	}
	if ProfileFixed.Limits(8).Adaptive {
		t.Fatalf("fixed profile must not adapt")
	}
}

func TestParseProfileRejectsUnknownValue(t *testing.T) {
	if _, err := ParseProfile("turbo"); err == nil {
		t.Fatalf("expected invalid profile error")
	}
}

func TestControllerRunAdditivelyIncreasesHealthyConcurrency(t *testing.T) {
	target := newSequenceTarget([]Snapshot{
		{Active: true, Settings: Settings{Concurrency: 4, BatchSize: 2048}, WrittenNodes: 100, EnqueuedNodes: 100, QueueOccupancy: 0.2},
		{Active: true, Settings: Settings{Concurrency: 4, BatchSize: 2048}, WrittenNodes: 200, EnqueuedNodes: 200, QueueOccupancy: 0.2},
	})
	event := runUntilEvent(t, ProfileBalanced, target)
	if event.Current.Concurrency != 5 || event.Reason != "additive concurrency increase" {
		t.Fatalf("unexpected event: %+v", event)
	}
}

func TestControllerRunHalvesConcurrencyAfterSustainedCongestion(t *testing.T) {
	target := newSequenceTarget([]Snapshot{
		{Active: true, Settings: Settings{Concurrency: 8, BatchSize: 2048}, WrittenNodes: 100, EnqueuedNodes: 100, QueueOccupancy: 0.9},
		{Active: true, Settings: Settings{Concurrency: 8, BatchSize: 2048}, WrittenNodes: 200, EnqueuedNodes: 240, QueueOccupancy: 0.95},
		{Active: true, Settings: Settings{Concurrency: 8, BatchSize: 2048}, WrittenNodes: 300, EnqueuedNodes: 380, QueueOccupancy: 0.98},
	})
	event := runUntilEvent(t, ProfileBalanced, target)
	if event.Current.Concurrency != 4 {
		t.Fatalf("expected multiplicative decrease to 4, got %+v", event)
	}
}

func TestControllerRunReducesBatchAfterSlowFlush(t *testing.T) {
	target := newSequenceTarget([]Snapshot{
		{Active: true, Settings: Settings{Concurrency: 4, BatchSize: 4096}, WrittenNodes: 100, EnqueuedNodes: 100, FlushCount: 1, QueueOccupancy: 0.4},
		{Active: true, Settings: Settings{Concurrency: 4, BatchSize: 4096}, WrittenNodes: 200, EnqueuedNodes: 200, FlushCount: 2, LastFlushDuration: 3 * time.Second, QueueOccupancy: 0.4},
	})
	event := runUntilEvent(t, ProfileBalanced, target)
	if event.Current.BatchSize != 2048 {
		t.Fatalf("expected batch decrease to 2048, got %+v", event)
	}
}

func runUntilEvent(t *testing.T, profile Profile, target *sequenceTarget) Event {
	t.Helper()
	controller := newForCPU(profile, 8)
	controller.timing = timings{warmup: time.Millisecond, sampleInterval: time.Millisecond, heartbeat: time.Hour}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	events := make(chan Event, 1)
	go controller.Run(ctx, target, func(event Event) {
		if event.Kind == EventChange {
			select {
			case events <- event:
			default:
			}
		}
	})

	select {
	case event := <-events:
		return event
	case <-ctx.Done():
		t.Fatalf("controller did not emit a change")
		return Event{}
	}
}

type sequenceTarget struct {
	mu        sync.Mutex
	snapshots []Snapshot
	index     int
}

func newSequenceTarget(snapshots []Snapshot) *sequenceTarget {
	return &sequenceTarget{snapshots: snapshots}
}

func (t *sequenceTarget) Snapshot() Snapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.index >= len(t.snapshots) {
		return Snapshot{Active: false}
	}
	snapshot := t.snapshots[t.index]
	t.index++
	return snapshot
}

func (t *sequenceTarget) Apply(settings Settings) Settings {
	return settings
}
