package transfer

import (
	"testing"
)

func TestTrackerCancel_AbortsRunningJob(t *testing.T) {
	tr := New(2)
	job := tr.Start("move", "download-move", 1, 100)

	if job.Canceled() {
		t.Fatal("fresh job should not be canceled")
	}
	if job.Context().Err() != nil {
		t.Fatal("fresh job context should be live")
	}

	if !tr.Cancel(job.ID(), 0, true) {
		t.Fatal("Cancel should find and cancel the running job")
	}
	if !job.Canceled() {
		t.Error("job should be canceled after Cancel")
	}
	if job.Context().Err() == nil {
		t.Error("job context should be canceled (so the producer aborts)")
	}
	if got := job.Snapshot().Status; got != StatusCanceled {
		t.Errorf("status = %q, want canceled", got)
	}
}

func TestTrackerCancel_UnknownID(t *testing.T) {
	tr := New(2)
	if tr.Cancel("nope", 0, true) {
		t.Error("Cancel of an unknown id should return false")
	}
}

func TestCanceledJob_IgnoresLateFinish(t *testing.T) {
	tr := New(2)
	job := tr.Start("move", "download-move", 1, 100)
	tr.Cancel(job.ID(), 0, true)
	// The producer notices the cancellation only after it returns and calls Fail
	// (or Done) — that must NOT resurrect the job out of the canceled state.
	job.Fail(nil)
	if got := job.Snapshot().Status; got != StatusCanceled {
		t.Errorf("late Fail() changed status to %q, want canceled", got)
	}
	job.Done()
	if got := job.Snapshot().Status; got != StatusCanceled {
		t.Errorf("late Done() changed status to %q, want canceled", got)
	}
}

func TestCancel_AlreadyDoneJob(t *testing.T) {
	tr := New(2)
	job := tr.Start("move", "download-move", 1, 100)
	job.Done()
	// Cancel finds the job (returns true) but doesn't flip a terminal job.
	if !tr.Cancel(job.ID(), 0, true) {
		t.Error("Cancel should still report the job as found")
	}
	if got := job.Snapshot().Status; got != StatusDone {
		t.Errorf("a done job must stay done, got %q", got)
	}
}
