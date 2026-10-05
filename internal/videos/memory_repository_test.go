package videos

import "testing"

func TestMemoryRepositoryEnforcesOwnership(t *testing.T) {
	repository := NewMemoryRepository()
	video, err := repository.Create("owner", CreateInput{Title: "Demo", Visibility: VisibilityPrivate})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.GetForUser("other", video.ID); err != ErrNotOwner {
		t.Fatalf("expected ownership error, got %v", err)
	}
	if err := repository.DeleteForUser("other", video.ID); err != ErrNotOwner {
		t.Fatalf("expected ownership error, got %v", err)
	}
}

func TestMemoryRepositoryDefaultsPrivate(t *testing.T) {
	video, err := NewMemoryRepository().Create("owner", CreateInput{Title: "Demo"})
	if err != nil {
		t.Fatal(err)
	}
	if video.Visibility != VisibilityPrivate || video.Status != StatusUploading {
		t.Fatalf("unexpected defaults: %+v", video)
	}
}
