package scancontrol

import (
	"fmt"
	"strings"
)

type Profile string

const (
	ProfileBalanced   Profile = "balanced"
	ProfileThroughput Profile = "throughput"
	ProfileLowImpact  Profile = "low-impact"
	ProfileFixed      Profile = "fixed"
)

type Settings struct {
	Concurrency int
	BatchSize   int
}

type Limits struct {
	Adaptive bool
	Initial  Settings
	Min      Settings
	Max      Settings
}

func ParseProfile(value string) (Profile, error) {
	profile := Profile(strings.ToLower(strings.TrimSpace(value)))
	switch profile {
	case ProfileBalanced, ProfileThroughput, ProfileLowImpact, ProfileFixed:
		return profile, nil
	default:
		return "", fmt.Errorf("invalid SCAN_PROFILE %q: expected balanced, throughput, low-impact, or fixed", value)
	}
}

func (p Profile) Limits(cpuCount int) Limits {
	if cpuCount < 1 {
		cpuCount = 1
	}

	switch p {
	case ProfileThroughput:
		maxConcurrency := clamp(cpuCount*8, 4, 128)
		return Limits{
			Adaptive: true,
			Initial:  Settings{Concurrency: clamp(cpuCount*2, 4, maxConcurrency), BatchSize: 4096},
			Min:      Settings{Concurrency: 2, BatchSize: 1024},
			Max:      Settings{Concurrency: maxConcurrency, BatchSize: 16384},
		}
	case ProfileLowImpact:
		maxConcurrency := clamp(cpuCount, 2, 16)
		return Limits{
			Adaptive: true,
			Initial:  Settings{Concurrency: clamp(cpuCount/2, 1, maxConcurrency), BatchSize: 512},
			Min:      Settings{Concurrency: 1, BatchSize: 256},
			Max:      Settings{Concurrency: maxConcurrency, BatchSize: 2048},
		}
	case ProfileFixed:
		concurrency := clamp(cpuCount, 1, 32)
		return Limits{
			Adaptive: false,
			Initial:  Settings{Concurrency: concurrency, BatchSize: 2048},
			Min:      Settings{Concurrency: concurrency, BatchSize: 2048},
			Max:      Settings{Concurrency: concurrency, BatchSize: 2048},
		}
	case ProfileBalanced:
		fallthrough
	default:
		maxConcurrency := clamp(cpuCount*4, 4, 64)
		return Limits{
			Adaptive: true,
			Initial:  Settings{Concurrency: clamp(cpuCount, 4, maxConcurrency), BatchSize: 2048},
			Min:      Settings{Concurrency: 1, BatchSize: 512},
			Max:      Settings{Concurrency: maxConcurrency, BatchSize: 8192},
		}
	}
}

func clamp(value, minValue, maxValue int) int {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}
