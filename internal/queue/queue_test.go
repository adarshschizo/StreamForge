package queue

import "testing"

func TestJobValidation(t *testing.T) {
	if err := (Job{ID: "job", VideoID: "video", InputKey: "originals/video.mp4"}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (Job{ID: "job"}).Validate(); err != ErrInvalidJob {
		t.Fatalf("expected invalid job, got %v", err)
	}
}
