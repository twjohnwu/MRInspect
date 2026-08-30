package rag

import (
	"context"
)

type noopRetriever struct{}

func (noopRetriever) Name() string { return "noop" }

func (noopRetriever) Retrieve(context.Context, Query) (Result, error) {
	return Result{Degraded: []string{"rag not configured"}}, nil
}

func (noopRetriever) Close() error { return nil }
