package commands

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"time"

	"github.com/infracost/ci/v2/internal/vcsurl"
	"github.com/infracost/cli/pkg/logging"
	"github.com/infracost/vcs/pkg/vcs"
)

// retryPolicy bounds how long a rate-limited post may keep trying.
type retryPolicy struct {
	attempts   int           // total attempts, including the first
	baseDelay  time.Duration // doubled per attempt when there is no Retry-After
	waitBudget time.Duration // longest Retry-After worth honouring, compared before jitter
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

// The comment is already on the pull request by this point, so the retry is
// short: it covers a restart or a failover, not a sustained outage.
var savePostedCommentRetryDelay = 2 * time.Second

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

// postHints are the two things a failed post can be: a token that was never
// usable, and one whose principal lacks the permission to comment. The
// permission is named per provider, because nothing about it carries over.
type postHints struct {
	token      string
	permission string
}

// providerPostHints covers every provider but Azure Repos, whose 403 splits
// further on the principal behind the token and has azurePostHint to itself.
var providerPostHints = map[string]postHints{
	vcsurl.ProviderGitHub:    {token: gitHubTokenHint, permission: gitHubPermissionHint},
	vcsurl.ProviderGitLab:    {token: gitLabTokenHint, permission: gitLabPermissionHint},
	vcsurl.ProviderBitbucket: {token: bitbucketTokenHint, permission: bitbucketPermissionHint},
}

// notFoundHint fronts the permission hint on a 404, where the pull request
// being gone and the token not being allowed to see it look the same.
const notFoundHint = "the pull request was not found: check the repository URL and pull request number first. " +
	"If both are right, this is a permission problem — "

// postHint names what to fix when a comment post failed on the token or the
// permissions behind it — a run that scans, prices and then posts nothing is
// otherwise indistinguishable from one that is simply broken.
//
// Returns "" when there is nothing specific to say.
func postHint(provider string, args *diffArgs, err error) string {
	if provider == vcsurl.ProviderAzureRepos {
		return azurePostHint(provider, args.azureToken, err)
	}

	hints, ok := providerPostHints[provider]
	if !ok {
		return ""
	}

	var postErr *vcs.PostError
	if !errors.As(err, &postErr) {
		return ""
	}

	switch postErr.StatusCode {
	case http.StatusUnauthorized:
		return hints.token
	case http.StatusForbidden:
		return hints.permission
	// All three providers answer "no such repository" to a token not allowed
	// to see one, so a 404 is either that or a pull request that is not there.
	// Naming both keeps a wrong number from being read as a token scope.
	case http.StatusNotFound:
		return notFoundHint + hints.permission
	}
	return ""
}
