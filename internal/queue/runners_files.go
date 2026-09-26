package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"server-control-panel/internal/files"
)

// MobileUploadStagingReapRunner removes mobile upload session directories
// abandoned in <DataDir>/.mobile-upload-staging/<id>/ — a chunked
// upload cancelled or never finalized by the client never had any
// cleanup, and the staging file (pre-allocated at the final size, up to
// maxUploadSize) stayed forever. Scheduled (e.g. hourly); cheap
// and idempotent when there is nothing to clean — the same pattern as
// DeployPreviewReapRunner.
type MobileUploadStagingReapRunner struct {
	DataDir string
}

func (MobileUploadStagingReapRunner) Kind() string                                { return "mobile_upload_staging_reap" }
func (MobileUploadStagingReapRunner) AuthorizedFor(_ string, isPrimary bool) bool { return isPrimary }

func (r MobileUploadStagingReapRunner) Run(ctx context.Context, _ json.RawMessage, logW io.Writer, progress func(int), step func(string)) error {
	step("sweeping abandoned mobile upload sessions")
	n, err := files.ReapStaleUploadSessions(r.DataDir, time.Now())
	if err != nil {
		// One stuck session must not invalidate the sweep of the others — the error
		// already arrives aggregated from ReapStaleUploadSessions, but we still report
		// how many were removed before propagating the failure.
		fmt.Fprintf(logW, "sessions removed before the failure: %d\n", n)
		return err
	}
	fmt.Fprintf(logW, "abandoned upload sessions removed: %d\n", n)
	progress(100)
	return nil
}
