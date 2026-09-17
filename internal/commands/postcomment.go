package commands

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/infracost/cli/pkg/logging"
	"github.com/infracost/vcs/pkg/vcs"
)

// retryPolicy bounds how long a rate-limited post may keep trying.
type retryPolicy struct {
	attempts   int           // total attempts, including the first
	baseDelay  time.Duration // doubled per attempt when there is no Retry-After
	waitBudget time.Duration // longest total wait worth honouring
}

// minWait floors every wait, so a policy with no baseDelay cannot fire its
// attempts back to back.
const minWait = 100 * time.Millisecond

// defaultRetryPolicy honours GitHub's secondary rate limit, which asks for
// around 60s. A primary limit resets on the hour and is not worth waiting for.
var defaultRetryPolicy = retryPolicy{
	attempts:   4,
	baseDelay:  2 * time.Second,
	waitBudget: 90 * time.Second,
}

// postComment posts body, retrying transient failures within p, and returns the
// total time it spent waiting. The behavior is fixed: only BehaviorUpdate
// converges on a retry, finding the comment a lost first response may have made.
func postComment(ctx context.Context, client vcs.VCS, body string, p retryPolicy) (vcs.PostResult, time.Duration, error) {
	var waited time.Duration

	for attempt := 1; ; attempt++ {
		result, err := client.PostComment(ctx, body, vcs.BehaviorUpdate)
		if err == nil {
			return result, waited, nil
		}

		var postErr *vcs.PostError
		if !errors.As(err, &postErr) || !postErr.Retryable {
			return result, waited, err
		}

		// The budget covers the whole run, not one wait, so a server repeating the
		// same Retry-After cannot idle the job for a multiple of it.
		if attempt >= p.attempts || waited+postErr.RetryAfter > p.waitBudget {
			return result, waited, giveUp(attempt, postErr, err)
		}

		wait := nextWait(attempt, postErr.RetryAfter, p)
		logging.Warnf("failed to post comment (attempt %d of %d), retrying in %s: %s", attempt, p.attempts, wait.Round(time.Millisecond), err)
		if err := sleep(ctx, wait); err != nil {
			return result, waited, err
		}
		waited += wait
	}
}

// nextWait honours the server's Retry-After, else backs off exponentially. Both
// are jittered upward by up to 20%: a PR's matrix jobs share a rate limit, so
// they are handed the same Retry-After and would otherwise return in lockstep.
// Upward only — coming back early just earns another 429. Callers compare the
// unjittered Retry-After against the budget, so jitter never decides a give-up.
func nextWait(attempt int, retryAfter time.Duration, p retryPolicy) time.Duration {
	wait := retryAfter
	if wait <= 0 {
		wait = p.baseDelay << (attempt - 1)
	}

	wait += time.Duration(rand.Float64() * 0.2 * float64(wait)) //nolint:gosec // jitter, not a security decision
	if wait < minWait {
		return minWait
	}
	return wait
}

// giveUp names the wait the server asked for, so a job log says whether the run
// is worth repeating now or later.
func giveUp(attempts int, postErr *vcs.PostError, err error) error {
	plural := "attempts"
	if attempts == 1 {
		plural = "attempt"
	}

	if postErr.RetryAfter > 0 {
		return fmt.Errorf("rate limited: server asked for %s, giving up after %d %s: %w", postErr.RetryAfter, attempts, plural, err)
	}
	return fmt.Errorf("giving up after %d %s: %w", attempts, plural, err)
}

// sleep waits for d unless ctx is cancelled first.
func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
