package scancontrol

import (
	"context"
	"runtime"
	"time"
)

const (
	defaultWarmup         = 2 * time.Second
	defaultSampleInterval = 2 * time.Second
	defaultHeartbeat      = 30 * time.Second
	healthyQueueMax       = 0.70
	fullQueueMin          = 0.90
	throughputDropRatio   = 0.85
	slowFlush             = 2 * time.Second
	fastFlush             = 500 * time.Millisecond
	congestedSampleCount  = 2
	stalledSampleCount    = 5
	postDecreaseHold      = 2
	postBatchIncreaseHold = 2
)

type Snapshot struct {
	Active            bool
	Settings          Settings
	EnqueuedNodes     int64
	WrittenNodes      int64
	QueueOccupancy    float64
	LastFlushDuration time.Duration
	LastFlushAt       time.Time
	FlushCount        int64
	InUse             int
}

type Target interface {
	Snapshot() Snapshot
	Apply(Settings) Settings
}

type EventKind string

const (
	EventChange    EventKind = "change"
	EventHeartbeat EventKind = "heartbeat"
	EventStall     EventKind = "stall"
)

type Event struct {
	Kind              EventKind
	Reason            string
	Previous          Settings
	Current           Settings
	Snapshot          Snapshot
	WrittenPerSecond  float64
	EnqueuedPerSecond float64
	QueueDelta        float64
	NoProgressSamples int
}

type timings struct {
	warmup         time.Duration
	sampleInterval time.Duration
	heartbeat      time.Duration
}

type Controller struct {
	limits Limits
	timing timings

	initialized         bool
	previous            Snapshot
	previousWrittenRate float64
	lastConcurrencyMove string
	concurrencyHold     int
	batchHold           int
	fullQueueSamples    int
}

func New(profile Profile) *Controller {
	return newForCPU(profile, runtime.NumCPU())
}

func newForCPU(profile Profile, cpuCount int) *Controller {
	return &Controller{
		limits: profile.Limits(cpuCount),
		timing: timings{
			warmup:         defaultWarmup,
			sampleInterval: defaultSampleInterval,
			heartbeat:      defaultHeartbeat,
		},
	}
}

func (c *Controller) Limits() Limits {
	return c.limits
}

func (c *Controller) Run(ctx context.Context, target Target, notify func(Event)) {
	if !c.limits.Adaptive {
		return
	}

	warmup := time.NewTimer(c.timing.warmup)
	defer warmup.Stop()
	select {
	case <-warmup.C:
	case <-ctx.Done():
		return
	}

	initial := target.Snapshot()
	if !initial.Active {
		return
	}
	c.previous = initial
	c.initialized = true
	lastEventAt := time.Now()
	noProgressSamples := 0

	ticker := time.NewTicker(c.timing.sampleInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			current := target.Snapshot()
			if !current.Active {
				return
			}

			event, changed := c.next(current, c.timing.sampleInterval)
			if event.WrittenPerSecond == 0 {
				noProgressSamples++
			} else {
				noProgressSamples = 0
			}
			event.NoProgressSamples = noProgressSamples

			if changed {
				event.Current = target.Apply(event.Current)
				if notify != nil {
					notify(event)
				}
				lastEventAt = time.Now()
				continue
			}

			now := time.Now()
			if noProgressSamples == stalledSampleCount && current.QueueOccupancy >= fullQueueMin {
				event.Kind = EventStall
				event.Reason = "writer queue stalled"
				if notify != nil {
					notify(event)
				}
				lastEventAt = now
				continue
			}
			if now.Sub(lastEventAt) >= c.timing.heartbeat {
				event.Kind = EventHeartbeat
				event.Reason = "steady"
				if notify != nil {
					notify(event)
				}
				lastEventAt = now
			}
		case <-ctx.Done():
			return
		}
	}
}

func (c *Controller) next(current Snapshot, interval time.Duration) (Event, bool) {
	previousSettings := current.Settings
	nextSettings := previousSettings

	enqueuedDelta := nonNegativeDelta(current.EnqueuedNodes, c.previous.EnqueuedNodes)
	writtenDelta := nonNegativeDelta(current.WrittenNodes, c.previous.WrittenNodes)
	enqueuedRate := float64(enqueuedDelta) / interval.Seconds()
	writtenRate := float64(writtenDelta) / interval.Seconds()
	queueDelta := current.QueueOccupancy - c.previous.QueueOccupancy
	hadFlush := current.FlushCount > c.previous.FlushCount

	if current.QueueOccupancy >= fullQueueMin {
		c.fullQueueSamples++
	} else {
		c.fullQueueSamples = 0
	}

	reasons := make([]string, 0, 2)
	throughputDroppedAfterIncrease := c.lastConcurrencyMove == "increase" &&
		c.previousWrittenRate > 0 &&
		writtenRate < c.previousWrittenRate*throughputDropRatio &&
		current.QueueOccupancy >= healthyQueueMax
	flushIsSlow := hadFlush && current.LastFlushDuration >= slowFlush
	congested := c.fullQueueSamples >= congestedSampleCount || throughputDroppedAfterIncrease || flushIsSlow

	if congested {
		decreased := clamp((previousSettings.Concurrency+1)/2, c.limits.Min.Concurrency, c.limits.Max.Concurrency)
		if decreased < previousSettings.Concurrency {
			nextSettings.Concurrency = decreased
			c.lastConcurrencyMove = "decrease"
			c.concurrencyHold = postDecreaseHold
			reasons = append(reasons, "multiplicative concurrency decrease")
		}
	} else if c.concurrencyHold > 0 {
		c.concurrencyHold--
		c.lastConcurrencyMove = "hold"
	} else {
		throughputHealthy := writtenRate > 0 && (c.previousWrittenRate == 0 || writtenRate >= c.previousWrittenRate*throughputDropRatio)
		if current.QueueOccupancy < healthyQueueMax && throughputHealthy && previousSettings.Concurrency < c.limits.Max.Concurrency {
			nextSettings.Concurrency = clamp(previousSettings.Concurrency+1, c.limits.Min.Concurrency, c.limits.Max.Concurrency)
			c.lastConcurrencyMove = "increase"
			reasons = append(reasons, "additive concurrency increase")
		} else {
			c.lastConcurrencyMove = "hold"
		}
	}

	if flushIsSlow && previousSettings.BatchSize > c.limits.Min.BatchSize {
		factor := 2
		if current.LastFlushDuration >= 10*time.Second {
			factor = 4
		}
		nextSettings.BatchSize = clamp(previousSettings.BatchSize/factor, c.limits.Min.BatchSize, c.limits.Max.BatchSize)
		c.batchHold = postDecreaseHold
		reasons = append(reasons, "multiplicative batch decrease")
	} else if c.batchHold > 0 {
		c.batchHold--
	} else {
		writerPressure := enqueuedRate > writtenRate*1.10 || queueDelta > 0.01
		if hadFlush && c.fullQueueSamples >= congestedSampleCount && writerPressure && current.LastFlushDuration < fastFlush && previousSettings.BatchSize < c.limits.Max.BatchSize {
			step := previousSettings.BatchSize / 4
			if step < 256 {
				step = 256
			}
			nextSettings.BatchSize = clamp(previousSettings.BatchSize+step, c.limits.Min.BatchSize, c.limits.Max.BatchSize)
			c.batchHold = postBatchIncreaseHold
			reasons = append(reasons, "additive batch increase")
		}
	}

	c.previous = current
	c.previousWrittenRate = writtenRate

	event := Event{
		Kind:              EventChange,
		Reason:            joinReasons(reasons),
		Previous:          previousSettings,
		Current:           nextSettings,
		Snapshot:          current,
		WrittenPerSecond:  writtenRate,
		EnqueuedPerSecond: enqueuedRate,
		QueueDelta:        queueDelta,
	}
	return event, nextSettings != previousSettings
}

func nonNegativeDelta(current, previous int64) int64 {
	delta := current - previous
	if delta < 0 {
		return 0
	}
	return delta
}

func joinReasons(reasons []string) string {
	if len(reasons) == 0 {
		return "hold"
	}
	joined := reasons[0]
	for _, reason := range reasons[1:] {
		joined += ", " + reason
	}
	return joined
}
