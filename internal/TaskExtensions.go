package internal

import (
	"context"
	"fmt"
	"time"
)

// ActionTimeoutTaskCallback represents a callback function that performs a task with a cancellation context
type ActionTimeoutTaskCallback[T any] func(ctx context.Context) (T, error)

// ActionOnTimeOutRetry represents a callback function invoked on retry after a timeout or error
type ActionOnTimeOutRetry func(retryAttemptCount, retryAttemptTotal, timeOutSecond, timeOutStep int)

// DefaultTimeoutSec is the default timeout duration in seconds
const DefaultTimeoutSec = 20

// DefaultRetryAttempt is the default number of retry attempts
const DefaultRetryAttempt = 10

// WaitForRetry executes a task with retry logic and timeout handling
func WaitForRetry[T any](
	ctx context.Context,
	callback ActionTimeoutTaskCallback[T],
	timeout *int,
	timeoutStep *int,
	retryAttempt *int,
	actionOnRetry ActionOnTimeOutRetry,
) (T, error) {
	var zero T

	// Set default values if not provided
	timeoutVal := DefaultTimeoutSec
	if timeout != nil {
		timeoutVal = *timeout
	}

	retryAttemptVal := DefaultRetryAttempt
	if retryAttempt != nil {
		retryAttemptVal = *retryAttempt
	}

	timeoutStepVal := 0
	if timeoutStep != nil {
		timeoutStepVal = *timeoutStep
	}

	retryAttemptCurrent := 1
	var lastError error

	for retryAttemptCurrent <= retryAttemptVal {
		// Create a context with timeout
		timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutVal)*time.Second)
		defer cancel()

		// Execute the callback
		result, err := callback(timeoutCtx)
		if err == nil {
			return result, nil
		}

		// Check if the context was canceled (either by parent or timeout)
		if ctx.Err() != nil {
			return zero, ctx.Err()
		}

		if timeoutCtx.Err() != nil {
			lastError = fmt.Errorf("operation timed out: %w", err)
			msg := fmt.Sprintf("The operation has timed out! Retrying attempt left: %d/%d", retryAttemptCurrent, retryAttemptVal)
			if LogHandler != nil {
				PushLogWarning(nil, msg)
			}
		} else {
			lastError = err
			msg := fmt.Sprintf("The operation has thrown an exception! Retrying attempt left: %d/%d\n%v", retryAttemptCurrent, retryAttemptVal, err)
			if LogHandler != nil {
				PushLogError(nil, msg)
			}
		}

		// Invoke retry callback if provided
		if actionOnRetry != nil {
			actionOnRetry(retryAttemptCurrent, retryAttemptVal, timeoutVal, timeoutStepVal)
		}

		retryAttemptCurrent++
		timeoutVal += timeoutStepVal

		// If this was the last attempt, break out with the error
		if retryAttemptCurrent > retryAttemptVal {
			break
		}
	}

	// If we have a last error and the parent context wasn't canceled, return it
	if lastError != nil && ctx.Err() == nil {
		return zero, lastError
	}

	// Default to timeout error if no specific error was set
	return zero, fmt.Errorf("the operation has timed out after %d attempts", retryAttemptVal)
}
