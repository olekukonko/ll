package lm

import (
	"fmt"
	"github.com/olekukonko/ll/lx"
	"math/rand"
	"sync"
)

type Sampling struct {
	rates map[lx.LevelType]float64
	stats map[lx.LevelType]int
	mu    sync.Mutex
}

func NewSampling(level lx.LevelType, rate float64) *Sampling {
	s := &Sampling{
		rates: make(map[lx.LevelType]float64),
		stats: make(map[lx.LevelType]int),
	}
	s.Set(level, rate)
	return s
}

func (s *Sampling) Set(level lx.LevelType, rate float64) *Sampling {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rates[level] = rate
	return s
}

func (s *Sampling) Handle(e *lx.Entry) error {
	rate, exists := s.rates[e.Level]
	if !exists {
		// fmt.Printf("Sampling: No rate for level %v\n", e.Level)
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	random := rand.Float64()
	if random <= rate {
		// fmt.Printf("Sampling: rate=%v, random=%v, allowing log\n", rate, random)
		return nil
	}
	s.stats[e.Level]++
	// fmt.Printf("Sampling: rate=%v, random=%v, rejecting log\n", rate, random)
	return fmt.Errorf("sampling error")
}

func (s *Sampling) GetStats() map[lx.LevelType]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make(map[lx.LevelType]int)
	for k, v := range s.stats {
		result[k] = v
	}
	return result
}
