package likes

import "sync"

type synchronizedMemoryRepository struct {
	mu    sync.RWMutex
	likes map[string]map[string]struct{}
}

func NewMemoryRepositoryWithMutex() Repository {
	return &synchronizedMemoryRepository{likes: make(map[string]map[string]struct{})}
}

func (r *synchronizedMemoryRepository) Toggle(userID, videoID string) (Summary, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.likes[videoID] == nil {
		r.likes[videoID] = make(map[string]struct{})
	}
	if _, ok := r.likes[videoID][userID]; ok {
		delete(r.likes[videoID], userID)
	} else {
		r.likes[videoID][userID] = struct{}{}
	}
	return Summary{Liked: r.contains(videoID, userID), Count: len(r.likes[videoID])}, nil
}

func (r *synchronizedMemoryRepository) Get(userID, videoID string) (Summary, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return Summary{Liked: r.contains(videoID, userID), Count: len(r.likes[videoID])}, nil
}

func (r *synchronizedMemoryRepository) contains(videoID, userID string) bool {
	_, ok := r.likes[videoID][userID]
	return ok
}
