package commands

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	vcsmocks "github.com/infracost/ci/internal/mocks/vcs"
	"github.com/infracost/vcs/pkg/vcs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// testPolicy sits just above the minWait floor, so the tests see a real backoff
// without the suite paying for one.
var testPolicy = retryPolicy{attempts: 3, baseDelay: 150 * time.Millisecond, waitBudget: 90 * time.Second}

func retryAfterError(seconds string) error {
	return vcs.HTTPPostError(http.StatusTooManyRequests, http.Header{"Retry-After": []string{seconds}}, errors.New("429 Too Many Requests"))
}

func expectPost(m *vcsmocks.MockVCS) *vcsmocks.MockVCS_PostComment_Call {
	return m.EXPECT().PostComment(mock.Anything, "body", vcs.BehaviorUpdate)
}

// Retry-After has second granularity, so the honoured-wait path is covered by
// nextWait and by the over-budget case, not by a loop test that would sleep.
func TestPostComment_RetriesRateLimit(t *testing.T) {
	m := vcsmocks.NewMockVCS(t)
	expectPost(m).Return(vcs.PostResult{}, retryAfterError("0")).Once()
	expectPost(m).Return(vcs.PostResult{Posted: true}, nil).Once()

	result, waited, err := postComment(context.Background(), m, "body", testPolicy)

	require.NoError(t, err)
	assert.True(t, result.Posted)
	assert.GreaterOrEqual(t, waited, testPolicy.baseDelay)
	assert.Less(t, waited, time.Duration(float64(testPolicy.baseDelay)*1.2))
	m.AssertNumberOfCalls(t, "PostComment", 2)
}

func TestPostComment_NonRetryableIsUnchanged(t *testing.T) {
	m := vcsmocks.NewMockVCS(t)
	forbidden := vcs.HTTPPostError(http.StatusForbidden, nil, errors.New("403 Forbidden"))
	expectPost(m).Return(vcs.PostResult{}, forbidden).Once()

	_, _, err := postComment(context.Background(), m, "body", testPolicy)

	assert.Same(t, forbidden, err)
	m.AssertNumberOfCalls(t, "PostComment", 1)
}

func TestPostComment_AttemptsExhausted(t *testing.T) {
	m := vcsmocks.NewMockVCS(t)
	reset := vcs.RetryablePostError(errors.New("connection reset"))
	expectPost(m).Return(vcs.PostResult{}, reset)

	_, _, err := postComment(context.Background(), m, "body", testPolicy)

	require.ErrorIs(t, err, reset)
	assert.Contains(t, err.Error(), "giving up after 3 attempts")
	m.AssertNumberOfCalls(t, "PostComment", testPolicy.attempts)
}

// A Retry-After longer than the budget is a give-up, not a long sleep: the run
// is not worth an idle runner, and the error says how long the server wanted.
func TestPostComment_RetryAfterExceedsBudget(t *testing.T) {
	m := vcsmocks.NewMockVCS(t)
	expectPost(m).Return(vcs.PostResult{}, retryAfterError("3600")).Once()

	start := time.Now()
	_, waited, err := postComment(context.Background(), m, "body", testPolicy)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "server asked for 1h0m0s")
	assert.Contains(t, err.Error(), "giving up after 1 attempt:")
	assert.Zero(t, waited)
	assert.Less(t, time.Since(start), time.Second)
	m.AssertNumberOfCalls(t, "PostComment", 1)
}

// Jitter is added after the budget comparison, so a Retry-After exactly at the
// budget is still honoured rather than a coin toss.
func TestPostComment_RetryAfterAtBudgetIsHonoured(t *testing.T) {
	m := vcsmocks.NewMockVCS(t)
	atBudget := &vcs.PostError{Retryable: true, RetryAfter: 200 * time.Millisecond, Err: errors.New("429 Too Many Requests")}
	expectPost(m).Return(vcs.PostResult{}, atBudget).Once()
	expectPost(m).Return(vcs.PostResult{Posted: true}, nil).Once()

	_, waited, err := postComment(context.Background(), m, "body", retryPolicy{attempts: 2, waitBudget: 200 * time.Millisecond})

	require.NoError(t, err)
	assert.GreaterOrEqual(t, waited, 200*time.Millisecond)
	m.AssertNumberOfCalls(t, "PostComment", 2)
}

// The budget is a total, so a repeated Retry-After under it still ends the run.
func TestPostComment_RetryAfterExhaustsBudgetAcrossAttempts(t *testing.T) {
	m := vcsmocks.NewMockVCS(t)
	underBudget := &vcs.PostError{Retryable: true, RetryAfter: 200 * time.Millisecond, Err: errors.New("429 Too Many Requests")}
	expectPost(m).Return(vcs.PostResult{}, underBudget)

	_, waited, err := postComment(context.Background(), m, "body", retryPolicy{attempts: 4, waitBudget: 300 * time.Millisecond})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "giving up after 2 attempts")
	assert.Less(t, waited, 300*time.Millisecond)
	m.AssertNumberOfCalls(t, "PostComment", 2)
}

func TestPostComment_ContextCancelledWhileWaiting(t *testing.T) {
	m := vcsmocks.NewMockVCS(t)
	expectPost(m).Return(vcs.PostResult{}, retryAfterError("30")).Once()

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(10*time.Millisecond, cancel)

	start := time.Now()
	_, _, err := postComment(ctx, m, "body", retryPolicy{attempts: 2, waitBudget: time.Minute})

	require.ErrorIs(t, err, context.Canceled)
	assert.Less(t, time.Since(start), time.Second)
}

// Jitter has no injected source of randomness, so each case asserts the range
// the wait must land in rather than an exact duration.
func TestNextWait(t *testing.T) {
	p := retryPolicy{attempts: 4, baseDelay: 2 * time.Second, waitBudget: 90 * time.Second}

	tests := []struct {
		name       string
		attempt    int
		retryAfter time.Duration
		want       time.Duration
	}{
		{name: "retry-after is honoured", attempt: 1, retryAfter: 30 * time.Second, want: 30 * time.Second},
		{name: "backoff first attempt", attempt: 1, want: 2 * time.Second},
		{name: "backoff doubles", attempt: 3, want: 8 * time.Second},
		{name: "floored at minWait", attempt: 1, want: minWait},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy := p
			if tt.want == minWait {
				policy.baseDelay = 0
			}

			for range 20 {
				got := nextWait(tt.attempt, tt.retryAfter, policy)

				assert.GreaterOrEqual(t, got, tt.want)
				if tt.want > minWait {
					assert.Less(t, got, time.Duration(float64(tt.want)*1.2))
				}
			}
		})
	}
}
