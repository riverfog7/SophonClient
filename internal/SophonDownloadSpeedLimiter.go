package internal

import (
	"sync"
	"sync/atomic"
)

// SpeedChangedHandler is a callback function for speed change events
type SpeedChangedHandler func(sender interface{}, newRequestedSpeed int64)

// ChunkProcessingChangedHandler is a callback function for chunk processing count changes
type ChunkProcessingChangedHandler func(sender interface{}, currentChunkProcessing int)

// SophonDownloadSpeedLimiter manages download speed limits and tracks chunk processing
type SophonDownloadSpeedLimiter struct {
	// Event handlers
	CurrentChunkProcessingChangedEvent ChunkProcessingChangedHandler
	DownloadSpeedChangedEvent          SpeedChangedHandler

	// State
	InitialRequestedSpeed  *int64
	innerListener          SpeedChangedHandler
	currentChunkProcessing int32

	// Mutex for event handler operations
	mu sync.RWMutex
}

// CreateInstance creates a new SophonDownloadSpeedLimiter with an initial speed
func CreateInstance(initialSpeed int64) *SophonDownloadSpeedLimiter {
	return &SophonDownloadSpeedLimiter{
		InitialRequestedSpeed: &initialSpeed,
	}
}

// GetListener returns the event handler for speed changes
func (s *SophonDownloadSpeedLimiter) GetListener() SpeedChangedHandler {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.innerListener == nil {
		s.innerListener = s.DownloadSpeedChangeListener
	}

	return s.innerListener
}

// DownloadSpeedChangeListener handles speed change events
func (s *SophonDownloadSpeedLimiter) DownloadSpeedChangeListener(sender interface{}, newRequestedSpeed int64) {
	s.mu.RLock()
	handler := s.DownloadSpeedChangedEvent
	s.mu.RUnlock()

	if handler != nil {
		handler(s, newRequestedSpeed)
	}

	// Update the initial speed
	atomic.StoreInt64(s.InitialRequestedSpeed, newRequestedSpeed)
}

// IncrementChunkProcessedCount increases the chunk processing count
func (s *SophonDownloadSpeedLimiter) IncrementChunkProcessedCount() {
	newCount := atomic.AddInt32(&s.currentChunkProcessing, 1)

	s.mu.RLock()
	handler := s.CurrentChunkProcessingChangedEvent
	s.mu.RUnlock()

	if handler != nil {
		handler(s, int(newCount))
	}
}

// DecrementChunkProcessedCount decreases the chunk processing count
func (s *SophonDownloadSpeedLimiter) DecrementChunkProcessedCount() {
	newCount := atomic.AddInt32(&s.currentChunkProcessing, -1)

	s.mu.RLock()
	handler := s.CurrentChunkProcessingChangedEvent
	s.mu.RUnlock()

	if handler != nil {
		handler(s, int(newCount))
	}
}

// GetCurrentChunkProcessing returns the current number of chunks being processed
func (s *SophonDownloadSpeedLimiter) GetCurrentChunkProcessing() int {
	return int(atomic.LoadInt32(&s.currentChunkProcessing))
}
